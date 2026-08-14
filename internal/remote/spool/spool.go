// Package spool provides a durable, on-disk buffer for battery telemetry on the
// agent. Every snapshot is appended to a local SQLite database before any attempt
// to send it to the control server, so data collected while the WebSocket link is
// down (or across an agent restart) is preserved and replayed in order once the
// connection is restored. This keeps the server-side session history — and the
// reports built from it — complete across outages.
package spool

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"

	"gok-pi/metrics/observers"
)

// Entry is a stored telemetry snapshot with its spool row ID.
type Entry struct {
	ID       int64
	Snapshot observers.Snapshot
}

// Spool is the agent-side durable telemetry buffer backed by SQLite.
type Spool struct {
	db  *sqlx.DB
	log *slog.Logger
}

// Open creates or opens the spool database at the given path.
func Open(path string, log *slog.Logger) (*Spool, error) {
	db, err := sqlx.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open spool db: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite is single-writer

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS telemetry (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			recorded_at TEXT    NOT NULL,
			payload     TEXT    NOT NULL,
			delivered   INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_telemetry_undelivered ON telemetry(delivered, id);
		CREATE INDEX IF NOT EXISTS idx_telemetry_recorded ON telemetry(recorded_at);
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate spool db: %w", err)
	}

	return &Spool{db: db, log: log.With(slog.String("component", "telemetry-spool"))}, nil
}

// Append stores a telemetry snapshot durably. recorded_at uses the snapshot's own
// timestamp so the server can reconstruct session timing on replay.
func (s *Spool) Append(snap observers.Snapshot) error {
	payload, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	recordedAt := snap.UpdatedAt
	if recordedAt.IsZero() {
		recordedAt = time.Now().UTC()
	}
	_, err = s.db.Exec(
		`INSERT INTO telemetry (recorded_at, payload) VALUES (?, ?)`,
		recordedAt.UTC().Format(time.RFC3339Nano), string(payload),
	)
	if err != nil {
		return fmt.Errorf("append snapshot: %w", err)
	}
	return nil
}

// Undelivered returns up to limit snapshots not yet acknowledged as sent, oldest first.
func (s *Spool) Undelivered(limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 200
	}
	var rows []struct {
		ID      int64  `db:"id"`
		Payload string `db:"payload"`
	}
	if err := s.db.Select(&rows,
		`SELECT id, payload FROM telemetry WHERE delivered = 0 ORDER BY id LIMIT ?`, limit,
	); err != nil {
		return nil, fmt.Errorf("query undelivered: %w", err)
	}

	entries := make([]Entry, 0, len(rows))
	for _, r := range rows {
		var snap observers.Snapshot
		if err := json.Unmarshal([]byte(r.Payload), &snap); err != nil {
			// A corrupt row would otherwise wedge replay forever; mark it delivered and skip.
			s.log.Warn("dropping unparseable spool entry", slog.Int64("id", r.ID), slog.Any("error", err))
			_ = s.MarkDelivered(r.ID)
			continue
		}
		entries = append(entries, Entry{ID: r.ID, Snapshot: snap})
	}
	return entries, nil
}

// MarkDelivered marks all rows up to and including maxID as sent.
func (s *Spool) MarkDelivered(maxID int64) error {
	_, err := s.db.Exec(`UPDATE telemetry SET delivered = 1 WHERE id <= ? AND delivered = 0`, maxID)
	if err != nil {
		return fmt.Errorf("mark delivered: %w", err)
	}
	return nil
}

// Stats is a point-in-time picture of the buffer. OldestUndelivered is the whole
// diagnosis when telemetry stops reaching the server: a backlog that keeps growing
// while its oldest row keeps ageing means the uplink is not draining, which is
// invisible from the agent log alone.
type Stats struct {
	Total             int64      `json:"total"`
	Pending           int64      `json:"pending"`
	OldestUndelivered *time.Time `json:"oldest_undelivered,omitempty"`
	NewestRecorded    *time.Time `json:"newest_recorded,omitempty"`
}

// Stats reports the buffer's size and the age of its backlog in one pass.
func (s *Spool) Stats() (Stats, error) {
	var row struct {
		Total   int64          `db:"total"`
		Pending int64          `db:"pending"`
		Oldest  sql.NullString `db:"oldest"`
		Newest  sql.NullString `db:"newest"`
	}
	err := s.db.Get(&row, `
		SELECT COUNT(*) AS total,
		       COALESCE(SUM(CASE WHEN delivered = 0 THEN 1 ELSE 0 END), 0) AS pending,
		       MIN(CASE WHEN delivered = 0 THEN recorded_at END) AS oldest,
		       MAX(recorded_at) AS newest
		FROM telemetry`)
	if err != nil {
		return Stats{}, fmt.Errorf("spool stats: %w", err)
	}

	stats := Stats{Total: row.Total, Pending: row.Pending}
	if t, ok := parseStamp(row.Oldest); ok {
		stats.OldestUndelivered = &t
	}
	if t, ok := parseStamp(row.Newest); ok {
		stats.NewestRecorded = &t
	}
	return stats, nil
}

func parseStamp(v sql.NullString) (time.Time, bool) {
	if !v.Valid || v.String == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, v.String)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// PendingCount returns the number of undelivered snapshots.
func (s *Spool) PendingCount() (int64, error) {
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM telemetry WHERE delivered = 0`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// Prune removes delivered rows older than deliveredRetention and any row older
// than maxAge (a hard backstop so a permanently offline agent cannot fill the disk).
func (s *Spool) Prune(deliveredRetention, maxAge time.Duration) (int64, error) {
	now := time.Now().UTC()
	deliveredCutoff := now.Add(-deliveredRetention).Format(time.RFC3339Nano)
	hardCutoff := now.Add(-maxAge).Format(time.RFC3339Nano)

	res, err := s.db.Exec(
		`DELETE FROM telemetry WHERE (delivered = 1 AND recorded_at < ?) OR recorded_at < ?`,
		deliveredCutoff, hardCutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("prune spool: %w", err)
	}
	return res.RowsAffected()
}

// Close closes the database.
func (s *Spool) Close() error {
	return s.db.Close()
}

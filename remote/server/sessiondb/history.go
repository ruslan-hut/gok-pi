package sessiondb

import (
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// TelemetryPoint is one minute of battery telemetry, aggregated from the ~6 frames
// an agent sends per minute. Sessions record only what happened inside a manual-mode
// operation; this table is the continuous record an operator needs to answer "what
// was the battery doing at 21:40 yesterday" regardless of mode.
//
// Samples is the number of frames that went into the bucket. It doubles as the
// uplink health signal: a full minute is 6 frames at the agent's 10s poll interval,
// so a bucket with fewer — or a minute with no row at all — is telemetry that never
// arrived.
type TelemetryPoint struct {
	AgentID     string `db:"agent_id"     json:"agent_id"`
	BatteryName string `db:"battery_name" json:"battery_name"`
	Bucket      string `db:"bucket"       json:"bucket"` // RFC3339 UTC, truncated to the minute

	USOC                float64 `db:"usoc"                  json:"usoc"`                  // user-facing SoC at the end of the minute (%)
	RSOC                float64 `db:"rsoc"                  json:"rsoc"`                  // raw SoC at the end of the minute (%)
	RemainingCapacityWh float64 `db:"remaining_capacity_wh" json:"remaining_capacity_wh"` // at the end of the minute

	ConsumptionAvgW float64 `db:"consumption_avg_w" json:"consumption_avg_w"` // house load, mean over the minute
	ConsumptionMaxW float64 `db:"consumption_max_w" json:"consumption_max_w"`

	// Battery power is signed and swings within a minute, so the extremes are kept
	// alongside the mean: an averaged-out spike is invisible but matters to an operator.
	PacAvgW float64 `db:"pac_avg_w" json:"pac_avg_w"`
	PacMinW float64 `db:"pac_min_w" json:"pac_min_w"`
	PacMaxW float64 `db:"pac_max_w" json:"pac_max_w"`

	OperatingMode string `db:"operating_mode" json:"operating_mode"` // last mode seen in the minute
	Samples       int    `db:"samples"        json:"samples"`
}

// HistoryQuery selects a window of telemetry history. AgentID and BatteryName are
// optional filters; Until zero means "up to now".
type HistoryQuery struct {
	AgentID     string
	BatteryName string
	Since       time.Time
	Until       time.Time
	Limit       int
}

// HourCoverage reports how much telemetry actually landed in one hour. Buckets is
// how many distinct minutes carry data (60 = no gap) and Samples the total frames
// (360 = a complete hour at the 10s poll interval).
type HourCoverage struct {
	Hour        string `db:"hour"         json:"hour"` // "YYYY-MM-DDTHH" in UTC
	AgentID     string `db:"agent_id"     json:"agent_id"`
	BatteryName string `db:"battery_name" json:"battery_name"`
	Buckets     int    `db:"buckets"      json:"buckets"`
	Samples     int    `db:"samples"      json:"samples"`
}

// migrateHistory creates the telemetry_history table. The unique index on
// (agent_id, battery_name, bucket) gives the recorder upsert semantics: it can
// write the in-progress minute repeatedly for a live UI and correct it as more
// frames arrive, and a spool replay that re-delivers an old minute overwrites it
// instead of duplicating it.
func migrateHistory(db *sqlx.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS telemetry_history (
			agent_id            TEXT    NOT NULL,
			battery_name        TEXT    NOT NULL,
			bucket              TEXT    NOT NULL,
			usoc                REAL    NOT NULL DEFAULT 0,
			rsoc                REAL    NOT NULL DEFAULT 0,
			remaining_capacity_wh REAL  NOT NULL DEFAULT 0,
			consumption_avg_w   REAL    NOT NULL DEFAULT 0,
			consumption_max_w   REAL    NOT NULL DEFAULT 0,
			pac_avg_w           REAL    NOT NULL DEFAULT 0,
			pac_min_w           REAL    NOT NULL DEFAULT 0,
			pac_max_w           REAL    NOT NULL DEFAULT 0,
			operating_mode      TEXT    NOT NULL DEFAULT '',
			samples             INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (agent_id, battery_name, bucket)
		);
		CREATE INDEX IF NOT EXISTS idx_history_bucket ON telemetry_history(bucket);
	`)
	return err
}

// UpsertTelemetryPoints writes a batch of minute buckets in one transaction.
func (s *Store) UpsertTelemetryPoints(points []TelemetryPoint) error {
	if len(points) == 0 {
		return nil
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return fmt.Errorf("begin history tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, p := range points {
		_, err := tx.Exec(`
			INSERT INTO telemetry_history (
				agent_id, battery_name, bucket, usoc, rsoc, remaining_capacity_wh,
				consumption_avg_w, consumption_max_w, pac_avg_w, pac_min_w, pac_max_w,
				operating_mode, samples
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(agent_id, battery_name, bucket) DO UPDATE SET
				usoc = excluded.usoc,
				rsoc = excluded.rsoc,
				remaining_capacity_wh = excluded.remaining_capacity_wh,
				consumption_avg_w = excluded.consumption_avg_w,
				consumption_max_w = excluded.consumption_max_w,
				pac_avg_w = excluded.pac_avg_w,
				pac_min_w = excluded.pac_min_w,
				pac_max_w = excluded.pac_max_w,
				operating_mode = excluded.operating_mode,
				samples = excluded.samples`,
			p.AgentID, p.BatteryName, p.Bucket, p.USOC, p.RSOC, p.RemainingCapacityWh,
			p.ConsumptionAvgW, p.ConsumptionMaxW, p.PacAvgW, p.PacMinW, p.PacMaxW,
			p.OperatingMode, p.Samples,
		)
		if err != nil {
			return fmt.Errorf("upsert history point: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit history tx: %w", err)
	}
	return nil
}

// QueryTelemetryHistory returns minute buckets in the window, oldest first.
func (s *Store) QueryTelemetryHistory(q HistoryQuery) ([]TelemetryPoint, error) {
	query := `SELECT agent_id, battery_name, bucket, usoc, rsoc, remaining_capacity_wh,
	                 consumption_avg_w, consumption_max_w, pac_avg_w, pac_min_w, pac_max_w,
	                 operating_mode, samples
	          FROM telemetry_history
	          WHERE bucket >= ?`
	args := []interface{}{q.Since.UTC().Format(time.RFC3339)}

	if !q.Until.IsZero() {
		query += " AND bucket < ?"
		args = append(args, q.Until.UTC().Format(time.RFC3339))
	}
	if q.AgentID != "" {
		query += " AND agent_id = ?"
		args = append(args, q.AgentID)
	}
	if q.BatteryName != "" {
		query += " AND battery_name = ?"
		args = append(args, q.BatteryName)
	}
	query += " ORDER BY bucket ASC"

	if q.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, q.Limit)
	}

	var points []TelemetryPoint
	if err := s.db.Select(&points, query, args...); err != nil {
		return nil, fmt.Errorf("query telemetry history: %w", err)
	}
	return points, nil
}

// TelemetryCoverage reports per-hour bucket and sample counts for the window. A
// complete hour is 60 buckets / 360 samples per battery; anything less is telemetry
// that never reached the server, which is what makes an uplink stall visible after
// the fact instead of only while it is happening.
func (s *Store) TelemetryCoverage(agentID string, since, until time.Time) ([]HourCoverage, error) {
	query := `SELECT substr(bucket, 1, 13) AS hour, agent_id, battery_name,
	                 COUNT(*) AS buckets, COALESCE(SUM(samples), 0) AS samples
	          FROM telemetry_history
	          WHERE bucket >= ?`
	args := []interface{}{since.UTC().Format(time.RFC3339)}

	if !until.IsZero() {
		query += " AND bucket < ?"
		args = append(args, until.UTC().Format(time.RFC3339))
	}
	if agentID != "" {
		query += " AND agent_id = ?"
		args = append(args, agentID)
	}
	query += " GROUP BY hour, agent_id, battery_name ORDER BY hour ASC"

	var rows []HourCoverage
	if err := s.db.Select(&rows, query, args...); err != nil {
		return nil, fmt.Errorf("query telemetry coverage: %w", err)
	}
	return rows, nil
}

// CleanupHistory removes buckets older than the retention window.
func (s *Store) CleanupHistory(retention time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-retention).Format(time.RFC3339)
	res, err := s.db.Exec(`DELETE FROM telemetry_history WHERE bucket < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("cleanup telemetry history: %w", err)
	}
	return res.RowsAffected()
}

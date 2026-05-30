package sessiondb

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// ComputedSchedule is the database representation of a price-computed schedule window.
// Each record represents a single charge or discharge window for one battery on one day,
// computed from P20/P80 percentile analysis of PVPC hourly prices.
// Schedules are always valid for exactly one day (00:00–23:59, no cross-midnight windows).
type ComputedSchedule struct {
	ID          int64   `db:"id"           json:"id"`
	Date        string  `db:"date"         json:"date"`         // "YYYY-MM-DD" (Madrid timezone)
	BatteryName string  `db:"battery_name" json:"battery_name"` // device this schedule applies to
	Type        string  `db:"type"         json:"type"`         // "charge" or "discharge"
	StartHour   int     `db:"start_hour"   json:"start_hour"`   // 0-23
	EndHour     int     `db:"end_hour"     json:"end_hour"`     // 1-24 (exclusive)
	AvgPrice    float64 `db:"avg_price"    json:"avg_price"`    // average price in window (EUR/MWh)
	PowerLimit  int     `db:"power_limit"  json:"power_limit"`  // max power in Watts
	SocLimit    int     `db:"soc_limit"    json:"soc_limit"`    // SoC limit (0-100)
	Low         float64 `db:"low"          json:"low"`          // low percentile threshold (charge)
	High        float64 `db:"high"         json:"high"`         // high percentile threshold (discharge)
	ComputedAt  string  `db:"computed_at"  json:"computed_at"`  // RFC3339 timestamp
}

// migrateSchedules creates the computed_schedules table if it does not exist.
// The unique index on (date, battery_name, type, start_hour, end_hour) enables
// upsert semantics: recomputing the same day updates existing rows without
// changing their IDs, so schedule names pushed to agents remain stable.
func migrateSchedules(db *sqlx.DB) error {
	// Migrate from old p25/p75 columns to low/high.
	// Check if the table exists with old schema and recreate it.
	// Computed schedules are ephemeral (recomputed daily), so data loss is acceptable.
	var colName string
	err := db.QueryRow(`SELECT name FROM pragma_table_info('computed_schedules') WHERE name = 'low'`).Scan(&colName)
	switch {
	case err == nil:
		// Column 'low' exists: already the current schema, nothing to migrate.
	case errors.Is(err, sql.ErrNoRows):
		// Column 'low' is absent (old schema, or table doesn't exist yet): drop so
		// it gets recreated below. Schedules are ephemeral, so data loss is fine.
		if _, derr := db.Exec(`DROP TABLE IF EXISTS computed_schedules`); derr != nil {
			return fmt.Errorf("drop old computed_schedules table: %w", derr)
		}
	default:
		// A genuine DB error: surface it rather than silently dropping the table.
		return fmt.Errorf("probe computed_schedules schema: %w", err)
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS computed_schedules (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			date         TEXT    NOT NULL,
			battery_name TEXT    NOT NULL,
			type         TEXT    NOT NULL,
			start_hour   INTEGER NOT NULL,
			end_hour     INTEGER NOT NULL,
			avg_price    REAL    NOT NULL DEFAULT 0,
			power_limit  INTEGER NOT NULL DEFAULT 2000,
			soc_limit    INTEGER NOT NULL DEFAULT 100,
			low          REAL    NOT NULL DEFAULT 0,
			high         REAL    NOT NULL DEFAULT 0,
			computed_at  TEXT    NOT NULL
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_cs_unique
			ON computed_schedules(date, battery_name, type, start_hour, end_hour);
		CREATE INDEX IF NOT EXISTS idx_cs_date
			ON computed_schedules(date);
	`)
	return err
}

// UpsertSchedules inserts or updates computed schedule records in a single transaction.
// On conflict (same date+battery+type+hours), it updates price/limits/thresholds
// while preserving the stable row ID for consistent schedule naming.
func (s *Store) UpsertSchedules(schedules []ComputedSchedule) error {
	if len(schedules) == 0 {
		return nil
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Preparex(`
		INSERT INTO computed_schedules (date, battery_name, type, start_hour, end_hour,
			avg_price, power_limit, soc_limit, low, high, computed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(date, battery_name, type, start_hour, end_hour) DO UPDATE SET
			avg_price   = excluded.avg_price,
			power_limit = excluded.power_limit,
			soc_limit   = excluded.soc_limit,
			low         = excluded.low,
			high        = excluded.high,
			computed_at = excluded.computed_at
	`)
	if err != nil {
		return fmt.Errorf("prepare upsert: %w", err)
	}
	defer stmt.Close()

	for _, cs := range schedules {
		if _, err := stmt.Exec(
			cs.Date, cs.BatteryName, cs.Type, cs.StartHour, cs.EndHour,
			cs.AvgPrice, cs.PowerLimit, cs.SocLimit, cs.Low, cs.High, cs.ComputedAt,
		); err != nil {
			return fmt.Errorf("upsert schedule (date=%s battery=%s %s %d-%d): %w",
				cs.Date, cs.BatteryName, cs.Type, cs.StartHour, cs.EndHour, err)
		}
	}

	return tx.Commit()
}

// GetSchedulesByDate returns all computed schedules for the given date (YYYY-MM-DD),
// ordered by start hour. Used to build the set of auto-schedules to push to agents.
func (s *Store) GetSchedulesByDate(date string) ([]ComputedSchedule, error) {
	var rows []ComputedSchedule
	err := s.db.Select(&rows,
		`SELECT id, date, battery_name, type, start_hour, end_hour,
		        avg_price, power_limit, soc_limit, low, high, computed_at
		 FROM computed_schedules WHERE date = ? ORDER BY start_hour`, date)
	if err != nil {
		return nil, fmt.Errorf("query schedules for date %s: %w", date, err)
	}
	return rows, nil
}

// CleanupSchedules deletes computed schedules older than the given retention period.
func (s *Store) CleanupSchedules(retention time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-retention).Format("2006-01-02")
	result, err := s.db.Exec("DELETE FROM computed_schedules WHERE date < ?", cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

package sessiondb

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// SessionRecord is the database representation of a charge/discharge session.
// Only manual-mode (scheduled) sessions are recorded.
type SessionRecord struct {
	ID          int64      `db:"id" json:"id"`
	AgentID     string     `db:"agent_id" json:"agent_id"`
	BatteryName string     `db:"battery_name" json:"battery_name"`
	Type        string     `db:"type" json:"type"` // "charge" or "discharge"
	StartedAt   time.Time  `db:"started_at" json:"started_at"`
	EndedAt     *time.Time `db:"ended_at" json:"ended_at,omitempty"`
	DurationSec float64    `db:"duration_sec" json:"duration_seconds,omitempty"`

	// Energy & power
	EnergyWh   float64 `db:"energy_wh" json:"energy_wh"`     // accumulated energy (Wh)
	AvgPowerW  float64 `db:"avg_power_w" json:"avg_power_w"` // average power during session
	PeakPowerW float64 `db:"peak_power_w" json:"peak_power_w"`

	// SoC
	SocStart float64 `db:"soc_start" json:"soc_start"` // USOC at session start (%)
	SocEnd   float64 `db:"soc_end" json:"soc_end"`     // USOC at session end (%)

	// Price & cost (signed: discharge = positive/income, charge = negative/expense)
	AvgPriceEurMWh float64 `db:"avg_price_eur_mwh" json:"avg_price_eur_mwh"` // average electricity price
	CostEur        float64 `db:"cost_eur" json:"cost_eur"`                   // signed cost

	Samples int `db:"samples" json:"samples"` // number of telemetry samples
}

// BatterySummary aggregates sessions per battery for display.
type BatterySummary struct {
	BatteryName       string  `json:"battery_name"`
	ChargeEnergyWh    float64 `json:"charge_energy_wh"`
	ChargeCostEur     float64 `json:"charge_cost_eur"`
	DischargeEnergyWh float64 `json:"discharge_energy_wh"`
	DischargeCostEur  float64 `json:"discharge_cost_eur"`
	NetCostEur        float64 `json:"net_cost_eur"`
	ActiveCharge      bool    `json:"active_charge"`
	ActiveDischarge   bool    `json:"active_discharge"`
}

// Store persists session data to SQLite.
type Store struct {
	db  *sqlx.DB
	log *slog.Logger
}

// Open creates or opens a SQLite database at the given path.
func Open(path string, log *slog.Logger) (*Store, error) {
	db, err := sqlx.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open session db: %w", err)
	}

	db.SetMaxOpenConns(1) // SQLite is single-writer

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate session db: %w", err)
	}

	if err := migrateSchedules(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate computed_schedules: %w", err)
	}

	return &Store{db: db, log: log.With(slog.String("component", "session-db"))}, nil
}

func migrate(db *sqlx.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			agent_id        TEXT    NOT NULL,
			battery_name    TEXT    NOT NULL,
			type            TEXT    NOT NULL,
			started_at      TEXT    NOT NULL,
			ended_at        TEXT,
			duration_sec    REAL    NOT NULL DEFAULT 0,
			energy_wh       REAL    NOT NULL DEFAULT 0,
			avg_power_w     REAL    NOT NULL DEFAULT 0,
			peak_power_w    REAL    NOT NULL DEFAULT 0,
			soc_start       REAL    NOT NULL DEFAULT 0,
			soc_end         REAL    NOT NULL DEFAULT 0,
			avg_price_eur_mwh REAL  NOT NULL DEFAULT 0,
			cost_eur        REAL    NOT NULL DEFAULT 0,
			samples         INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_sessions_agent_battery ON sessions(agent_id, battery_name);
		CREATE INDEX IF NOT EXISTS idx_sessions_started ON sessions(started_at);
	`)
	return err
}

// InsertSession creates a new open session and returns its ID.
func (s *Store) InsertSession(rec *SessionRecord) (int64, error) {
	result, err := s.db.Exec(`
		INSERT INTO sessions (agent_id, battery_name, type, started_at, soc_start)
		VALUES (?, ?, ?, ?, ?)`,
		rec.AgentID, rec.BatteryName, rec.Type,
		rec.StartedAt.UTC().Format(time.RFC3339),
		rec.SocStart,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// UpdateSession writes accumulated telemetry data to an open session.
func (s *Store) UpdateSession(id int64, energyWh, avgPowerW, peakPowerW, socEnd, avgPriceEurMWh, costEur float64, samples int) error {
	_, err := s.db.Exec(`
		UPDATE sessions
		SET energy_wh = ?, avg_power_w = ?, peak_power_w = ?, soc_end = ?,
		    avg_price_eur_mwh = ?, cost_eur = ?, samples = ?
		WHERE id = ?`,
		energyWh, avgPowerW, peakPowerW, socEnd, avgPriceEurMWh, costEur, samples, id,
	)
	return err
}

// CloseSession finalizes a session with end time and cost.
func (s *Store) CloseSession(id int64, endedAt time.Time, durationSec, energyWh, avgPowerW, peakPowerW, socEnd, avgPriceEurMWh, costEur float64, samples int) error {
	_, err := s.db.Exec(`
		UPDATE sessions
		SET ended_at = ?, duration_sec = ?, energy_wh = ?, avg_power_w = ?,
		    peak_power_w = ?, soc_end = ?, avg_price_eur_mwh = ?, cost_eur = ?, samples = ?
		WHERE id = ?`,
		endedAt.UTC().Format(time.RFC3339),
		durationSec, energyWh, avgPowerW, peakPowerW, socEnd,
		avgPriceEurMWh, costEur, samples, id,
	)
	return err
}

// DeleteSession removes a single session row, used to drop throwaway sessions.
func (s *Store) DeleteSession(id int64) error {
	_, err := s.db.Exec("DELETE FROM sessions WHERE id = ?", id)
	return err
}

// GetOpenSessions returns all sessions that have not been closed.
func (s *Store) GetOpenSessions() ([]SessionRecord, error) {
	var rows []struct {
		ID          int64   `db:"id"`
		AgentID     string  `db:"agent_id"`
		BatteryName string  `db:"battery_name"`
		Type        string  `db:"type"`
		StartedAt   string  `db:"started_at"`
		SocStart    float64 `db:"soc_start"`
		EnergyWh    float64 `db:"energy_wh"`
		AvgPowerW   float64 `db:"avg_power_w"`
		PeakPowerW  float64 `db:"peak_power_w"`
		SocEnd      float64 `db:"soc_end"`
		Samples     int     `db:"samples"`
	}
	err := s.db.Select(&rows, `
		SELECT id, agent_id, battery_name, type, started_at, soc_start,
		       energy_wh, avg_power_w, peak_power_w, soc_end, samples
		FROM sessions WHERE ended_at IS NULL`)
	if err != nil {
		return nil, err
	}

	result := make([]SessionRecord, len(rows))
	for i, r := range rows {
		t, _ := time.Parse(time.RFC3339, r.StartedAt)
		result[i] = SessionRecord{
			ID: r.ID, AgentID: r.AgentID, BatteryName: r.BatteryName,
			Type: r.Type, StartedAt: t, SocStart: r.SocStart,
			EnergyWh: r.EnergyWh, AvgPowerW: r.AvgPowerW,
			PeakPowerW: r.PeakPowerW, SocEnd: r.SocEnd, Samples: r.Samples,
		}
	}
	return result, nil
}

// GetSummaries returns per-battery aggregated data for the last N hours.
func (s *Store) GetSummaries(agentID string, hours int) ([]BatterySummary, error) {
	since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
	return s.GetSummariesRange(agentID, since, time.Time{})
}

// GetSummariesRange returns per-battery aggregated data for a time range.
// If until is zero, no upper bound is applied.
func (s *Store) GetSummariesRange(agentID string, since, until time.Time) ([]BatterySummary, error) {
	query := `
		SELECT battery_name, type,
		       COALESCE(SUM(energy_wh), 0) as total_energy_wh,
		       COALESCE(SUM(cost_eur), 0) as total_cost_eur
		FROM sessions
		WHERE started_at >= ?`
	args := []interface{}{since.Format(time.RFC3339)}

	if !until.IsZero() {
		query += " AND started_at < ?"
		args = append(args, until.Format(time.RFC3339))
	}
	if agentID != "" {
		query += " AND agent_id = ?"
		args = append(args, agentID)
	}
	query += " GROUP BY battery_name, type"

	var rows []struct {
		BatteryName   string  `db:"battery_name"`
		Type          string  `db:"type"`
		TotalEnergyWh float64 `db:"total_energy_wh"`
		TotalCostEur  float64 `db:"total_cost_eur"`
	}
	if err := s.db.Select(&rows, query, args...); err != nil {
		return nil, err
	}

	byBattery := make(map[string]*BatterySummary)
	for _, r := range rows {
		sum, ok := byBattery[r.BatteryName]
		if !ok {
			sum = &BatterySummary{BatteryName: r.BatteryName}
			byBattery[r.BatteryName] = sum
		}
		switch r.Type {
		case "charge":
			sum.ChargeEnergyWh = r.TotalEnergyWh
			sum.ChargeCostEur = r.TotalCostEur
		case "discharge":
			sum.DischargeEnergyWh = r.TotalEnergyWh
			sum.DischargeCostEur = r.TotalCostEur
		}
	}

	var result []BatterySummary
	for _, v := range byBattery {
		v.NetCostEur = v.ChargeCostEur + v.DischargeCostEur
		result = append(result, *v)
	}
	return result, nil
}

// GetRecentSessions returns sessions from the last N hours.
func (s *Store) GetRecentSessions(agentID string, hours int) ([]SessionRecord, error) {
	cutoff := time.Now().UTC().Add(-time.Duration(hours) * time.Hour).Format(time.RFC3339)

	query := `SELECT * FROM sessions WHERE started_at >= ?`
	args := []interface{}{cutoff}
	if agentID != "" {
		query += " AND agent_id = ?"
		args = append(args, agentID)
	}
	query += " ORDER BY started_at DESC"

	var raw []struct {
		ID             int64          `db:"id"`
		AgentID        string         `db:"agent_id"`
		BatteryName    string         `db:"battery_name"`
		Type           string         `db:"type"`
		StartedAt      string         `db:"started_at"`
		EndedAt        sql.NullString `db:"ended_at"`
		DurationSec    float64        `db:"duration_sec"`
		EnergyWh       float64        `db:"energy_wh"`
		AvgPowerW      float64        `db:"avg_power_w"`
		PeakPowerW     float64        `db:"peak_power_w"`
		SocStart       float64        `db:"soc_start"`
		SocEnd         float64        `db:"soc_end"`
		AvgPriceEurMWh float64        `db:"avg_price_eur_mwh"`
		CostEur        float64        `db:"cost_eur"`
		Samples        int            `db:"samples"`
	}
	if err := s.db.Select(&raw, query, args...); err != nil {
		return nil, err
	}

	result := make([]SessionRecord, len(raw))
	for i, r := range raw {
		startedAt, _ := time.Parse(time.RFC3339, r.StartedAt)
		rec := SessionRecord{
			ID: r.ID, AgentID: r.AgentID, BatteryName: r.BatteryName,
			Type: r.Type, StartedAt: startedAt, DurationSec: r.DurationSec,
			EnergyWh: r.EnergyWh, AvgPowerW: r.AvgPowerW, PeakPowerW: r.PeakPowerW,
			SocStart: r.SocStart, SocEnd: r.SocEnd,
			AvgPriceEurMWh: r.AvgPriceEurMWh, CostEur: r.CostEur, Samples: r.Samples,
		}
		if r.EndedAt.Valid {
			t, _ := time.Parse(time.RFC3339, r.EndedAt.String)
			rec.EndedAt = &t
		}
		result[i] = rec
	}
	return result, nil
}

// Cleanup deletes sessions older than the given retention period.
func (s *Store) Cleanup(retention time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-retention).Format(time.RFC3339)
	result, err := s.db.Exec("DELETE FROM sessions WHERE started_at < ? AND ended_at IS NOT NULL", cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// DBStats holds database statistics for monitoring.
type DBStats struct {
	FileSizeBytes int64  `json:"file_size_bytes"`
	TotalSessions int64  `json:"total_sessions"`
	OpenSessions  int64  `json:"open_sessions"`
	OldestSession string `json:"oldest_session,omitempty"` // RFC3339
	NewestSession string `json:"newest_session,omitempty"` // RFC3339
}

// AgentDBStats holds per-agent session counts.
type AgentDBStats struct {
	AgentID           string  `json:"agent_id"`
	TotalSessions     int64   `json:"total_sessions"`
	ChargeSessions    int64   `json:"charge_sessions"`
	DischargeSessions int64   `json:"discharge_sessions"`
	TotalEnergyWh     float64 `json:"total_energy_wh"`
	NetCostEur        float64 `json:"net_cost_eur"`
}

// GetStats returns overall database statistics.
func (s *Store) GetStats() (*DBStats, error) {
	stats := &DBStats{}

	// File size
	var pageCount, pageSize int64
	if err := s.db.QueryRow("PRAGMA page_count").Scan(&pageCount); err != nil {
		return nil, fmt.Errorf("query page_count: %w", err)
	}
	if err := s.db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		return nil, fmt.Errorf("query page_size: %w", err)
	}
	stats.FileSizeBytes = pageCount * pageSize

	// Counts
	if err := s.db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&stats.TotalSessions); err != nil {
		return nil, fmt.Errorf("query total sessions: %w", err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM sessions WHERE ended_at IS NULL").Scan(&stats.OpenSessions); err != nil {
		return nil, fmt.Errorf("query open sessions: %w", err)
	}

	// Date range
	var oldest, newest sql.NullString
	if err := s.db.QueryRow("SELECT MIN(started_at) FROM sessions").Scan(&oldest); err != nil {
		return nil, fmt.Errorf("query oldest session: %w", err)
	}
	if err := s.db.QueryRow("SELECT MAX(started_at) FROM sessions").Scan(&newest); err != nil {
		return nil, fmt.Errorf("query newest session: %w", err)
	}
	if oldest.Valid {
		stats.OldestSession = oldest.String
	}
	if newest.Valid {
		stats.NewestSession = newest.String
	}

	return stats, nil
}

// GetAgentStats returns per-agent session statistics.
func (s *Store) GetAgentStats() ([]AgentDBStats, error) {
	var rows []struct {
		AgentID     string  `db:"agent_id"`
		Total       int64   `db:"total"`
		Charges     int64   `db:"charges"`
		Discharges  int64   `db:"discharges"`
		TotalEnergy float64 `db:"total_energy"`
		TotalCost   float64 `db:"total_cost"`
	}
	err := s.db.Select(&rows, `
		SELECT agent_id,
		       COUNT(*) as total,
		       SUM(CASE WHEN type = 'charge' THEN 1 ELSE 0 END) as charges,
		       SUM(CASE WHEN type = 'discharge' THEN 1 ELSE 0 END) as discharges,
		       COALESCE(SUM(energy_wh), 0) as total_energy,
		       COALESCE(SUM(cost_eur), 0) as total_cost
		FROM sessions
		WHERE ended_at IS NOT NULL
		GROUP BY agent_id`)
	if err != nil {
		return nil, err
	}

	result := make([]AgentDBStats, len(rows))
	for i, r := range rows {
		result[i] = AgentDBStats{
			AgentID:           r.AgentID,
			TotalSessions:     r.Total,
			ChargeSessions:    r.Charges,
			DischargeSessions: r.Discharges,
			TotalEnergyWh:     r.TotalEnergy,
			NetCostEur:        r.TotalCost,
		}
	}
	return result, nil
}

// SessionQuery defines filters for querying raw session records.
type SessionQuery struct {
	AgentID  string // filter by agent_id (empty = all)
	Type     string // filter by type: "charge" or "discharge" (empty = all)
	DateFrom string // RFC3339 lower bound on started_at (empty = no lower bound)
	DateTo   string // RFC3339 upper bound on started_at (empty = no upper bound)
	Status   string // "open", "closed", or "" (all)
	Limit    int    // max records to return (0 = default 100)
	Offset   int    // pagination offset
}

// SessionQueryResult wraps paginated query results.
type SessionQueryResult struct {
	Records []SessionRecord `json:"records"`
	Total   int64           `json:"total"`
	Limit   int             `json:"limit"`
	Offset  int             `json:"offset"`
}

// QuerySessions returns filtered, paginated session records for the database inspector.
func (s *Store) QuerySessions(q SessionQuery) (*SessionQueryResult, error) {
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	where := "WHERE 1=1"
	var args []interface{}

	if q.AgentID != "" {
		where += " AND agent_id = ?"
		args = append(args, q.AgentID)
	}
	if q.Type != "" {
		where += " AND type = ?"
		args = append(args, q.Type)
	}
	if q.DateFrom != "" {
		where += " AND started_at >= ?"
		args = append(args, q.DateFrom)
	}
	if q.DateTo != "" {
		where += " AND started_at <= ?"
		args = append(args, q.DateTo)
	}
	if q.Status == "open" {
		where += " AND ended_at IS NULL"
	} else if q.Status == "closed" {
		where += " AND ended_at IS NOT NULL"
	}

	// Count total matching records
	var total int64
	countQuery := "SELECT COUNT(*) FROM sessions " + where
	if err := s.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count sessions: %w", err)
	}

	// Fetch page
	dataQuery := "SELECT * FROM sessions " + where + " ORDER BY started_at DESC LIMIT ? OFFSET ?"
	dataArgs := append(append([]interface{}{}, args...), limit, q.Offset)

	var raw []struct {
		ID             int64          `db:"id"`
		AgentID        string         `db:"agent_id"`
		BatteryName    string         `db:"battery_name"`
		Type           string         `db:"type"`
		StartedAt      string         `db:"started_at"`
		EndedAt        sql.NullString `db:"ended_at"`
		DurationSec    float64        `db:"duration_sec"`
		EnergyWh       float64        `db:"energy_wh"`
		AvgPowerW      float64        `db:"avg_power_w"`
		PeakPowerW     float64        `db:"peak_power_w"`
		SocStart       float64        `db:"soc_start"`
		SocEnd         float64        `db:"soc_end"`
		AvgPriceEurMWh float64        `db:"avg_price_eur_mwh"`
		CostEur        float64        `db:"cost_eur"`
		Samples        int            `db:"samples"`
	}
	if err := s.db.Select(&raw, dataQuery, dataArgs...); err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}

	records := make([]SessionRecord, len(raw))
	for i, r := range raw {
		startedAt, _ := time.Parse(time.RFC3339, r.StartedAt)
		rec := SessionRecord{
			ID: r.ID, AgentID: r.AgentID, BatteryName: r.BatteryName,
			Type: r.Type, StartedAt: startedAt, DurationSec: r.DurationSec,
			EnergyWh: r.EnergyWh, AvgPowerW: r.AvgPowerW, PeakPowerW: r.PeakPowerW,
			SocStart: r.SocStart, SocEnd: r.SocEnd,
			AvgPriceEurMWh: r.AvgPriceEurMWh, CostEur: r.CostEur, Samples: r.Samples,
		}
		if r.EndedAt.Valid {
			t, _ := time.Parse(time.RFC3339, r.EndedAt.String)
			rec.EndedAt = &t
		}
		records[i] = rec
	}

	return &SessionQueryResult{
		Records: records,
		Total:   total,
		Limit:   limit,
		Offset:  q.Offset,
	}, nil
}

// GetDistinctAgents returns all distinct agent_id values in the sessions table.
func (s *Store) GetDistinctAgents() ([]string, error) {
	var agents []string
	err := s.db.Select(&agents, "SELECT DISTINCT agent_id FROM sessions ORDER BY agent_id")
	if err != nil {
		return nil, err
	}
	return agents, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

package sessiondb

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

func historyStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "sessions.db"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func point(bucket time.Time, samples int, usoc float64) TelemetryPoint {
	return TelemetryPoint{
		AgentID:         "agent-1",
		BatteryName:     "BAT-001",
		Bucket:          bucket.UTC().Format(time.RFC3339),
		USOC:            usoc,
		ConsumptionAvgW: 500,
		PacAvgW:         -2000,
		Samples:         samples,
	}
}

func TestUpsertTelemetryPointsIsIdempotent(t *testing.T) {
	store := historyStore(t)
	bucket := time.Date(2026, 8, 12, 20, 30, 0, 0, time.UTC)

	// The recorder writes the in-progress minute repeatedly as it fills, so the same
	// bucket must update in place rather than accumulate duplicate rows.
	if err := store.UpsertTelemetryPoints([]TelemetryPoint{point(bucket, 2, 90)}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := store.UpsertTelemetryPoints([]TelemetryPoint{point(bucket, 6, 88)}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	points, err := store.QueryTelemetryHistory(HistoryQuery{Since: bucket.Add(-time.Hour)})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected 1 row after re-upserting the same bucket, got %d", len(points))
	}
	if points[0].Samples != 6 || points[0].USOC != 88 {
		t.Fatalf("expected the later aggregate to win, got samples=%d usoc=%v", points[0].Samples, points[0].USOC)
	}
}

func TestQueryTelemetryHistoryWindow(t *testing.T) {
	store := historyStore(t)
	base := time.Date(2026, 8, 12, 20, 0, 0, 0, time.UTC)

	var batch []TelemetryPoint
	for i := 0; i < 10; i++ {
		batch = append(batch, point(base.Add(time.Duration(i)*time.Minute), 6, float64(100-i)))
	}
	if err := store.UpsertTelemetryPoints(batch); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	points, err := store.QueryTelemetryHistory(HistoryQuery{
		Since: base.Add(3 * time.Minute),
		Until: base.Add(6 * time.Minute),
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(points) != 3 {
		t.Fatalf("expected 3 buckets in [+3m, +6m), got %d", len(points))
	}
	if points[0].Bucket != base.Add(3*time.Minute).Format(time.RFC3339) {
		t.Fatalf("expected oldest first, got %s", points[0].Bucket)
	}

	filtered, err := store.QueryTelemetryHistory(HistoryQuery{Since: base, BatteryName: "other"})
	if err != nil {
		t.Fatalf("query filtered: %v", err)
	}
	if len(filtered) != 0 {
		t.Fatalf("expected no rows for an unknown battery, got %d", len(filtered))
	}
}

func TestTelemetryCoverageReportsIncompleteHours(t *testing.T) {
	store := historyStore(t)
	full := time.Date(2026, 8, 12, 20, 0, 0, 0, time.UTC)
	partial := time.Date(2026, 8, 12, 21, 0, 0, 0, time.UTC)

	var batch []TelemetryPoint
	for i := 0; i < 60; i++ {
		batch = append(batch, point(full.Add(time.Duration(i)*time.Minute), 6, 90))
	}
	// An hour where the uplink went quiet after 10 minutes.
	for i := 0; i < 10; i++ {
		batch = append(batch, point(partial.Add(time.Duration(i)*time.Minute), 6, 80))
	}
	if err := store.UpsertTelemetryPoints(batch); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	hours, err := store.TelemetryCoverage("agent-1", full.Add(-time.Hour), time.Time{})
	if err != nil {
		t.Fatalf("coverage: %v", err)
	}
	if len(hours) != 2 {
		t.Fatalf("expected 2 hours, got %d", len(hours))
	}
	if hours[0].Buckets != 60 || hours[0].Samples != 360 {
		t.Fatalf("expected a complete first hour, got buckets=%d samples=%d", hours[0].Buckets, hours[0].Samples)
	}
	if hours[1].Buckets != 10 || hours[1].Samples != 60 {
		t.Fatalf("expected the gap hour to report 10/60, got buckets=%d samples=%d", hours[1].Buckets, hours[1].Samples)
	}
}

func TestCleanupHistory(t *testing.T) {
	store := historyStore(t)
	now := time.Now().UTC().Truncate(time.Minute)

	batch := []TelemetryPoint{
		point(now.Add(-100*24*time.Hour), 6, 50),
		point(now.Add(-time.Hour), 6, 60),
	}
	if err := store.UpsertTelemetryPoints(batch); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	deleted, err := store.CleanupHistory(90 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 expired bucket deleted, got %d", deleted)
	}

	remaining, err := store.QueryTelemetryHistory(HistoryQuery{Since: now.Add(-365 * 24 * time.Hour)})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected the recent bucket to survive, got %d rows", len(remaining))
	}
}

func TestMigrateHistoryAddsPowerColumnsToOldTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")

	db, err := sqlx.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE telemetry_history (
			agent_id TEXT NOT NULL, battery_name TEXT NOT NULL, bucket TEXT NOT NULL,
			usoc REAL NOT NULL DEFAULT 0, rsoc REAL NOT NULL DEFAULT 0,
			remaining_capacity_wh REAL NOT NULL DEFAULT 0,
			consumption_avg_w REAL NOT NULL DEFAULT 0, consumption_max_w REAL NOT NULL DEFAULT 0,
			pac_avg_w REAL NOT NULL DEFAULT 0, pac_min_w REAL NOT NULL DEFAULT 0, pac_max_w REAL NOT NULL DEFAULT 0,
			operating_mode TEXT NOT NULL DEFAULT '', samples INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (agent_id, battery_name, bucket)
		);
		INSERT INTO telemetry_history (agent_id, battery_name, bucket, usoc) VALUES ('a', 'b', '2026-11-03T10:00:00Z', 50);
	`)
	if err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if err := migrateHistory(db); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}

	var point TelemetryPoint
	err = db.Get(&point, `SELECT usoc, production_avg_w, grid_avg_w, grid_min_w, grid_max_w FROM telemetry_history`)
	if err != nil {
		t.Fatal(err)
	}
	if point.USOC != 50 || point.GridMinW != 0 {
		t.Errorf("got %+v", point)
	}
}

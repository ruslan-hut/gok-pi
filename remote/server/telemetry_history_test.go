package server

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"gok-pi/remote/server/sessiondb"
)

func recorderWithStore(t *testing.T) (*telemetryRecorder, *sessiondb.Store) {
	t.Helper()
	store, err := sessiondb.Open(filepath.Join(t.TempDir(), "sessions.db"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return newTelemetryRecorder(testLogger(), store), store
}

func frame(name string, recordedAt time.Time, usoc, consumption, pac float64) TelemetrySnapshot {
	return TelemetrySnapshot{
		Name:             name,
		USOC:             usoc,
		RSOC:             usoc - 5,
		ConsumptionW:     consumption,
		PacTotalW:        pac,
		OperatingMode:    "2",
		OperatingModeSet: true,
		UpdatedAt:        recordedAt,
	}
}

func TestRecorderAggregatesFramesIntoMinuteBuckets(t *testing.T) {
	rec, store := recorderWithStore(t)
	base := time.Date(2026, 8, 12, 20, 30, 0, 0, time.UTC)

	// Six frames in one minute, as a 10s poll interval produces.
	rec.Observe("agent-1", frame("BAT-001", base, 90, 400, -1000), base)
	rec.Observe("agent-1", frame("BAT-001", base.Add(10*time.Second), 89, 600, -3000), base.Add(10*time.Second))
	rec.Observe("agent-1", frame("BAT-001", base.Add(20*time.Second), 88, 500, -2000), base.Add(20*time.Second))

	rec.Flush(base.Add(30 * time.Second))

	points, err := store.QueryTelemetryHistory(sessiondb.HistoryQuery{Since: base.Add(-time.Hour)})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected one minute bucket, got %d", len(points))
	}

	p := points[0]
	if p.Samples != 3 {
		t.Fatalf("expected 3 samples, got %d", p.Samples)
	}
	// Levels are last-write-wins, power is averaged with extremes kept.
	if p.USOC != 88 {
		t.Fatalf("expected the last SoC of the minute, got %v", p.USOC)
	}
	if p.ConsumptionAvgW != 500 || p.ConsumptionMaxW != 600 {
		t.Fatalf("expected load avg=500 max=600, got avg=%v max=%v", p.ConsumptionAvgW, p.ConsumptionMaxW)
	}
	if p.PacAvgW != -2000 || p.PacMinW != -3000 || p.PacMaxW != -1000 {
		t.Fatalf("expected pac avg=-2000 min=-3000 max=-1000, got avg=%v min=%v max=%v", p.PacAvgW, p.PacMinW, p.PacMaxW)
	}
	if p.OperatingMode != "2" {
		t.Fatalf("expected operating mode 2, got %q", p.OperatingMode)
	}
}

func TestRecorderClosesBucketOnMinuteRollover(t *testing.T) {
	rec, store := recorderWithStore(t)
	base := time.Date(2026, 8, 12, 20, 30, 0, 0, time.UTC)
	next := base.Add(time.Minute)

	rec.Observe("agent-1", frame("BAT-001", base, 90, 400, -1000), base)
	rec.Observe("agent-1", frame("BAT-001", next, 80, 400, -1000), next)
	rec.Flush(next.Add(10 * time.Second))

	points, err := store.QueryTelemetryHistory(sessiondb.HistoryQuery{Since: base.Add(-time.Hour)})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("expected two buckets across the rollover, got %d", len(points))
	}
	if points[0].USOC != 90 || points[1].USOC != 80 {
		t.Fatalf("expected buckets to keep their own values, got %v and %v", points[0].USOC, points[1].USOC)
	}
	if points[0].Samples != 1 || points[1].Samples != 1 {
		t.Fatalf("expected one sample per bucket, got %d and %d", points[0].Samples, points[1].Samples)
	}
}

// Telemetry replayed from an agent's offline spool must land in the minute it was
// recorded, not the minute it arrived, or the history would show a flat line during
// the outage and a spike on reconnect.
func TestRecorderBucketsReplayedTelemetryByItsOwnTimestamp(t *testing.T) {
	rec, store := recorderWithStore(t)
	recordedAt := time.Date(2026, 8, 12, 14, 0, 0, 0, time.UTC)
	arrivedAt := recordedAt.Add(6 * time.Hour)

	rec.Observe("agent-1", frame("BAT-001", recordedAt, 70, 400, -1000), arrivedAt)
	rec.Flush(arrivedAt)

	points, err := store.QueryTelemetryHistory(sessiondb.HistoryQuery{Since: recordedAt.Add(-time.Hour)})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected one bucket, got %d", len(points))
	}
	if points[0].Bucket != recordedAt.Format(time.RFC3339) {
		t.Fatalf("expected the bucket to use the recorded time %s, got %s", recordedAt.Format(time.RFC3339), points[0].Bucket)
	}
}

func TestRecorderForgetFlushesPendingBucket(t *testing.T) {
	rec, store := recorderWithStore(t)
	base := time.Date(2026, 8, 12, 20, 30, 0, 0, time.UTC)

	rec.Observe("agent-1", frame("BAT-001", base, 90, 400, -1000), base)
	rec.Forget("agent-1")

	points, err := store.QueryTelemetryHistory(sessiondb.HistoryQuery{Since: base.Add(-time.Hour)})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected the pending minute to be written on disconnect, got %d rows", len(points))
	}
}

func TestRecorderKeepsStreamsSeparate(t *testing.T) {
	rec, store := recorderWithStore(t)
	base := time.Date(2026, 8, 12, 20, 30, 0, 0, time.UTC)

	rec.Observe("agent-1", frame("BAT-001", base, 90, 400, -1000), base)
	rec.Observe("agent-1", frame("BAT-002", base, 40, 900, 2000), base)
	rec.Observe("agent-2", frame("BAT-001", base, 60, 100, 0), base)
	rec.Flush(base.Add(30 * time.Second))

	points, err := store.QueryTelemetryHistory(sessiondb.HistoryQuery{Since: base.Add(-time.Hour)})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(points) != 3 {
		t.Fatalf("expected one bucket per agent/battery stream, got %d", len(points))
	}

	agent1 := 0
	for _, p := range points {
		if p.AgentID == "agent-1" {
			agent1++
		}
	}
	if agent1 != 2 {
		t.Fatalf("expected agent-1 to have both of its batteries recorded, got %d", agent1)
	}
}

// A nil recorder is the no-database configuration; every entry point must tolerate it.
func TestNilRecorderIsInert(t *testing.T) {
	var rec *telemetryRecorder
	now := time.Now()

	rec.Observe("agent-1", frame("BAT-001", now, 90, 400, -1000), now)
	rec.Flush(now)
	rec.Forget("agent-1")
}

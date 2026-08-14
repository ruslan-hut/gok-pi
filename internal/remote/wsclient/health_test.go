package wsclient

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"gok-pi/internal/config"
	"gok-pi/internal/remote/spool"
	"gok-pi/metrics/observers"
)

func newTestClient(t *testing.T) *Client {
	t.Helper()
	return New(config.RemoteControl{}, AgentMetadata{ID: "agent-1", Env: "test"}, newTestLogger())
}

func newSpooledClient(t *testing.T) (*Client, *spool.Spool) {
	t.Helper()
	sp, err := spool.Open(filepath.Join(t.TempDir(), "spool.db"), newTestLogger())
	if err != nil {
		t.Fatalf("open spool: %v", err)
	}
	t.Cleanup(func() { _ = sp.Close() })

	client := newTestClient(t)
	client.UseSpool(sp)
	return client, sp
}

// The watchdog exists because a stuck write path produces no error of its own.
// Below the threshold it must stay out of the way; above it, it must fail the
// connection so run() redials.
func TestCheckBacklogFailsConnectionOnlyWhenStale(t *testing.T) {
	client, sp := newSpooledClient(t)

	fresh := observers.Snapshot{Name: "BAT-001", UpdatedAt: time.Now().Add(-time.Minute)}
	if err := sp.Append(fresh); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := client.checkBacklog(); err != nil {
		t.Fatalf("expected a fresh backlog to be tolerated, got %v", err)
	}

	stale := observers.Snapshot{Name: "BAT-001", UpdatedAt: time.Now().Add(-backlogStallAfter - time.Minute)}
	if err := sp.Append(stale); err != nil {
		t.Fatalf("append: %v", err)
	}
	err := client.checkBacklog()
	if err == nil {
		t.Fatal("expected a backlog older than the threshold to fail the connection")
	}

	if got := client.health.snapshot().ForcedRedials; got != 1 {
		t.Fatalf("expected the forced redial to be counted once, got %d", got)
	}
}

func TestCheckBacklogIgnoresDeliveredRows(t *testing.T) {
	client, sp := newSpooledClient(t)

	stale := observers.Snapshot{Name: "BAT-001", UpdatedAt: time.Now().Add(-24 * time.Hour)}
	if err := sp.Append(stale); err != nil {
		t.Fatalf("append: %v", err)
	}
	entries, err := sp.Undelivered(10)
	if err != nil {
		t.Fatalf("undelivered: %v", err)
	}
	if err := sp.MarkDelivered(entries[len(entries)-1].ID); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}

	// Old rows that were delivered are just history awaiting pruning, not a stall.
	if err := client.checkBacklog(); err != nil {
		t.Fatalf("expected delivered rows to be ignored, got %v", err)
	}
}

func TestCheckBacklogIsInertWithoutSpool(t *testing.T) {
	client := newTestClient(t)
	if err := client.checkBacklog(); err != nil {
		t.Fatalf("expected no watchdog without a spool, got %v", err)
	}
}

func TestSpoolHealthReportsBacklogAge(t *testing.T) {
	client, sp := newSpooledClient(t)

	recorded := time.Now().Add(-10 * time.Minute)
	if err := sp.Append(observers.Snapshot{Name: "BAT-001", UpdatedAt: recorded}); err != nil {
		t.Fatalf("append: %v", err)
	}

	health := client.spoolHealth()
	if health.Pending != 1 {
		t.Fatalf("expected 1 pending, got %d", health.Pending)
	}
	if health.OldestUndeliveredAgeSec < 590 || health.OldestUndeliveredAgeSec > 610 {
		t.Fatalf("expected a ~600s backlog age, got %d", health.OldestUndeliveredAgeSec)
	}
}

// A heartbeat still has to report the counters when the spool cannot be read —
// that failure is itself a symptom, not a reason to report nothing.
func TestSpoolHealthWithoutSpool(t *testing.T) {
	client := newTestClient(t)
	client.health.recordAppend(nil)
	client.health.recordDelivered(3)

	health := client.spoolHealth()
	if health.Appended != 1 || health.Delivered != 3 {
		t.Fatalf("expected counters to survive a missing spool, got appended=%d delivered=%d", health.Appended, health.Delivered)
	}
	if health.Pending != 0 || health.OldestUndeliveredAgeSec != 0 {
		t.Fatal("expected no backlog figures without a spool")
	}
}

func TestHealthCountersRecordFailures(t *testing.T) {
	h := newUplinkHealth()

	h.recordAppend(nil)
	h.recordAppend(errors.New("disk full"))
	h.recordDelivered(5)
	h.recordFlushError(errors.New("write failed"))
	h.recordMarkError(errors.New("mark failed"))
	h.recordReadError(errors.New("read failed"))
	h.recordPrune(360)
	h.recordReconnect()

	snap := h.snapshot()
	if snap.Appended != 1 || snap.AppendErrors != 1 {
		t.Fatalf("expected 1 append and 1 append error, got %d/%d", snap.Appended, snap.AppendErrors)
	}
	if snap.Delivered != 5 || snap.FlushErrors != 1 || snap.MarkErrors != 1 || snap.ReadErrors != 1 {
		t.Fatalf("unexpected counters: %+v", snap)
	}
	if snap.Reconnects != 1 || snap.LastPruneCount != 360 {
		t.Fatalf("expected reconnect and prune to be recorded, got %+v", snap)
	}
	// The most recent failure is kept verbatim: "which error" is the first
	// question asked of a stalled uplink.
	if snap.LastError != "read failed" {
		t.Fatalf("expected the latest error to be retained, got %q", snap.LastError)
	}
	if snap.LastErrorAt == nil || snap.LastAppendAt == nil || snap.LastDeliveryAt == nil {
		t.Fatal("expected timestamps for the recorded events")
	}
}

func TestDiagnosticsReportsSpoolAndBatteries(t *testing.T) {
	client, sp := newSpooledClient(t)

	if err := sp.Append(observers.Snapshot{Name: "BAT-001", UpdatedAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatalf("append: %v", err)
	}
	observers.UpdateBattery("BAT-DIAG", observers.Reading{RSOC: 55, USOC: 60, Status: "Connected", OperatingMode: "2"})

	diag := client.diagnostics()

	if diag.Agent.ID != "agent-1" {
		t.Fatalf("expected the agent identity in diagnostics, got %q", diag.Agent.ID)
	}
	if diag.Spool == nil || diag.Spool.Pending != 1 {
		t.Fatalf("expected the spool backlog to be reported, got %+v", diag.Spool)
	}
	if diag.SpoolError != "" {
		t.Fatalf("expected no spool error, got %q", diag.SpoolError)
	}
	if diag.Goroutines <= 0 || diag.UptimeSec < 0 {
		t.Fatalf("expected runtime figures, got goroutines=%d uptime=%d", diag.Goroutines, diag.UptimeSec)
	}

	// Battery state comes from the observers, so asking for diagnostics never
	// polls the battery API.
	found := false
	for _, b := range diag.Batteries {
		if b.Name == "BAT-DIAG" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the observed battery to appear in diagnostics")
	}
}

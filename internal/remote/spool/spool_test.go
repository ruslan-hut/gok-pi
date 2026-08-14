package spool

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"gok-pi/metrics/observers"
)

func newTestSpool(t *testing.T) *Spool {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sp, err := Open(filepath.Join(t.TempDir(), "spool.db"), log)
	if err != nil {
		t.Fatalf("open spool: %v", err)
	}
	t.Cleanup(func() { _ = sp.Close() })
	return sp
}

func snap(name string, ts time.Time) observers.Snapshot {
	return observers.Snapshot{Name: name, UpdatedAt: ts}
}

func TestSpoolReplayAndAck(t *testing.T) {
	sp := newTestSpool(t)
	base := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		if err := sp.Append(snap("001", base.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	entries, err := sp.Undelivered(0)
	if err != nil {
		t.Fatalf("undelivered: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("expected 5 undelivered, got %d", len(entries))
	}
	// Ordered oldest-first by id, preserving the original timestamps.
	if !entries[0].Snapshot.UpdatedAt.Equal(base) {
		t.Fatalf("expected first entry at %v, got %v", base, entries[0].Snapshot.UpdatedAt)
	}

	// Ack the first three; only the last two should remain.
	if err := sp.MarkDelivered(entries[2].ID); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
	remaining, err := sp.Undelivered(0)
	if err != nil {
		t.Fatalf("undelivered after ack: %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("expected 2 remaining, got %d", len(remaining))
	}
	if remaining[0].ID != entries[3].ID {
		t.Fatalf("expected remaining to start at id %d, got %d", entries[3].ID, remaining[0].ID)
	}
}

func TestSpoolSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spool.db")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	sp, err := Open(path, log)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := sp.Append(snap("001", time.Now().UTC())); err != nil {
		t.Fatalf("append: %v", err)
	}
	_ = sp.Close()

	reopened, err := Open(path, log)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	entries, err := reopened.Undelivered(0)
	if err != nil {
		t.Fatalf("undelivered: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after reopen, got %d", len(entries))
	}
}

func TestSpoolPrune(t *testing.T) {
	sp := newTestSpool(t)
	now := time.Now().UTC()

	// Old delivered row → pruned by deliveredRetention.
	if err := sp.Append(snap("001", now.Add(-48*time.Hour))); err != nil {
		t.Fatalf("append old: %v", err)
	}
	entries, _ := sp.Undelivered(0)
	if err := sp.MarkDelivered(entries[0].ID); err != nil {
		t.Fatalf("ack: %v", err)
	}
	// Recent undelivered row → kept.
	if err := sp.Append(snap("001", now)); err != nil {
		t.Fatalf("append recent: %v", err)
	}

	deleted, err := sp.Prune(24*time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 pruned, got %d", deleted)
	}
	pending, _ := sp.PendingCount()
	if pending != 1 {
		t.Fatalf("expected 1 pending after prune, got %d", pending)
	}
}

func TestSpoolStatsReportsBacklogAge(t *testing.T) {
	sp := newTestSpool(t)
	base := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		if err := sp.Append(snap("BAT-001", base.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	stats, err := sp.Stats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Total != 5 || stats.Pending != 5 {
		t.Fatalf("expected 5 total and 5 pending, got total=%d pending=%d", stats.Total, stats.Pending)
	}
	if stats.OldestUndelivered == nil || !stats.OldestUndelivered.Equal(base) {
		t.Fatalf("expected the oldest undelivered row to be %s, got %v", base, stats.OldestUndelivered)
	}
	if stats.NewestRecorded == nil || !stats.NewestRecorded.Equal(base.Add(4*time.Minute)) {
		t.Fatalf("expected the newest row to be %s, got %v", base.Add(4*time.Minute), stats.NewestRecorded)
	}

	// Delivering the head of the queue must move the backlog's age forward: this
	// is the number the watchdog and the heartbeat both read.
	entries, err := sp.Undelivered(3)
	if err != nil {
		t.Fatalf("undelivered: %v", err)
	}
	if err := sp.MarkDelivered(entries[len(entries)-1].ID); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}

	stats, err = sp.Stats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Pending != 2 {
		t.Fatalf("expected 2 pending after delivering 3, got %d", stats.Pending)
	}
	if stats.OldestUndelivered == nil || !stats.OldestUndelivered.Equal(base.Add(3*time.Minute)) {
		t.Fatalf("expected the backlog to start at %s, got %v", base.Add(3*time.Minute), stats.OldestUndelivered)
	}
}

func TestSpoolStatsOnEmptyBuffer(t *testing.T) {
	sp := newTestSpool(t)

	stats, err := sp.Stats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Total != 0 || stats.Pending != 0 {
		t.Fatalf("expected an empty spool, got total=%d pending=%d", stats.Total, stats.Pending)
	}
	if stats.OldestUndelivered != nil || stats.NewestRecorded != nil {
		t.Fatal("expected no timestamps for an empty spool")
	}
}

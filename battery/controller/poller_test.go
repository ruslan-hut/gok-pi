package controller

import (
	"gok-pi/battery/entity"
	"gok-pi/metrics/observers"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// statusClient serves a canned status reading to the poller.
type statusClient struct {
	fakeClient
	status *entity.SystemStatus
	err    error
}

func (s *statusClient) Status() (*entity.SystemStatus, error) { return s.status, s.err }

// TestPollEmitsSingleSnapshot pins the telemetry amplification fix from the
// 2026-07-23..08-01 logs, where the spool pruned ~3240 rows/hour: nine per 10s
// poll, because the poller called one notifying observer helper per field. A poll
// must produce exactly one snapshot, or every row is written and uplinked 9x.
func TestPollEmitsSingleSnapshot(t *testing.T) {
	var mu sync.Mutex
	var got []observers.Snapshot

	cancel := observers.RegisterListener(func(s observers.Snapshot) {
		mu.Lock()
		defer mu.Unlock()
		if s.Name == "BAT-POLL" {
			got = append(got, s)
		}
	})
	defer cancel()

	client := &statusClient{status: &entity.SystemStatus{
		RSOC:                55,
		USOC:                54,
		RemainingCapacityWh: 8700,
		ConsumptionW:        231,
		PacTotalW:           -120,
		BatteryDischarging:  true,
		OperatingMode:       "1",
	}}

	p := NewStatusPoller("BAT-POLL", client, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.poll()

	mu.Lock()
	defer mu.Unlock()

	if len(got) != 1 {
		t.Fatalf("expected 1 snapshot per poll, got %d", len(got))
	}

	// The single snapshot must still carry the whole reading.
	s := got[0]
	if s.RSOC != 55 || s.USOC != 54 || s.RemainingCapacityWh != 8700 ||
		s.ConsumptionW != 231 || s.PacTotalW != -120 {
		t.Fatalf("snapshot lost numeric fields: %+v", s)
	}
	if !s.BatteryDischarging || !s.BatteryDischargingSet {
		t.Fatalf("snapshot lost discharge state: %+v", s)
	}
	if s.BatteryCharging || !s.BatteryChargingSet {
		t.Fatalf("snapshot lost charge state: %+v", s)
	}
	if s.OperatingMode != "1" || !s.OperatingModeSet {
		t.Fatalf("snapshot lost operating mode: %+v", s)
	}
	if s.Status != "Connected" {
		t.Fatalf("expected the reading to mark the battery Connected, got %q", s.Status)
	}
}

// TestPollFailureEmitsDisconnected checks the failure path still publishes exactly
// one snapshot and does not report stale connectivity.
func TestPollFailureEmitsDisconnected(t *testing.T) {
	var mu sync.Mutex
	var got []observers.Snapshot

	cancel := observers.RegisterListener(func(s observers.Snapshot) {
		mu.Lock()
		defer mu.Unlock()
		if s.Name == "BAT-POLL-FAIL" {
			got = append(got, s)
		}
	})
	defer cancel()

	client := &statusClient{err: io.ErrUnexpectedEOF}
	p := NewStatusPoller("BAT-POLL-FAIL", client, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.poll()

	mu.Lock()
	defer mu.Unlock()

	if len(got) != 1 {
		t.Fatalf("expected 1 snapshot per failed poll, got %d", len(got))
	}
	if got[0].Status != "Disconnected" {
		t.Fatalf("expected Disconnected, got %q", got[0].Status)
	}
}

// TestPollDiscardsImplausibleReading pins the 2026-08-16 incident: the battery
// answered one poll with USOC zeroed and the discharge flag cleared while it was
// sitting at 85%, the discharge controller read it as "below the SoC limit" and
// stopped a discharge its window was still running. Such a frame must reach neither
// the observers nor the subscribers.
func TestPollDiscardsImplausibleReading(t *testing.T) {
	var mu sync.Mutex
	var got []observers.Snapshot

	cancel := observers.RegisterListener(func(s observers.Snapshot) {
		mu.Lock()
		defer mu.Unlock()
		if s.Name == "BAT-POLL-BAD" {
			got = append(got, s)
		}
	})
	defer cancel()

	client := &statusClient{status: &entity.SystemStatus{
		USOC: 85, RSOC: 85, RemainingCapacityWh: 12705,
		BatteryDischarging: true, OperatingMode: "1",
	}}
	p := NewStatusPoller("BAT-POLL-BAD", client, slog.New(slog.NewTextHandler(io.Discard, nil)))
	feed := p.Subscribe()

	p.poll()

	client.status = &entity.SystemStatus{USOC: 0, RSOC: 0, OperatingMode: "1"}
	p.poll()

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected the bad reading to emit no snapshot, got %d", len(got))
	}

	select {
	case status := <-feed:
		if status == nil || status.USOC != 85 {
			t.Fatalf("expected the last good reading on the feed, got %+v", status)
		}
	default:
		t.Fatal("expected the last good reading to still be on the feed")
	}
}

// TestPollAcceptsPersistentStep checks the discard cannot become permanent: a step
// that keeps being reported is the battery's new state, not a bad frame.
func TestPollAcceptsPersistentStep(t *testing.T) {
	client := &statusClient{status: &entity.SystemStatus{USOC: 85, RSOC: 85, OperatingMode: "1"}}
	p := NewStatusPoller("BAT-POLL-STEP", client, slog.New(slog.NewTextHandler(io.Discard, nil)))
	feed := p.Subscribe()

	p.poll()
	<-feed

	client.status = &entity.SystemStatus{USOC: 10, RSOC: 10, OperatingMode: "1"}
	for i := 0; i <= maxDiscardedReadings; i++ {
		p.poll()
	}

	select {
	case status := <-feed:
		if status == nil || status.USOC != 10 {
			t.Fatalf("expected the step to be accepted after %d discards, got %+v", maxDiscardedReadings, status)
		}
	default:
		t.Fatalf("expected the step to be accepted after %d discards", maxDiscardedReadings)
	}
}

// TestPollAcceptsStepAfterGap checks a reading is not measured against a reference
// old enough to have stopped meaning anything: after an outage the battery may
// legitimately be somewhere else entirely.
func TestPollAcceptsStepAfterGap(t *testing.T) {
	p := NewStatusPoller("BAT-POLL-GAP", &statusClient{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.lastUSOC = 85
	p.lastUSOCAt = time.Now().Add(-socReferenceMaxAge - time.Second)

	if p.implausible(&entity.SystemStatus{USOC: 10}, time.Now()) {
		t.Fatal("expected a reading after a long gap to be accepted")
	}
}

package controller

import (
	"gok-pi/battery/entity"
	"gok-pi/metrics/observers"
	"io"
	"log/slog"
	"sync"
	"testing"
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

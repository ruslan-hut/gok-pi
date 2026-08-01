package controller

import (
	"gok-pi/battery/entity"
	"io"
	"log/slog"
	"testing"
)

// TestPollerPublishesNilAfterSustainedFailure covers the poller half of the stale
// reading fix: a blip must not disturb consumers, but an outage must tell them the
// last reading can no longer be refreshed - once, not on every failed poll.
func TestPollerPublishesNilAfterSustainedFailure(t *testing.T) {
	client := &statusClient{status: &entity.SystemStatus{RSOC: 55, USOC: 55, OperatingMode: "1"}}
	p := NewStatusPoller("BAT-OUTAGE", client, slog.New(slog.NewTextHandler(io.Discard, nil)))
	feed := p.Subscribe()

	p.poll()
	if got := <-feed; got == nil {
		t.Fatal("setup: expected a successful reading on the feed")
	}

	client.err = io.ErrUnexpectedEOF
	for i := 1; i < StaleAfterFailures; i++ {
		p.poll()
		select {
		case got := <-feed:
			t.Fatalf("failure %d should not publish yet, got %+v", i, got)
		default:
		}
	}

	p.poll() // the StaleAfterFailures-th failure
	select {
	case got := <-feed:
		if got != nil {
			t.Fatalf("expected a nil reading marking the status stale, got %+v", got)
		}
	default:
		t.Fatal("expected the poller to publish a nil reading after a sustained outage")
	}

	// Further failed polls must not keep re-publishing.
	p.poll()
	p.poll()
	select {
	case got := <-feed:
		t.Fatalf("expected no further publishes during the outage, got %+v", got)
	default:
	}
}

// TestInvalidateStatusStopsDecidingFromStaleData covers the controller half: after
// the battery has been unreachable, the cached reading must be dropped so no start
// or stop is issued from data that is minutes old, and the next fresh reading must
// re-sync internal state.
func TestInvalidateStatusStopsDecidingFromStaleData(t *testing.T) {
	client := &fakeClient{}
	c := newTestController(t, client)
	c.socLimit = 53
	c.schedules = []entity.Schedule{scheduleAllDay(53)}

	setSoC(c, 55)
	c.evaluate()
	if !c.active {
		t.Fatal("setup: controller should be active")
	}
	starts, stops := client.starts, client.stops

	c.invalidateStatus()

	if c.status != nil {
		t.Fatal("stale status should be discarded")
	}
	if c.ready {
		t.Fatal("controller should not be ready without a reading")
	}
	if !c.firstStatusPoll {
		t.Fatal("the next reading must be treated as a re-sync")
	}

	// evaluate() must not command an unreachable battery.
	c.evaluate()
	if client.starts != starts || client.stops != stops {
		t.Fatalf("no operation may be issued without a reading: starts %d->%d, stops %d->%d",
			starts, client.starts, stops, client.stops)
	}
}

// TestResyncDoesNotPromoteOwnOperationToManualOverride guards the hazard in
// re-running the startup sync after an outage: a schedule-driven discharge whose
// window closed while the battery was unreachable must be stoppable, not adopted
// as a manual override that would run until the next restart.
func TestResyncDoesNotPromoteOwnOperationToManualOverride(t *testing.T) {
	client := &fakeClient{}
	c := newTestController(t, client)
	c.socLimit = 53
	c.schedules = []entity.Schedule{scheduleAllDay(53)}

	setSoC(c, 55)
	c.evaluate()
	if !c.active {
		t.Fatal("setup: controller should be active")
	}

	c.invalidateStatus()

	// The battery comes back still discharging in manual mode, but the schedule
	// that justified it has ended in the meantime.
	c.schedules[0].Enabled = false
	recovered := &entity.SystemStatus{OperatingMode: "1", USOC: 54, RSOC: 54, BatteryDischarging: true}
	c.observeStatus(recovered)
	c.syncStateFromBattery(recovered)
	c.firstStatusPoll = false

	if c.manualOverride {
		t.Fatal("a schedule-driven operation must not be promoted to a manual override on re-sync")
	}

	c.evaluate()
	if client.stops != 1 {
		t.Fatalf("expected the orphaned discharge to be stopped after re-sync, got %d stops", client.stops)
	}
	if c.active {
		t.Fatal("controller should be inactive after the stop")
	}
}

// TestStartupSyncStillAdoptsForeignOperation checks the re-sync guard did not break
// the startup case it exists for: a fresh process finding the battery discharging in
// manual mode with no schedule must preserve it.
func TestStartupSyncStillAdoptsForeignOperation(t *testing.T) {
	client := &fakeClient{}
	c := newTestController(t, client)

	status := &entity.SystemStatus{OperatingMode: "1", USOC: 60, RSOC: 60, BatteryDischarging: true}
	c.observeStatus(status)
	c.syncStateFromBattery(status)

	if !c.active {
		t.Fatal("startup sync should adopt the running discharge")
	}
	if !c.manualOverride {
		t.Fatal("startup sync should preserve an unscheduled manual discharge")
	}
}

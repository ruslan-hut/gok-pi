package controller

import (
	"gok-pi/battery/entity"
	"io"
	"log/slog"
	"testing"
	"time"
)

// middayZone returns a fixed zone in which the current instant reads as ~12:00,
// so schedule windows built by the tests never wrap around midnight.
func middayZone() *time.Location {
	utc := time.Now().UTC()
	secs := utc.Hour()*3600 + utc.Minute()*60 + utc.Second()
	return time.FixedZone("TEST", 12*3600-secs)
}

// newPruneController builds a controller for the given direction that records
// which schedules it asks the config layer to remove.
func newPruneController(t *testing.T, dir Direction, removed *[]string) *Controller {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c, err := New("BAT-TEST", &fakeClient{}, dir, log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.timezone = middayZone()
	c.removeSchedule = func(name string) error {
		*removed = append(*removed, name)
		return nil
	}
	return c
}

// expiredSchedules returns one expired auto-schedule per direction, both already
// past in the controller's timezone. Both controllers for a battery hold the full
// list, which is what makes ownership matter.
func expiredSchedules() []entity.Schedule {
	return []entity.Schedule{
		{Name: "auto-1-discharge-BAT-TEST", Type: "discharge", Enabled: true, StartTime: "09:00", StopTime: "10:00", PowerLimit: 2000, SocLimit: 53},
		{Name: "auto-1-charge-BAT-TEST", Type: "charge", Enabled: true, StartTime: "09:00", StopTime: "10:00", PowerLimit: 2000, SocLimit: 90},
	}
}

// TestPruneOnlyRemovesOwnDirection reproduces the production log lines from
// 2026-07-24 and 2026-07-27, where "mod=battery.charge" reported removing
// auto-...-discharge-BAT-001 and vice versa: the prune loop ignored the direction
// filter, so each controller deleted the other's schedules from the shared config
// on its own clock. A controller must only prune what it actually drives.
func TestPruneOnlyRemovesOwnDirection(t *testing.T) {
	client := &fakeClient{}

	var dischargeRemoved []string
	dc := newPruneController(t, DischargeDirection(client), &dischargeRemoved)
	dc.schedules = expiredSchedules()
	dc.removeExpiredAutoSchedules()

	if len(dischargeRemoved) != 1 || dischargeRemoved[0] != "auto-1-discharge-BAT-TEST" {
		t.Fatalf("discharge controller should remove only its own schedule, removed %v", dischargeRemoved)
	}
	if len(dc.schedules) != 1 || dc.schedules[0].Name != "auto-1-charge-BAT-TEST" {
		t.Fatalf("charge schedule must stay in the discharge controller's list, got %v", dc.schedules)
	}

	var chargeRemoved []string
	cc := newPruneController(t, ChargeDirection(client), &chargeRemoved)
	cc.schedules = expiredSchedules()
	cc.removeExpiredAutoSchedules()

	if len(chargeRemoved) != 1 || chargeRemoved[0] != "auto-1-charge-BAT-TEST" {
		t.Fatalf("charge controller should remove only its own schedule, removed %v", chargeRemoved)
	}
	if len(cc.schedules) != 1 || cc.schedules[0].Name != "auto-1-discharge-BAT-TEST" {
		t.Fatalf("discharge schedule must stay in the charge controller's list, got %v", cc.schedules)
	}
}

// TestPruneKeepsUnexpiredAndManualSchedules guards the rest of the prune contract:
// only expired auto-schedules of this direction go, never a live one and never a
// manually configured schedule.
func TestPruneKeepsUnexpiredAndManualSchedules(t *testing.T) {
	var removed []string
	c := newPruneController(t, DischargeDirection(&fakeClient{}), &removed)
	c.schedules = []entity.Schedule{
		{Name: "auto-1-discharge-BAT-TEST", Type: "discharge", Enabled: true, StartTime: "14:00", StopTime: "15:00"},
		{Name: "evening-discharge", Type: "discharge", Enabled: true, StartTime: "09:00", StopTime: "10:00"},
	}

	c.removeExpiredAutoSchedules()

	if len(removed) != 0 {
		t.Fatalf("expected nothing removed, got %v", removed)
	}
	if len(c.schedules) != 2 {
		t.Fatalf("expected both schedules kept, got %v", c.schedules)
	}
}

// TestPruneRemovesUntypedAutoSchedule covers the discharge filter's acceptance of
// an empty type: such a schedule is driven by the discharge controller, so it must
// also be pruned by it and not stranded in the config forever.
func TestPruneRemovesUntypedAutoSchedule(t *testing.T) {
	var removed []string
	c := newPruneController(t, DischargeDirection(&fakeClient{}), &removed)
	c.schedules = []entity.Schedule{
		{Name: "auto-1-BAT-TEST", Enabled: true, StartTime: "09:00", StopTime: "10:00"},
	}

	c.removeExpiredAutoSchedules()

	if len(removed) != 1 || removed[0] != "auto-1-BAT-TEST" {
		t.Fatalf("expected the untyped auto-schedule to be pruned by discharge, removed %v", removed)
	}
}

package controller

import (
	"gok-pi/battery/entity"
	"testing"
)

// startCommand builds the command an EV charging session produces: the discharge
// limits travel with the start so the operation cannot land half-applied.
func chargerStart(power, soc int) ControlCommand {
	return ControlCommand{
		Type:   CommandStart,
		Power:  power,
		Source: CommandSourceCharger,
		Limits: &CommandLimits{PowerLimit: &power, SocLimit: &soc},
	}
}

func configUpdate(schedules []entity.Schedule) ControlCommand {
	return ControlCommand{
		Type:   CommandUpdateConfig,
		Config: &ConfigUpdate{Schedules: schedules},
	}
}

// TestChargerOverrideSurvivesConfigUpdate is the reason the charger integration
// works at all: the control server pushes config whenever auto-schedules change,
// which happens every few minutes. If that cancelled the override, a car would
// silently drop back to grid power mid-charge.
func TestChargerOverrideSurvivesConfigUpdate(t *testing.T) {
	client := &fakeClient{}
	c := newTestController(t, client)
	setSoC(c, 90)

	if err := c.processControlCommand(chargerStart(3000, 40)); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !c.active || !c.manualOverride {
		t.Fatalf("setup: expected an active override, active=%v override=%v", c.active, c.manualOverride)
	}

	if err := c.processControlCommand(configUpdate(nil)); err != nil {
		t.Fatalf("config update: %v", err)
	}

	if !c.manualOverride {
		t.Fatal("charger override was cancelled by a config push")
	}
	if c.overrideSource != CommandSourceCharger {
		t.Fatalf("override source = %q, want %q", c.overrideSource, CommandSourceCharger)
	}
	if !c.ready {
		t.Fatal("controller should stay ready while the session runs")
	}
	if client.stops != 0 {
		t.Fatalf("config push stopped the discharge (%d stops)", client.stops)
	}

	// The session's limits must survive too: re-deriving them from the (empty)
	// schedules would reset the battery to its defaults on the next poll.
	if c.rate != 3000 {
		t.Fatalf("rate = %d, want 3000", c.rate)
	}
	if c.socLimit != 40 {
		t.Fatalf("soc limit = %v, want 40", c.socLimit)
	}
}

// TestManualOverrideStillClearedByConfigUpdate guards the pre-existing behaviour:
// an operator's override is meant to be superseded by a new config.
func TestManualOverrideStillClearedByConfigUpdate(t *testing.T) {
	client := &fakeClient{}
	c := newTestController(t, client)
	setSoC(c, 90)

	if err := c.processControlCommand(ControlCommand{Type: CommandStart, Power: 3000}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !c.manualOverride {
		t.Fatal("setup: expected a manual override")
	}

	if err := c.processControlCommand(configUpdate(nil)); err != nil {
		t.Fatalf("config update: %v", err)
	}

	if c.manualOverride {
		t.Fatal("operator override should be cleared by a config push")
	}
}

// TestChargerStartAppliesLimits checks the limits ride along with the start.
func TestChargerStartAppliesLimits(t *testing.T) {
	client := &fakeClient{}
	c := newTestController(t, client)
	setSoC(c, 90)

	if err := c.processControlCommand(chargerStart(2500, 35)); err != nil {
		t.Fatalf("start: %v", err)
	}

	if c.powerLimit != 2500 {
		t.Fatalf("power limit = %d, want 2500", c.powerLimit)
	}
	if c.socLimit != 35 {
		t.Fatalf("soc limit = %v, want 35", c.socLimit)
	}
	if client.starts != 1 {
		t.Fatalf("starts = %d, want 1", client.starts)
	}
}

// TestChargerStartRefusedBelowSocFloor: the SoC floor from the link is honoured,
// so a session cannot discharge the battery past the operator's boundary. The
// car simply falls back to grid power.
func TestChargerStartRefusedBelowSocFloor(t *testing.T) {
	client := &fakeClient{}
	c := newTestController(t, client)
	setSoC(c, 30)

	err := c.processControlCommand(chargerStart(2500, 50))
	if err == nil {
		t.Fatal("expected the start to be refused at the SoC floor")
	}
	if client.starts != 0 {
		t.Fatalf("starts = %d, want 0", client.starts)
	}
}

// TestStopClearsOverrideSource releases the battery back to its schedules when
// the charging session ends.
func TestStopClearsOverrideSource(t *testing.T) {
	client := &fakeClient{}
	c := newTestController(t, client)
	setSoC(c, 90)

	if err := c.processControlCommand(chargerStart(3000, 40)); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := c.processControlCommand(ControlCommand{Type: CommandStop}); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if c.manualOverride || c.overrideSource != "" {
		t.Fatalf("stop left an override: override=%v source=%q", c.manualOverride, c.overrideSource)
	}
	if c.active {
		t.Fatal("controller should be inactive after stop")
	}
	if client.stops != 1 {
		t.Fatalf("stops = %d, want 1", client.stops)
	}
}

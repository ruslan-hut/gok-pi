package controller

import (
	"io"
	"log/slog"
	"testing"

	"gok-pi/battery/entity"
)

func newDirController(t *testing.T, dir func(FullClient) Direction, batterySoc int) (*Controller, *fakeClient) {
	t.Helper()
	client := &fakeClient{}
	c, err := New("BAT-TEST", client, dir(client), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.timezone = middayZone()
	c.SetBatteryDefaults(2000, batterySoc)
	return c, client
}

func allDay(name, typ string, soc int) entity.Schedule {
	return entity.Schedule{Name: name, Type: typ, Enabled: true, StartTime: "00:00", StopTime: "24:00", PowerLimit: 2000, SocLimit: soc}
}

// P-102 (a): a manual charge outside any window targets 100 %, not the battery's
// soc_limit (which is the discharge floor). It starts above that limit and runs
// until full.
func TestManualChargeIgnoresBatterySocLimit(t *testing.T) {
	for _, soc := range []float64{10, 59, 60, 70, 95} {
		c, client := newDirController(t, ChargeDirection, 60)
		setSoC(c, soc)
		if err := c.processControlCommand(ControlCommand{Type: CommandStart, Power: 2000}); err != nil {
			t.Fatalf("soc %v: start refused: %v", soc, err)
		}
		if client.starts != 1 || !c.active {
			t.Fatalf("soc %v: starts=%d active=%v", soc, client.starts, c.active)
		}
		if c.socLimit != 100 {
			t.Fatalf("soc %v: soc limit = %v, want 100", soc, c.socLimit)
		}

		// Keeps charging through the old limit...
		setSoC(c, 99)
		c.evaluate()
		if !c.active || client.stops != 0 {
			t.Fatalf("soc %v: stopped at 99 (stops=%d)", soc, client.stops)
		}
		// ...and stops at 100.
		setSoC(c, 100)
		c.evaluate()
		if c.active || client.stops != 1 {
			t.Fatalf("soc %v: not stopped at 100 (active=%v stops=%d)", soc, c.active, client.stops)
		}
	}
}

// A config push changing the battery soc_limit must not move the charge target.
func TestChargeTargetSurvivesBatterySocLimitUpdate(t *testing.T) {
	c, _ := newDirController(t, ChargeDirection, 60)
	setSoC(c, 70)
	soc := 40
	if err := c.processControlCommand(ControlCommand{
		Type: CommandUpdateConfig, Config: &ConfigUpdate{SocLimit: &soc},
	}); err != nil {
		t.Fatalf("update_config: %v", err)
	}
	if c.socLimit != 100 {
		t.Fatalf("charge soc limit = %v, want 100", c.socLimit)
	}
}

// P-102 (b): a charge schedule with soc_limit 100 behaves as before.
func TestChargeScheduleToHundredUnchanged(t *testing.T) {
	c, _ := newDirController(t, ChargeDirection, 60)
	c.AddSchedule(allDay("night", "charge", 100))
	setSoC(c, 30)
	c.checkTime()
	if !c.ready || c.socLimit != 100 {
		t.Fatalf("ready=%v soc limit=%v, want ready at 100", c.ready, c.socLimit)
	}
}

// P-102 (c): a manual discharge schedule below the battery's soc_limit stops at
// the battery's limit, not at its own.
func TestManualDischargeScheduleFlooredAtBatteryLimit(t *testing.T) {
	c, client := newDirController(t, DischargeDirection, 60)
	c.AddSchedule(allDay("evening", "discharge", 20))
	setSoC(c, 80)
	c.checkTime()
	if !c.ready || c.socLimit != 60 {
		t.Fatalf("ready=%v soc limit=%v, want ready with limit 60", c.ready, c.socLimit)
	}
	c.evaluate()
	if !c.active {
		t.Fatal("discharge should start")
	}
	setSoC(c, 60)
	c.evaluate()
	if c.active || client.stops != 1 {
		t.Fatalf("not stopped at the battery limit (active=%v stops=%d)", c.active, client.stops)
	}
}

// A schedule above the battery limit keeps its own, higher limit.
func TestDischargeScheduleAboveBatteryLimitKept(t *testing.T) {
	c, _ := newDirController(t, DischargeDirection, 60)
	c.AddSchedule(allDay("evening", "discharge", 75))
	setSoC(c, 90)
	c.checkTime()
	if c.socLimit != 75 {
		t.Fatalf("soc limit = %v, want 75", c.socLimit)
	}
}

// P-102 (d): explicit limits on start and set_limits cannot lower the discharge
// limit below the battery's.
func TestDischargeCommandLimitsFlooredAtBatteryLimit(t *testing.T) {
	low, high := 20, 75
	cases := []struct {
		name string
		cmd  ControlCommand
		want float64
	}{
		{"start below", ControlCommand{Type: CommandStart, Limits: &CommandLimits{SocLimit: &low}}, 60},
		{"start above", ControlCommand{Type: CommandStart, Limits: &CommandLimits{SocLimit: &high}}, 75},
		{"set_limits below", ControlCommand{Type: CommandSetLimits, Limits: &CommandLimits{SocLimit: &low}}, 60},
		{"set_limits above", ControlCommand{Type: CommandSetLimits, Limits: &CommandLimits{SocLimit: &high}}, 75},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newDirController(t, DischargeDirection, 60)
			setSoC(c, 90)
			if tc.cmd.Type == CommandStart {
				tc.cmd.Power = 2000
			}
			if err := c.processControlCommand(tc.cmd); err != nil {
				t.Fatalf("command: %v", err)
			}
			if c.socLimit != tc.want {
				t.Fatalf("soc limit = %v, want %v", c.socLimit, tc.want)
			}
		})
	}
}

// Charge limits from commands pass through: there is no lower bound for charge.
func TestChargeCommandLimitsNotClamped(t *testing.T) {
	c, _ := newDirController(t, ChargeDirection, 60)
	setSoC(c, 10)
	lim := 40
	if err := c.processControlCommand(ControlCommand{Type: CommandSetLimits, Limits: &CommandLimits{SocLimit: &lim}}); err != nil {
		t.Fatal(err)
	}
	if c.socLimit != 40 {
		t.Fatalf("soc limit = %v, want 40", c.socLimit)
	}
}

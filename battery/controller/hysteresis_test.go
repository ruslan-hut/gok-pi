package controller

import (
	"gok-pi/battery/entity"
	"io"
	"log/slog"
	"testing"
	"time"
)

// fakeClient records the battery operations a controller issues so a test can
// assert on how many mode changes a sequence of readings produces.
type fakeClient struct {
	starts int
	stops  int
	modes  []string
}

func (f *fakeClient) Status() (*entity.SystemStatus, error) { return nil, nil }

func (f *fakeClient) SwitchOperatingModeToManual(string) error {
	f.modes = append(f.modes, "manual")
	return nil
}

func (f *fakeClient) SwitchOperatingModeToAuto(string) error {
	f.modes = append(f.modes, "auto")
	return nil
}

func (f *fakeClient) StartDischarge(int) error { f.starts++; return nil }
func (f *fakeClient) StopDischarge() error     { f.stops++; return nil }
func (f *fakeClient) StartCharge(int) error    { f.starts++; return nil }
func (f *fakeClient) StopCharge() error        { f.stops++; return nil }

func newTestController(t *testing.T, client *fakeClient) *Controller {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c, err := New("BAT-TEST", client, DischargeDirection(client), log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.rate = 2000
	c.socLimit = 54
	return c
}

func setSoC(c *Controller, soc float64) {
	c.soc = soc
	c.status = &entity.SystemStatus{OperatingMode: "1", USOC: soc, RSOC: soc}
}

// TestDeadbandPreventsFlapping reproduces the production pattern from 2026-07-12,
// where USOC sat on the SoC limit and the controller started and stopped discharge
// twelve times in one evening. With the deadband, readings at the limit must not
// start the operation at all.
func TestDeadbandPreventsFlapping(t *testing.T) {
	client := &fakeClient{}
	c := newTestController(t, client)

	// USOC hovering exactly at and just above the limit, as in the real logs.
	for _, soc := range []float64{54, 54, 54.2, 54, 54.5, 54, 55, 54} {
		setSoC(c, soc)
		c.runOperation()
	}

	if client.starts != 0 {
		t.Fatalf("expected no discharge starts within the deadband, got %d (mode changes: %v)",
			client.starts, client.modes)
	}
	if c.active {
		t.Fatal("controller should not be active")
	}
}

// TestStartsOnceOutsideDeadband checks the deadband does not simply disable the
// controller: SoC clearly above limit+deadband must still start the operation.
func TestStartsOnceOutsideDeadband(t *testing.T) {
	client := &fakeClient{}
	c := newTestController(t, client)

	setSoC(c, 54+DefaultSocDeadband+1)
	c.runOperation()

	if client.starts != 1 {
		t.Fatalf("expected 1 start outside the deadband, got %d", client.starts)
	}
	if !c.active {
		t.Fatal("controller should be active after starting")
	}
	if c.lastTransition.IsZero() {
		t.Fatal("starting should record a transition timestamp")
	}
}

// TestMinDwellBlocksImmediateRestart covers the backstop for flapping that the SoC
// deadband alone does not explain, e.g. a power limit toggling across zero.
func TestMinDwellBlocksImmediateRestart(t *testing.T) {
	client := &fakeClient{}
	c := newTestController(t, client)

	soc := 54 + DefaultSocDeadband + 1
	setSoC(c, soc)
	c.runOperation()
	if client.starts != 1 {
		t.Fatalf("setup: expected 1 start, got %d", client.starts)
	}

	if err := c.stopOperation(); err != nil {
		t.Fatalf("stopOperation: %v", err)
	}

	// SoC is still well outside the deadband, so only the dwell may hold it back.
	setSoC(c, soc)
	c.runOperation()
	if client.starts != 1 {
		t.Fatalf("expected restart to be blocked by minimum dwell, got %d starts", client.starts)
	}

	// Once the dwell has elapsed the operation may start again.
	c.lastTransition = time.Now().Add(-DefaultMinDwell - time.Second)
	c.runOperation()
	if client.starts != 2 {
		t.Fatalf("expected restart after the dwell elapsed, got %d starts", client.starts)
	}
}

// scheduleAllDay returns an enabled discharge schedule whose window always covers
// the current time, so evaluate() takes the active-schedule path.
func scheduleAllDay(socLimit int) entity.Schedule {
	return entity.Schedule{
		Name:       "test-discharge",
		Type:       "discharge",
		Enabled:    true,
		StartTime:  "00:00",
		StopTime:   "23:59",
		PowerLimit: 2000,
		SocLimit:   socLimit,
	}
}

// TestScheduleEvaluationDoesNotFlapInsideDeadband reproduces the production pattern
// from 2026-07-27, where an evening of discharge produced twelve start/stop pairs
// with USOC pinned at 55. The schedule path used the start predicate to decide
// whether to stop, so start and stop shared the threshold limit+deadband and the
// deadband was cancelled out. An operation running inside the deadband must keep
// running until the true limit is reached.
func TestScheduleEvaluationDoesNotFlapInsideDeadband(t *testing.T) {
	client := &fakeClient{}
	c := newTestController(t, client)
	c.socLimit = 53
	c.schedules = []entity.Schedule{scheduleAllDay(53)}

	// Outside the deadband: the schedule starts the discharge.
	setSoC(c, 55)
	c.evaluate()
	if client.starts != 1 || !c.active {
		t.Fatalf("setup: expected the schedule to start discharge, got %d starts (active=%v)",
			client.starts, c.active)
	}

	// SoC drifts inside the deadband but never reaches the limit, exactly as in
	// the logs. This must not stop the operation.
	for _, soc := range []float64{54.6, 54, 54.4, 53.8, 54.2} {
		setSoC(c, soc)
		c.evaluate()
	}

	if client.stops != 0 {
		t.Fatalf("expected no stops inside the deadband, got %d (mode changes: %v)",
			client.stops, client.modes)
	}
	if !c.active {
		t.Fatal("discharge should still be running inside the deadband")
	}

	// Reaching the true limit still stops it.
	setSoC(c, 53)
	c.evaluate()
	if client.stops != 1 {
		t.Fatalf("expected discharge to stop at the true SoC limit, got %d stops", client.stops)
	}
	if c.active {
		t.Fatal("controller should be inactive after reaching the limit")
	}
}

// TestScheduleEvaluationStopsWhenWindowEnds guards the other direction: leaving the
// deadband-hold in place must not keep an operation alive past its schedule.
func TestScheduleEvaluationStopsWhenWindowEnds(t *testing.T) {
	client := &fakeClient{}
	c := newTestController(t, client)
	c.socLimit = 53
	c.schedules = []entity.Schedule{scheduleAllDay(53)}

	setSoC(c, 55)
	c.evaluate()
	if !c.active {
		t.Fatal("setup: controller should be active")
	}

	// Schedule no longer covers the current time; SoC is still inside the deadband.
	c.schedules[0].Enabled = false
	setSoC(c, 54)
	c.evaluate()

	if client.stops != 1 {
		t.Fatalf("expected discharge to stop when the schedule ended, got %d stops", client.stops)
	}
	if c.active {
		t.Fatal("controller should be inactive after the schedule ended")
	}
}

// TestScheduleEvaluationStopsOnInvalidPowerLimit checks that a config push that
// zeroes the power limit still stops the operation; only the SoC deadband is
// exempt from stopping.
func TestScheduleEvaluationStopsOnInvalidPowerLimit(t *testing.T) {
	client := &fakeClient{}
	c := newTestController(t, client)
	c.socLimit = 53
	c.schedules = []entity.Schedule{scheduleAllDay(53)}

	setSoC(c, 55)
	c.evaluate()
	if !c.active {
		t.Fatal("setup: controller should be active")
	}

	c.schedules[0].PowerLimit = 0
	c.evaluate()

	if client.stops != 1 {
		t.Fatalf("expected discharge to stop on an invalid power limit, got %d stops", client.stops)
	}
	if c.active {
		t.Fatal("controller should be inactive after the power limit went invalid")
	}
}

// TestStopUsesTrueLimitNotDeadband guards the safety property that the deadband
// only delays starting. An active operation must still stop the moment the real
// SoC limit is reached, never a deadband later.
func TestStopUsesTrueLimitNotDeadband(t *testing.T) {
	client := &fakeClient{}
	c := newTestController(t, client)

	setSoC(c, 54+DefaultSocDeadband+1)
	c.runOperation()
	if !c.active {
		t.Fatal("setup: controller should be active")
	}

	setSoC(c, 54)
	c.runOperation()

	if client.stops != 1 {
		t.Fatalf("expected discharge to stop at the true SoC limit, got %d stops", client.stops)
	}
	if c.active {
		t.Fatal("controller should be inactive after reaching the limit")
	}
}

package main

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"gok-pi/battery/driver"
	"gok-pi/battery/entity"
)

// opsDriver is a battery that reports a fixed status and records the operations
// the controllers issue against it.
type opsDriver struct {
	mu     sync.Mutex
	status entity.SystemStatus
	ops    []string
}

func (d *opsDriver) record(op string) {
	d.mu.Lock()
	d.ops = append(d.ops, op)
	d.mu.Unlock()
}

func (d *opsDriver) has(op string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, o := range d.ops {
		if o == op {
			return true
		}
	}
	return false
}

func (d *opsDriver) Status() (*entity.SystemStatus, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.status
	return &s, nil
}
func (d *opsDriver) StartDischarge(int) error                 { d.record("start_discharge"); return nil }
func (d *opsDriver) StopDischarge() error                     { d.record("stop_discharge"); return nil }
func (d *opsDriver) StartCharge(int) error                    { d.record("start_charge"); return nil }
func (d *opsDriver) StopCharge() error                        { d.record("stop_charge"); return nil }
func (d *opsDriver) SwitchOperatingModeToManual(string) error { d.record("manual"); return nil }
func (d *opsDriver) SwitchOperatingModeToAuto(string) error   { d.record("auto"); return nil }

var (
	opsDriversMu sync.Mutex
	opsDrivers   = map[string]*opsDriver{}
)

func init() {
	driver.Register("ops-fake", func(cfg entity.BatteryConfig, _ *slog.Logger) (driver.Driver, error) {
		opsDriversMu.Lock()
		defer opsDriversMu.Unlock()
		return opsDrivers[cfg.Name], nil
	})
}

// TestScheduleTypeAddedAfterStart: the agent starts with schedules of one type
// only, then the server pushes a schedule of the other type that is active now.
// That controller must exist and run it; before the fix it did not exist, the
// push was dropped and the window was missed until the next restart.
func TestScheduleTypeAddedAfterStart(t *testing.T) {
	cases := []struct {
		name       string
		initial    string // type present at start
		added      string // type pushed afterwards
		rsoc       float64
		wantOp     string
		wantAbsent string
	}{
		{"charge after discharge-only start", "discharge", "charge", 20, "start_charge", "start_discharge"},
		{"discharge after charge-only start", "charge", "discharge", 90, "start_discharge", "start_charge"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			batName := "BAT-" + tc.added
			fake := &opsDriver{status: entity.SystemStatus{
				OperatingMode: "2", RSOC: tc.rsoc, USOC: tc.rsoc,
			}}
			opsDriversMu.Lock()
			opsDrivers[batName] = fake
			opsDriversMu.Unlock()

			ctx, cancel := context.WithCancel(context.Background())
			var wg sync.WaitGroup
			defer func() { cancel(); wg.Wait() }()
			log := slog.New(slog.NewTextHandler(io.Discard, nil))

			bat := entity.BatteryConfig{Name: batName, Driver: "ops-fake", Enabled: true}
			sched := func(typ string) entity.Schedule {
				// All day, so the test does not depend on the clock.
				return entity.Schedule{
					Name: typ + "-all-day", Type: typ, BatteryName: batName, Enabled: true,
					StartTime: "00:00", StopTime: "24:00", PowerLimit: 1000, SocLimit: 50,
				}
			}
			// A SoC limit on the right side of RSOC, so the new window starts.
			added := sched(tc.added)
			if tc.added == "charge" {
				added.SocLimit = 80
			}

			m := newWorkerManager()
			m.Apply(ctx, &wg, []entity.BatteryConfig{bat}, []entity.Schedule{sched(tc.initial)}, "UTC", log)

			entry, ok := m.getEntry(batName)
			if !ok {
				t.Fatal("no worker started")
			}
			if entry.charger == nil || entry.discharger == nil {
				t.Fatalf("both controllers must exist, charger=%v discharger=%v",
					entry.charger != nil, entry.discharger != nil)
			}

			// Same battery config, new schedule set: the update_config path.
			m.Apply(ctx, &wg, []entity.BatteryConfig{bat},
				[]entity.Schedule{sched(tc.initial), added}, "UTC", log)

			// The controller acts on the next poll (10 s interval).
			deadline := time.Now().Add(25 * time.Second)
			for !fake.has(tc.wantOp) {
				if time.Now().After(deadline) {
					t.Fatalf("%s never issued; ops=%v", tc.wantOp, fake.ops)
				}
				time.Sleep(100 * time.Millisecond)
			}
			if fake.has(tc.wantAbsent) {
				t.Fatalf("%s issued by the wrong controller; ops=%v", tc.wantAbsent, fake.ops)
			}
		})
	}
}

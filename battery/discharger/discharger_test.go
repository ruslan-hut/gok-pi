package discharger

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"gok-pi/battery/entity"
)

type fakeClient struct {
	switchToManual []string
	switchToAuto   []string
	startCalls     []int
	stopCalls      int
}

func (f *fakeClient) Status() (*entity.SystemStatus, error) {
	return nil, nil
}

func (f *fakeClient) StartDischarge(power int) error {
	f.startCalls = append(f.startCalls, power)
	return nil
}

func (f *fakeClient) StopDischarge() error {
	f.stopCalls++
	return nil
}

func (f *fakeClient) SwitchOperatingModeToManual(currentMode string) error {
	f.switchToManual = append(f.switchToManual, currentMode)
	return nil
}

func (f *fakeClient) SwitchOperatingModeToAuto(currentMode string) error {
	f.switchToAuto = append(f.switchToAuto, currentMode)
	return nil
}

func newTestDischarger(client Client) *Discharge {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &Discharge{
		client:   client,
		log:      logger,
		commands: make(chan ControlCommand, 4),
	}
}

func TestProcessControlCommandStart(t *testing.T) {
	fc := &fakeClient{}
	d := newTestDischarger(fc)
	d.status = &entity.SystemStatus{OperatingMode: "2"}
	d.powerLimit = 600

	err := d.processControlCommand(ControlCommand{
		Type:  CommandStartDischarge,
		Power: 700,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !d.manualOverride {
		t.Fatal("expected manual override to be true")
	}
	if !d.isDischarging {
		t.Fatal("expected discharger to be marked as discharging")
	}
	if len(fc.switchToManual) != 1 || fc.switchToManual[0] != "2" {
		t.Fatalf("expected manual switch with current mode '2', got %v", fc.switchToManual)
	}
	if len(fc.startCalls) != 1 || fc.startCalls[0] != 700 {
		t.Fatalf("expected start call with 700, got %v", fc.startCalls)
	}
}

func TestProcessControlCommandSetLimits(t *testing.T) {
	fc := &fakeClient{}
	d := newTestDischarger(fc)
	d.manualOverride = true
	d.status = &entity.SystemStatus{OperatingMode: "1"}
	d.capacity = 2000
	d.capacityLimit = 1000
	d.stopTime = time.Now().Add(2 * time.Hour)

	power := 550
	soc := 40
	err := d.processControlCommand(ControlCommand{
		Type: CommandSetLimits,
		Limits: &CommandLimits{
			PowerLimit: &power,
			SocLimit:   &soc,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.powerLimit != 550 {
		t.Fatalf("expected power limit 550, got %d", d.powerLimit)
	}
	if int(d.socLimit) != 40 {
		t.Fatalf("expected soc limit 40, got %f", d.socLimit)
	}
	if len(fc.startCalls) != 1 {
		t.Fatalf("expected call to start discharge with new rate, got %v", fc.startCalls)
	}
}

func TestProcessControlCommandForceMode(t *testing.T) {
	fc := &fakeClient{}
	d := newTestDischarger(fc)
	d.status = &entity.SystemStatus{OperatingMode: "2"}

	err := d.processControlCommand(ControlCommand{
		Type: CommandForceMode,
		Mode: OperatingModeManual,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d.manualOverride {
		t.Fatal("expected manual override to be true after forcing manual mode")
	}
	if len(fc.switchToManual) != 1 {
		t.Fatalf("expected manual switch call, got %v", fc.switchToManual)
	}

	err = d.processControlCommand(ControlCommand{
		Type: CommandForceMode,
		Mode: OperatingModeAuto,
	})
	if err != nil {
		t.Fatalf("unexpected error switching to auto: %v", err)
	}
	if d.manualOverride {
		t.Fatal("expected manual override reset after switching to auto")
	}
	if len(fc.switchToAuto) != 1 {
		t.Fatalf("expected auto switch call, got %v", fc.switchToAuto)
	}
}

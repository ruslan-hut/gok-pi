package controller

import (
	"errors"
	"gok-pi/battery/entity"
	"io"
	"log/slog"
	"testing"
)

func TestModeCoordinator_AutoOnlyWhenNoneActive(t *testing.T) {
	m := NewModeCoordinator()
	autoCalls := 0
	noopManual := func() error { return nil }
	countAuto := func() error { autoCalls++; return nil }

	if err := m.AcquireManual("discharge", noopManual); err != nil {
		t.Fatalf("acquire discharge: %v", err)
	}
	if err := m.AcquireManual("charge", noopManual); err != nil {
		t.Fatalf("acquire charge: %v", err)
	}

	// Discharge releases first: charge still active, must NOT switch to auto.
	if err := m.ReleaseToAuto("discharge", countAuto); err != nil {
		t.Fatalf("release discharge: %v", err)
	}
	if autoCalls != 0 {
		t.Fatalf("switched to auto while charge still active (calls=%d)", autoCalls)
	}

	// Charge releases: now nobody active, must switch to auto exactly once.
	if err := m.ReleaseToAuto("charge", countAuto); err != nil {
		t.Fatalf("release charge: %v", err)
	}
	if autoCalls != 1 {
		t.Fatalf("expected 1 auto switch, got %d", autoCalls)
	}
}

func TestModeCoordinator_AcquireFailureDoesNotMarkActive(t *testing.T) {
	m := NewModeCoordinator()
	wantErr := errors.New("boom")
	if err := m.AcquireManual("discharge", func() error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("expected error, got %v", err)
	}
	// Since acquire failed, releasing should switch to auto (nobody active).
	auto := false
	if err := m.ReleaseToAuto("discharge", func() error { auto = true; return nil }); err != nil {
		t.Fatalf("release: %v", err)
	}
	if !auto {
		t.Fatal("expected auto switch since acquire never marked active")
	}
}

func TestModeCoordinator_MarkActiveBlocksAuto(t *testing.T) {
	m := NewModeCoordinator()
	m.MarkActive("discharge") // adopted on startup
	auto := false
	if err := m.ReleaseToAuto("charge", func() error { auto = true; return nil }); err != nil {
		t.Fatalf("release: %v", err)
	}
	if auto {
		t.Fatal("switched to auto while adopted discharge still active")
	}
}

// TestStopIgnoresAutoModeActivity pins the 2026-08-16 log noise: with the battery
// taking PV into store in auto mode, the charge controller matched on the charging
// flag alone and issued a setpoint POST plus a "stopped charge" line on every poll
// for five hours. Nothing is running in manual mode, so there is nothing to stop.
func TestStopIgnoresAutoModeActivity(t *testing.T) {
	client := &fakeClient{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c, err := New("BAT-AUTO", client, ChargeDirection(client), log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	c.status = &entity.SystemStatus{OperatingMode: batteryModeAuto, USOC: 92, BatteryCharging: true}

	if err := c.stopOperation(); err != nil {
		t.Fatalf("stopOperation: %v", err)
	}
	if client.stops != 0 {
		t.Fatalf("expected no stop command in auto mode, got %d", client.stops)
	}
	if len(client.modes) != 0 {
		t.Fatalf("expected no mode switch in auto mode, got %v", client.modes)
	}

	// The same reading in manual mode is this controller's to stop.
	c.status.OperatingMode = batteryModeManual
	if err := c.stopOperation(); err != nil {
		t.Fatalf("stopOperation: %v", err)
	}
	if client.stops != 1 {
		t.Fatalf("expected one stop command in manual mode, got %d", client.stops)
	}
}

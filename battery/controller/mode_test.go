package controller

import (
	"errors"
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

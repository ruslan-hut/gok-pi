package main

import (
	"encoding/json"
	"testing"

	"gok-pi/battery/discharger"
	"gok-pi/internal/remote/wsclient"
)

func TestTranslateCommandStart(t *testing.T) {
	payload := map[string]int{"power": 750}
	raw, _ := json.Marshal(payload)
	cmd := wsclient.Command{
		Type:    "agent.command",
		Command: "start_discharge",
		Target:  "battery1",
		Payload: raw,
	}

	control, err := translateCommand(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if control.Type != discharger.CommandStartDischarge {
		t.Fatalf("expected start discharge, got %s", control.Type)
	}
	if control.Power != 750 {
		t.Fatalf("expected power 750, got %d", control.Power)
	}
}

func TestTranslateCommandSetLimits(t *testing.T) {
	payload := map[string]int{
		"power_limit": 900,
		"soc_limit":   45,
	}
	raw, _ := json.Marshal(payload)
	cmd := wsclient.Command{
		Type:    "agent.command",
		Command: "set_limits",
		Target:  "battery1",
		Payload: raw,
	}

	control, err := translateCommand(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if control.Type != discharger.CommandSetLimits {
		t.Fatalf("expected set limits, got %s", control.Type)
	}
	if control.Limits == nil {
		t.Fatal("expected limits payload")
	}
	if control.Limits.PowerLimit == nil || *control.Limits.PowerLimit != 900 {
		t.Fatalf("expected power limit 900, got %+v", control.Limits.PowerLimit)
	}
	if control.Limits.SocLimit == nil || *control.Limits.SocLimit != 45 {
		t.Fatalf("expected soc limit 45, got %+v", control.Limits.SocLimit)
	}
}

func TestTranslateCommandForceMode(t *testing.T) {
	payload := map[string]string{"mode": "manual"}
	raw, _ := json.Marshal(payload)
	cmd := wsclient.Command{
		Type:    "agent.command",
		Command: "force_mode",
		Target:  "battery1",
		Payload: raw,
	}

	control, err := translateCommand(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if control.Mode != discharger.OperatingModeManual {
		t.Fatalf("expected manual mode, got %s", control.Mode)
	}
}

func TestTranslateCommandForceModeInvalid(t *testing.T) {
	payload := map[string]string{"mode": "invalid"}
	raw, _ := json.Marshal(payload)
	cmd := wsclient.Command{
		Type:    "agent.command",
		Command: "force_mode",
		Target:  "battery1",
		Payload: raw,
	}

	_, err := translateCommand(cmd)
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
}

package main

import (
	"encoding/json"
	"testing"

	"gok-pi/battery/controller"
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
	if control.Type != controller.CommandStart {
		t.Fatalf("expected start, got %s", control.Type)
	}
	if control.Power != 750 {
		t.Fatalf("expected power 750, got %d", control.Power)
	}
}

// TestTranslateCommandStartFromCharger decodes the exact payload the control
// server sends for an EV charging session (remote/server/chargers.go): the
// limits ride along with the start, and the source marks the override as one
// that must outlive config pushes.
func TestTranslateCommandStartFromCharger(t *testing.T) {
	raw := []byte(`{"power":3000,"power_limit":3000,"soc_limit":40,"source":"charger"}`)
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
	if control.Type != controller.CommandStart {
		t.Fatalf("expected start, got %s", control.Type)
	}
	if control.Power != 3000 {
		t.Fatalf("expected power 3000, got %d", control.Power)
	}
	if control.Source != controller.CommandSourceCharger {
		t.Fatalf("expected source %q, got %q", controller.CommandSourceCharger, control.Source)
	}
	if control.Limits == nil {
		t.Fatal("expected limits to be carried with the start")
	}
	if control.Limits.PowerLimit == nil || *control.Limits.PowerLimit != 3000 {
		t.Fatalf("power limit = %v, want 3000", control.Limits.PowerLimit)
	}
	if control.Limits.SocLimit == nil || *control.Limits.SocLimit != 40 {
		t.Fatalf("soc limit = %v, want 40", control.Limits.SocLimit)
	}
}

// A UI start carries no limits and no source, and must stay an ordinary manual
// override that a config push cancels.
func TestTranslateCommandStartFromUIHasNoSource(t *testing.T) {
	cmd := wsclient.Command{
		Type:    "agent.command",
		Command: "start_discharge",
		Target:  "battery1",
		Payload: []byte(`{"power":750}`),
	}

	control, err := translateCommand(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if control.Source != "" {
		t.Fatalf("source = %q, want empty", control.Source)
	}
	if control.Limits != nil {
		t.Fatalf("limits = %+v, want nil", control.Limits)
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
	if control.Type != controller.CommandSetLimits {
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
	if control.Mode != controller.OperatingModeManual {
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

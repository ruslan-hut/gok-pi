package wsclient

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"gok-pi/internal/config"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestHandleConfigPush(t *testing.T) {
	client := New(config.RemoteControl{}, AgentMetadata{ID: "agent-1", Env: "test"}, newTestLogger())

	message := map[string]interface{}{
		"type":     "server.config.push",
		"agent_id": "agent-1",
		"config": map[string]interface{}{
			"revision":   2,
			"updated_at": time.Now().UTC().Format(time.RFC3339),
			"batteries": []map[string]interface{}{
				{
					"name":           "battery-1",
					"url":            "http://example",
					"token":          "secret",
					"enabled":        true,
					"capacity_limit": 400,
				},
			},
			"schedules": []map[string]interface{}{
				{
					"start_time":   "08:00",
					"stop_time":    "10:00",
					"battery_name": "battery-1",
					"enabled":      true,
					"power_limit":  200,
					"soc_limit":    50,
				},
			},
		},
		"sent_at": time.Now().UTC().Format(time.RFC3339),
	}

	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}

	client.handleConfigPush(raw)

	select {
	case update := <-client.ConfigUpdates():
		if update.AgentID != "agent-1" {
			t.Fatalf("expected agent-1, got %s", update.AgentID)
		}
		if update.Config.Revision != 2 {
			t.Fatalf("expected revision 2, got %d", update.Config.Revision)
		}
		if len(update.Config.Batteries) != 1 {
			t.Fatalf("expected 1 battery, got %d", len(update.Config.Batteries))
		}
	case <-time.After(time.Second):
		t.Fatal("expected config update to be enqueued")
	}
}

// TestHandleConfigPushIgnoresForeignFields is the regression guard for a whole
// class of outage: the server's schema for a field the agent never reads moved
// ahead of the binary on the device, and the agent rejected every config push —
// running on stale config with nothing but a log line to say so.
func TestHandleConfigPushIgnoresForeignFields(t *testing.T) {
	client := New(config.RemoteControl{}, AgentMetadata{ID: "agent-1", Env: "test"}, newTestLogger())

	message := map[string]interface{}{
		"type":     "server.config.push",
		"agent_id": "agent-1",
		"config": map[string]interface{}{
			"revision": 7,
			"timezone": "Europe/Madrid",
			"batteries": []map[string]interface{}{
				{"name": "battery-1", "url": "http://example", "enabled": true},
			},
			"schedules": []map[string]interface{}{
				{"name": "night", "battery_name": "battery-1", "enabled": true},
			},
			// A shape this binary knows nothing about, in a field it never reads.
			// Typed as anything concrete, this alone fails the whole decode — which
			// is exactly how the server moving ahead of a device took config with it.
			"email_reports":       "moved-elsewhere",
			"some_future_section": map[string]interface{}{"anything": []int{1, 2, 3}},
		},
		"sent_at": time.Now().UTC().Format(time.RFC3339),
	}

	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}

	client.handleConfigPush(raw)

	select {
	case update := <-client.ConfigUpdates():
		if update.Config.Revision != 7 {
			t.Fatalf("expected revision 7, got %d", update.Config.Revision)
		}
		if update.Config.Timezone != "Europe/Madrid" {
			t.Fatalf("expected the timezone to survive, got %q", update.Config.Timezone)
		}
		if len(update.Config.Batteries) != 1 || len(update.Config.Schedules) != 1 {
			t.Fatalf("expected batteries and schedules to be applied, got %d/%d",
				len(update.Config.Batteries), len(update.Config.Schedules))
		}
	case <-time.After(time.Second):
		t.Fatal("a field the agent does not read blocked the whole config push")
	}
}

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
			"revision":  2,
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


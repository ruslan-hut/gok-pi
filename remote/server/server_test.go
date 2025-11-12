package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gok-pi/battery/entity"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestHandleAgentConfigGet(t *testing.T) {
	srv := New(Config{}, testLogger())

	expected, err := srv.configs.Save("agent-1", AgentConfigRequest{
		Revision: 0,
	})
	if err != nil {
		t.Fatalf("Save config: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/agent-1/config", nil)
	rr := httptest.NewRecorder()

	srv.handleAgentRoutes(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var response AgentConfig
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Revision != expected.Revision {
		t.Fatalf("expected revision %d, got %d", expected.Revision, response.Revision)
	}
}

func TestHandleAgentConfigPutConflict(t *testing.T) {
	srv := New(Config{}, testLogger())

	current, err := srv.configs.Save("agent-1", AgentConfigRequest{Revision: 0})
	if err != nil {
		t.Fatalf("Save config: %v", err)
	}

	payload := map[string]interface{}{
		"revision":  current.Revision + 1,
		"batteries": []interface{}{},
		"schedules": []interface{}{},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/agents/agent-1/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	srv.handleAgentRoutes(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d (%s)", rr.Code, rr.Body.String())
	}
}

func TestHandleAgentConfigPutSuccess(t *testing.T) {
	srv := New(Config{}, testLogger())

	initial, err := srv.configs.Save("agent-1", AgentConfigRequest{Revision: 0})
	if err != nil {
		t.Fatalf("Save config: %v", err)
	}

	payload := map[string]interface{}{
		"revision": initial.Revision,
		"batteries": []map[string]interface{}{
			{
				"name":           "battery-1",
				"url":            "http://example",
				"token":          "secret",
				"enabled":        true,
				"capacity_limit": 500,
			},
		},
		"schedules": []map[string]interface{}{
			{
				"start_time":   "18:00",
				"stop_time":    "20:00",
				"battery_name": "battery-1",
				"enabled":      true,
				"power_limit":  300,
				"soc_limit":    40,
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/agents/agent-1/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	srv.handleAgentRoutes(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (%s)", rr.Code, rr.Body.String())
	}

	var updated AgentConfig
	if err := json.Unmarshal(rr.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if updated.Revision != initial.Revision+1 {
		t.Fatalf("expected revision %d, got %d", initial.Revision+1, updated.Revision)
	}
	if len(updated.Batteries) != 1 {
		t.Fatalf("expected 1 battery in response, got %d", len(updated.Batteries))
	}
}

func TestOnAgentConfigSyncSeedsStore(t *testing.T) {
	srv := New(Config{}, testLogger())

	msg := AgentConfigSync{
		Type: "agent.config",
		Config: AgentConfigSnapshot{
			Batteries: []entity.BatteryConfig{
				{
					Name:          "battery-1",
					Url:           "http://example",
					Token:         "secret",
					Enabled:       true,
					CapacityLimit: 400,
				},
			},
			Schedules: []entity.Schedule{
				{
					StartTime:   "08:00",
					StopTime:    "09:00",
					BatteryName: "battery-1",
					Enabled:     true,
					PowerLimit:  200,
					SocLimit:    50,
				},
			},
		},
		SentAt: time.Now().UTC(),
	}

	srv.onAgentConfigSync("agent-1", msg)

	cfg, ok := srv.configs.Get("agent-1")
	if !ok {
		t.Fatal("expected config to be seeded")
	}
	if cfg.Revision != 1 {
		t.Fatalf("expected revision 1, got %d", cfg.Revision)
	}

	// Second sync should not overwrite revision.
	msg.Config.Batteries[0].CapacityLimit = 999
	srv.onAgentConfigSync("agent-1", msg)
	cfg2, _ := srv.configs.Get("agent-1")
	if cfg2.Revision != 1 {
		t.Fatalf("expected revision to remain 1, got %d", cfg2.Revision)
	}
	if cfg2.Batteries[0].CapacityLimit != 400 {
		t.Fatalf("expected original capacity limit preserved, got %d", cfg2.Batteries[0].CapacityLimit)
	}
}

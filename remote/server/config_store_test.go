package server

import (
	"path/filepath"
	"testing"
	"time"

	"gok-pi/battery/entity"
)

func TestConfigStore_SaveAndGet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "configs.json")

	store, err := NewConfigStore(path)
	if err != nil {
		t.Fatalf("NewConfigStore: %v", err)
	}

	req := AgentConfigRequest{
		Revision: 0,
		Batteries: []entity.BatteryConfig{
			{
				Name:          "battery-1",
				Url:           "http://example",
				Token:         "token",
				Enabled:       true,
				CapacityLimit: 1000,
			},
		},
		Schedules: []entity.Schedule{
			{
				StartTime:   "18:00",
				StopTime:    "20:00",
				BatteryName: "battery-1",
				Enabled:     true,
				PowerLimit:  500,
				SocLimit:    40,
			},
		},
	}

	saved, err := store.Save("agent-1", req)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.Revision != 1 {
		t.Fatalf("expected revision 1, got %d", saved.Revision)
	}
	if saved.UpdatedAt.IsZero() {
		t.Fatal("expected UpdatedAt to be set")
	}

	got, ok := store.Get("agent-1")
	if !ok {
		t.Fatal("expected config to exist")
	}
	if got.Revision != saved.Revision {
		t.Fatalf("expected revision %d, got %d", saved.Revision, got.Revision)
	}
	if got.Batteries[0].Name != "battery-1" {
		t.Fatalf("expected battery name preserved, got %q", got.Batteries[0].Name)
	}

	// ensure returned config is a clone
	got.Batteries[0].Name = "mutated"
	got2, _ := store.Get("agent-1")
	if got2.Batteries[0].Name != "battery-1" {
		t.Fatal("store was mutated when returned config changed")
	}

	updateReq := AgentConfigRequest{
		Revision:  saved.Revision,
		Batteries: req.Batteries,
		Schedules: req.Schedules,
	}

	updated, err := store.Save("agent-1", updateReq)
	if err != nil {
		t.Fatalf("Save update: %v", err)
	}
	if updated.Revision != 2 {
		t.Fatalf("expected revision 2, got %d", updated.Revision)
	}
	if !updated.UpdatedAt.After(saved.UpdatedAt) {
		t.Fatal("expected UpdatedAt to increase on update")
	}

	// ensure the data persisted to disk
	fresh, err := NewConfigStore(path)
	if err != nil {
		t.Fatalf("NewConfigStore reload: %v", err)
	}
	reloaded, ok := fresh.Get("agent-1")
	if !ok {
		t.Fatal("expected config to persist on disk")
	}
	if reloaded.Revision != updated.Revision {
		t.Fatalf("expected revision %d, got %d after reload", updated.Revision, reloaded.Revision)
	}
}

func TestConfigStore_SaveConflict(t *testing.T) {
	store, err := NewConfigStore("")
	if err != nil {
		t.Fatalf("NewConfigStore: %v", err)
	}

	if _, err := store.Save("agent-1", AgentConfigRequest{
		Revision: 1,
	}); err == nil {
		t.Fatal("expected conflict when creating config with non-zero revision")
	}

	initial, err := store.Save("agent-1", AgentConfigRequest{Revision: 0})
	if err != nil {
		t.Fatalf("Save initial: %v", err)
	}
	if _, err := store.Save("agent-1", AgentConfigRequest{Revision: initial.Revision + 1}); err == nil {
		t.Fatal("expected conflict when revision does not match current")
	}
}

func TestConfigStore_TimestampsCloned(t *testing.T) {
	store, err := NewConfigStore("")
	if err != nil {
		t.Fatalf("NewConfigStore: %v", err)
	}

	cfg, err := store.Save("agent-1", AgentConfigRequest{Revision: 0})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	cfg.UpdatedAt = cfg.UpdatedAt.Add(10 * time.Hour)
	fetched, _ := store.Get("agent-1")
	if fetched.UpdatedAt == cfg.UpdatedAt {
		t.Fatal("expected UpdatedAt to be cloned when returned")
	}
}


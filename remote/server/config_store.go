package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gok-pi/battery/entity"
)

var (
	// ErrConfigConflict is returned when the provided revision does not match the latest stored revision.
	ErrConfigConflict = errors.New("agent config revision conflict")
	// ErrInvalidSchedules is returned when schedule validation fails.
	ErrInvalidSchedules = errors.New("invalid schedules")
)

// AgentConfig represents the persisted configuration overrides for a gok-pi agent.
type AgentConfig struct {
	DeviceName string                 `json:"device_name,omitempty"`
	Env        string                 `json:"env,omitempty"`
	Timezone   string                 `json:"timezone,omitempty"`
	Revision   int                    `json:"revision"`
	UpdatedAt  time.Time              `json:"updated_at"`
	Batteries  []entity.BatteryConfig `json:"batteries"`
	Schedules  []entity.Schedule      `json:"schedules"`
}

// AgentConfigRequest is the payload accepted by the HTTP API when a config is updated.
type AgentConfigRequest struct {
	DeviceName string                 `json:"device_name,omitempty"`
	Env        string                 `json:"env,omitempty"`
	Timezone   string                 `json:"timezone,omitempty"`
	Revision   int                    `json:"revision"`
	Batteries  []entity.BatteryConfig `json:"batteries"`
	Schedules  []entity.Schedule      `json:"schedules"`
}

type configSnapshot map[string]AgentConfig

// ConfigStore persists agent configuration overrides to disk.
type ConfigStore struct {
	path string

	mu      sync.RWMutex
	records configSnapshot
}

// NewConfigStore creates a new ConfigStore. When path is empty the store remains in-memory.
func NewConfigStore(path string) (*ConfigStore, error) {
	store := &ConfigStore{
		path:    path,
		records: make(configSnapshot),
	}

	if path == "" {
		return store, nil
	}

	if err := store.load(); err != nil {
		return nil, err
	}

	return store, nil
}

func (cs *ConfigStore) load() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	data, err := os.ReadFile(cs.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read config store: %w", err)
	}

	var snapshot configSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("decode config store: %w", err)
	}

	cs.records = snapshot
	return nil
}

// Get returns the latest config for an agent. The boolean indicates whether the config exists.
func (cs *ConfigStore) Get(agentID string) (AgentConfig, bool) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	cfg, ok := cs.records[agentID]
	if !ok {
		return AgentConfig{}, false
	}

	return cloneAgentConfig(cfg), true
}

// Save writes a new configuration for the given agent while enforcing optimistic locking via the revision field.
func (cs *ConfigStore) Save(agentID string, req AgentConfigRequest) (AgentConfig, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	current, exists := cs.records[agentID]
	expectedRevision := req.Revision

	switch {
	case !exists && expectedRevision != 0:
		return AgentConfig{}, ErrConfigConflict
	case exists && expectedRevision != current.Revision:
		return AgentConfig{}, ErrConfigConflict
	}

	// Validate schedules (including uniqueness check)
	if err := entity.ValidateSchedules(req.Schedules); err != nil {
		return AgentConfig{}, fmt.Errorf("%w: %v", ErrInvalidSchedules, err)
	}

	next := AgentConfig{
		DeviceName: req.DeviceName,
		Env:        req.Env,
		Timezone:   req.Timezone,
		Revision:   1,
		UpdatedAt:  time.Now().UTC(),
		Batteries:  cloneBatteryConfigs(req.Batteries),
		Schedules:  cloneSchedules(req.Schedules),
	}

	if exists {
		next.Revision = current.Revision + 1
	}

	cs.records[agentID] = next

	if err := cs.persistLocked(); err != nil {
		return AgentConfig{}, err
	}

	return cloneAgentConfig(next), nil
}

// Seed inserts a configuration when no record exists for the agent yet. It returns the resulting config and
// whether it was inserted.
func (cs *ConfigStore) Seed(agentID string, batteries []entity.BatteryConfig, schedules []entity.Schedule, ts time.Time) (AgentConfig, bool, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if current, exists := cs.records[agentID]; exists {
		return cloneAgentConfig(current), false, nil
	}

	cfg := AgentConfig{
		DeviceName: "",
		Timezone:   "",
		Revision:   1,
		UpdatedAt:  ts.UTC(),
		Batteries:  cloneBatteryConfigs(batteries),
		Schedules:  cloneSchedules(schedules),
	}
	if cfg.UpdatedAt.IsZero() {
		cfg.UpdatedAt = time.Now().UTC()
	}

	cs.records[agentID] = cfg
	if err := cs.persistLocked(); err != nil {
		delete(cs.records, agentID)
		return AgentConfig{}, false, err
	}

	return cloneAgentConfig(cfg), true, nil
}

// UpdateAutoSchedules replaces all auto-prefixed schedules for the given agent
// while preserving manual schedules. It bumps the revision atomically.
func (cs *ConfigStore) UpdateAutoSchedules(agentID string, autoSchedules []entity.Schedule) (AgentConfig, bool, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	current, exists := cs.records[agentID]
	if !exists {
		return AgentConfig{}, false, nil
	}

	// Keep only manual schedules
	var manual []entity.Schedule
	for _, s := range current.Schedules {
		if !isAutoScheduleName(s.Name) {
			manual = append(manual, s)
		}
	}

	merged := append(manual, autoSchedules...)

	// Check if schedules actually changed
	if schedulesEqual(current.Schedules, merged) {
		return cloneAgentConfig(current), false, nil
	}

	next := cloneAgentConfig(current)
	next.Schedules = cloneSchedules(merged)
	next.Revision = current.Revision + 1
	next.UpdatedAt = time.Now().UTC()

	cs.records[agentID] = next

	if err := cs.persistLocked(); err != nil {
		cs.records[agentID] = current
		return AgentConfig{}, false, err
	}

	return cloneAgentConfig(next), true, nil
}

func isAutoScheduleName(name string) bool {
	return len(name) >= 5 && name[:5] == "auto-"
}

func schedulesEqual(a, b []entity.Schedule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Type != b[i].Type ||
			a[i].StartTime != b[i].StartTime || a[i].StopTime != b[i].StopTime ||
			a[i].BatteryName != b[i].BatteryName || a[i].Enabled != b[i].Enabled ||
			a[i].PowerLimit != b[i].PowerLimit || a[i].SocLimit != b[i].SocLimit {
			return false
		}
	}
	return true
}

// snapshot returns a copy of all stored configs.
func (cs *ConfigStore) snapshot() configSnapshot {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	out := make(configSnapshot, len(cs.records))
	for k, v := range cs.records {
		out[k] = cloneAgentConfig(v)
	}
	return out
}

func (cs *ConfigStore) persistLocked() error {
	if cs.path == "" {
		return nil
	}

	dir := filepath.Dir(cs.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure config store dir: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, "agent-config-*.json")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer func() {
		_ = os.Remove(tmpFile.Name())
	}()

	enc := json.NewEncoder(tmpFile)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cs.records); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("encode config snapshot: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpFile.Name(), cs.path); err != nil {
		return fmt.Errorf("persist config snapshot: %w", err)
	}

	return nil
}

func cloneAgentConfig(in AgentConfig) AgentConfig {
	return AgentConfig{
		DeviceName: in.DeviceName,
		Env:        in.Env,
		Timezone:   in.Timezone,
		Revision:   in.Revision,
		UpdatedAt:  in.UpdatedAt,
		Batteries:  cloneBatteryConfigs(in.Batteries),
		Schedules:  cloneSchedules(in.Schedules),
	}
}

func cloneBatteryConfigs(in []entity.BatteryConfig) []entity.BatteryConfig {
	if len(in) == 0 {
		return nil
	}

	out := make([]entity.BatteryConfig, len(in))
	copy(out, in)
	return out
}

func cloneSchedules(in []entity.Schedule) []entity.Schedule {
	if len(in) == 0 {
		return nil
	}

	out := make([]entity.Schedule, len(in))
	copy(out, in)
	return out
}

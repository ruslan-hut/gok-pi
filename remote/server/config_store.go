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
	"gok-pi/internal/lib/atomicfile"
)

var (
	// ErrConfigConflict is returned when the provided revision does not match the latest stored revision.
	ErrConfigConflict = errors.New("agent config revision conflict")
	// ErrInvalidSchedules is returned when schedule validation fails.
	ErrInvalidSchedules = errors.New("invalid schedules")
)

// AgentConfig is an alias for entity.AgentConfig to avoid duplicating the struct definition.
type AgentConfig = entity.AgentConfig

// AgentConfigRequest is the payload accepted by the HTTP API when a config is updated.
type AgentConfigRequest struct {
	DeviceName   string                     `json:"device_name,omitempty"`
	Env          string                     `json:"env,omitempty"`
	Timezone     string                     `json:"timezone,omitempty"`
	Revision     int                        `json:"revision"`
	Batteries    []entity.BatteryConfig     `json:"batteries"`
	Schedules    []entity.Schedule          `json:"schedules"`
	EmailReports *entity.EmailReportsConfig `json:"email_reports,omitempty"`
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
		DeviceName:   req.DeviceName,
		Env:          req.Env,
		Timezone:     req.Timezone,
		Revision:     1,
		UpdatedAt:    time.Now().UTC(),
		Batteries:    entity.CloneBatteryConfigs(req.Batteries),
		Schedules:    entity.CloneSchedules(req.Schedules),
		EmailReports: entity.CloneEmailReports(req.EmailReports),
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
		Batteries:  entity.CloneBatteryConfigs(batteries),
		Schedules:  entity.CloneSchedules(schedules),
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

// UpdateAutoSchedules updates auto-prefixed schedules for the given agent using
// add/remove-by-name semantics:
//   - Manual schedules (not "auto-" prefixed) are always preserved
//   - Auto-schedules whose name exists in newAutoSchedules are kept (or updated)
//   - Auto-schedules whose name is NOT in newAutoSchedules are removed (stale)
//   - New auto-schedules not already present are added
//
// This avoids blanket replacement and only changes what actually differs,
// reducing unnecessary config pushes to agents.
func (cs *ConfigStore) UpdateAutoSchedules(agentID string, newAutoSchedules []entity.Schedule) (AgentConfig, bool, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	current, exists := cs.records[agentID]
	if !exists {
		return AgentConfig{}, false, nil
	}

	// Build lookup of new auto-schedule names for O(1) membership checks
	newNames := make(map[string]entity.Schedule, len(newAutoSchedules))
	for _, s := range newAutoSchedules {
		newNames[s.Name] = s
	}

	// Partition current schedules: keep manual, filter auto by membership in new set
	var merged []entity.Schedule
	seen := make(map[string]bool)

	for _, s := range current.Schedules {
		if !entity.IsAutoSchedule(s.Name) {
			// Manual schedule — always keep
			merged = append(merged, s)
			continue
		}
		if _, ok := newNames[s.Name]; ok {
			// Auto-schedule still valid — keep existing (preserves GoalReachedTime etc.)
			merged = append(merged, s)
			seen[s.Name] = true
		}
		// else: stale auto-schedule — drop it
	}

	// Add new auto-schedules not already present
	for _, s := range newAutoSchedules {
		if !seen[s.Name] {
			merged = append(merged, s)
		}
	}

	// Check if schedules actually changed
	if schedulesEqual(current.Schedules, merged) {
		return cloneAgentConfig(current), false, nil
	}

	next := cloneAgentConfig(current)
	next.Schedules = entity.CloneSchedules(merged)
	next.Revision = current.Revision + 1
	next.UpdatedAt = time.Now().UTC()

	cs.records[agentID] = next

	if err := cs.persistLocked(); err != nil {
		cs.records[agentID] = current
		return AgentConfig{}, false, err
	}

	return cloneAgentConfig(next), true, nil
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

// agentIDs returns every agent the store knows about, connected or not.
func (cs *ConfigStore) agentIDs() []string {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	out := make([]string, 0, len(cs.records))
	for id := range cs.records {
		out = append(out, id)
	}
	return out
}

func (cs *ConfigStore) persistLocked() error {
	if cs.path == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(cs.path), 0o755); err != nil {
		return fmt.Errorf("ensure config store dir: %w", err)
	}

	data, err := json.MarshalIndent(cs.records, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config snapshot: %w", err)
	}

	if err := atomicfile.Write(cs.path, data, 0o644); err != nil {
		return fmt.Errorf("persist config snapshot: %w", err)
	}

	return nil
}

func cloneAgentConfig(in AgentConfig) AgentConfig {
	return AgentConfig{
		DeviceName:   in.DeviceName,
		Env:          in.Env,
		Timezone:     in.Timezone,
		Revision:     in.Revision,
		UpdatedAt:    in.UpdatedAt,
		Batteries:    entity.CloneBatteryConfigs(in.Batteries),
		Schedules:    entity.CloneSchedules(in.Schedules),
		EmailReports: entity.CloneEmailReports(in.EmailReports),
	}
}

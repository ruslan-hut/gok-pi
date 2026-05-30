// Package config manages loading, thread-safe access, and persistence of the agent's
// YAML configuration file (config.yml).
//
// It uses a singleton pattern with RWMutex for concurrent access. The config is loaded
// once via MustLoad() and can be updated at runtime by the control server via
// UpdateFromRemoteConfig(). Changes are persisted back to the YAML file via Save().
//
// Environment variables can override YAML values via cleanenv tags (e.g., env:"GOK_ENV").
// If no device_id is set, a random hex ID is generated and persisted on first load.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"gok-pi/battery/entity"
	"gok-pi/internal/lib/atomicfile"
	"log"
	"os"
	"sync"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
	"gopkg.in/yaml.v3"
)

type Config struct {
	DeviceName      string                 `yaml:"device_name" env-default:""`
	DeviceID        string                 `yaml:"device_id" env-default:""`
	Env             string                 `yaml:"env" env-default:"local" env-required:"true"`
	Timezone        string                 `yaml:"timezone" env-default:"UTC"`
	Metrics         MetricsServer          `yaml:"metrics"`
	RemoteControl   RemoteControl          `yaml:"remote_control"`
	Batteries       []entity.BatteryConfig `yaml:"batteries"`
	Schedules       []entity.Schedule      `yaml:"schedules"`
	ChargeSchedules []entity.Schedule      `yaml:"charge_schedules"`
}

type MetricsServer struct {
	Enabled bool   `yaml:"enabled" env-default:"false"`
	Bind    string `yaml:"bind" env-default:"0.0.0.0"`
	Port    string `yaml:"port" env-default:"5001"`
}

type RemoteControl struct {
	Enabled      bool             `yaml:"enabled" env-default:"false"`
	ServerURL    string           `yaml:"server_url" env-default:"wss://localhost:8443/api/agent"`
	SharedSecret string           `yaml:"shared_secret" env-default:""`
	Reconnect    ReconnectBackoff `yaml:"reconnect"`
}

type ReconnectBackoff struct {
	InitialSeconds int `yaml:"initial_seconds" env-default:"5"`
	MaxSeconds     int `yaml:"max_seconds" env-default:"60"`
}

var instance *Config
var instancePath string
var once sync.Once
var mu sync.RWMutex
var saveMu sync.Mutex // serializes marshal+write so concurrent Save() callers cannot race the rename
var goalStateChangedCallback func()
var goalStateCallbackMu sync.RWMutex

func MustLoad(path string) *Config {
	var err error
	once.Do(func() {
		instance = &Config{}
		if err = cleanenv.ReadConfig(path, instance); err != nil {
			desc, _ := cleanenv.GetDescription(instance, nil)
			err = fmt.Errorf("%s; %s", err, desc)
			instance = nil
			log.Fatal(err)
		}
		instancePath = path

		// Generate random device ID if empty
		if instance.DeviceID == "" {
			deviceID, err := generateDeviceID()
			if err != nil {
				log.Fatalf("failed to generate device ID: %v", err)
			}
			instance.DeviceID = deviceID
			// Save the generated ID back to the config file
			if err := Save(); err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: failed to save generated device ID to config file: %v\n", err)
				log.Printf("warning: failed to save generated device ID to config file: %v", err)
			} else {
				fmt.Fprintf(os.Stderr, "INFO: generated and saved device ID: %s\n", deviceID)
				log.Printf("generated device ID: %s", deviceID)
			}
		} else {
			// Log existing device ID for visibility
			fmt.Fprintf(os.Stderr, "INFO: using existing device ID: %s\n", instance.DeviceID)
		}
	})
	return instance
}

// generateDeviceID generates a random 16-character hex-encoded device ID
func generateDeviceID() (string, error) {
	bytes := make([]byte, 8) // 8 bytes = 16 hex characters
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

// UpdateBatteriesAndSchedules updates the batteries and schedules in the config instance.
// This is thread-safe and should be called when remote configuration is received.
func UpdateBatteriesAndSchedules(batteries []entity.BatteryConfig, schedules []entity.Schedule) {
	mu.Lock()
	defer mu.Unlock()
	if instance != nil {
		instance.Batteries = batteries
		instance.Schedules = schedules
	}
}

// SetGoalStateChangedCallback sets a callback that is invoked when goal state changes.
// This allows the agent to push updated config snapshots to the server.
func SetGoalStateChangedCallback(callback func()) {
	goalStateCallbackMu.Lock()
	goalStateChangedCallback = callback
	goalStateCallbackMu.Unlock()
}

func notifyGoalStateChanged() {
	goalStateCallbackMu.RLock()
	cb := goalStateChangedCallback
	goalStateCallbackMu.RUnlock()
	if cb != nil {
		cb()
	}
}

// GetSchedules returns a copy of the current schedules.
// This is thread-safe and can be used to get schedule data for publishing.
func GetSchedules() []entity.Schedule {
	mu.RLock()
	defer mu.RUnlock()
	if instance == nil {
		return nil
	}
	out := make([]entity.Schedule, len(instance.Schedules))
	copy(out, instance.Schedules)
	return out
}

// GetBatteries returns a copy of the current battery configs.
// This is thread-safe and can be used to get battery data for publishing.
func GetBatteries() []entity.BatteryConfig {
	mu.RLock()
	defer mu.RUnlock()
	if instance == nil {
		return nil
	}
	out := make([]entity.BatteryConfig, len(instance.Batteries))
	copy(out, instance.Batteries)
	return out
}

// UpdateFromRemoteConfig updates the config instance with all fields from a remote configuration.
// This includes device_name, env, timezone, batteries, and schedules. This is thread-safe.
// Preserves GoalReachedTime for schedules that still exist with run_once enabled.
func UpdateFromRemoteConfig(deviceName string, env string, timezone string, batteries []entity.BatteryConfig, schedules []entity.Schedule) {
	mu.Lock()
	if instance == nil {
		mu.Unlock()
		return
	}

	// Build map of old schedules to preserve goal state
	oldScheduleMap := make(map[string]entity.Schedule)
	for _, s := range instance.Schedules {
		oldScheduleMap[s.Name] = s
	}

	// Preserve GoalReachedTime for existing schedules, clear if run_once was disabled
	for i := range schedules {
		if oldSchedule, exists := oldScheduleMap[schedules[i].Name]; exists {
			if oldSchedule.GoalReachedTime != nil {
				if schedules[i].RunOnce {
					// Preserve goal state if run_once is still enabled
					schedules[i].GoalReachedTime = oldSchedule.GoalReachedTime
				}
				// If run_once was disabled, GoalReachedTime remains nil (cleared)
			}
		}
	}

	if deviceName != "" {
		instance.DeviceName = deviceName
	}
	if env != "" {
		instance.Env = env
	}
	if timezone != "" {
		instance.Timezone = timezone
	}
	instance.Batteries = batteries
	instance.Schedules = schedules

	mu.Unlock()
}

// Save persists the current config instance to the YAML file it was loaded from.
// Returns an error if the config was not loaded or if writing fails.
// Preserves existing file permissions if the file exists, otherwise uses 0600 (rw-------)
// for security since config files may contain sensitive data like tokens and secrets.
func Save() error {
	// Serialize the whole marshal+write so two concurrent savers cannot interleave
	// their os.Rename calls (last-writer-wins would drop an update).
	saveMu.Lock()
	defer saveMu.Unlock()

	// Deep-copy the config while holding the lock, then release it before I/O.
	// A plain struct copy would alias the slice backing arrays, letting an in-place
	// mutation (e.g. UpdateScheduleGoalReached / RemoveSchedule) race yaml.Marshal.
	mu.RLock()
	if instance == nil {
		mu.RUnlock()
		return fmt.Errorf("config not loaded")
	}
	if instancePath == "" {
		mu.RUnlock()
		return fmt.Errorf("config path not set")
	}

	cfgCopy := *instance
	cfgCopy.Batteries = entity.CloneBatteryConfigs(instance.Batteries)
	cfgCopy.Schedules = entity.CloneSchedules(instance.Schedules)
	cfgCopy.ChargeSchedules = entity.CloneSchedules(instance.ChargeSchedules)
	path := instancePath
	mu.RUnlock()

	data, err := yaml.Marshal(&cfgCopy)
	if err != nil {
		return fmt.Errorf("marshal config to YAML: %w", err)
	}

	// Determine file permissions: preserve existing if file exists, otherwise use 0600
	var fileMode os.FileMode = 0600 // rw------- (secure default for config with sensitive data)
	if info, err := os.Stat(path); err == nil {
		fileMode = info.Mode().Perm() // Preserve existing permissions
	}

	if err := atomicfile.Write(path, data, fileMode); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}

	return nil
}

// mutateSchedule finds a schedule by name under the write lock, applies fn, then notifies and saves.
// The fn receives the index into instance.Schedules so it can modify or splice the slice.
// Return true from fn to stop iterating.
func mutateSchedule(scheduleName string, fn func(i int) bool) error {
	mu.Lock()
	if instance == nil {
		mu.Unlock()
		return fmt.Errorf("config not loaded")
	}
	for i := range instance.Schedules {
		if instance.Schedules[i].Name == scheduleName {
			fn(i)
			break
		}
	}
	mu.Unlock()

	notifyGoalStateChanged()
	return Save()
}

// UpdateScheduleGoalReached updates the goal reached time for a schedule and persists to config file.
func UpdateScheduleGoalReached(scheduleName string, reachedAt time.Time) error {
	return mutateSchedule(scheduleName, func(i int) bool {
		instance.Schedules[i].GoalReachedTime = &reachedAt
		return true
	})
}

// RemoveSchedule removes a schedule by name from the config and persists to config file.
// Used to clean up expired auto-schedules after their time window ends.
func RemoveSchedule(scheduleName string) error {
	return mutateSchedule(scheduleName, func(i int) bool {
		instance.Schedules = append(instance.Schedules[:i], instance.Schedules[i+1:]...)
		return true
	})
}

// ClearScheduleGoalReached clears the goal reached state for a schedule and persists to config file.
func ClearScheduleGoalReached(scheduleName string) error {
	return mutateSchedule(scheduleName, func(i int) bool {
		instance.Schedules[i].GoalReachedTime = nil
		return true
	})
}

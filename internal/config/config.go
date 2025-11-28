package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"gok-pi/battery/entity"
	"log"
	"os"
	"sync"

	"github.com/ilyakaznacheev/cleanenv"
	"gopkg.in/yaml.v3"
)

type Config struct {
	DeviceName      string                 `yaml:"device_name" env-default:""`
	DeviceID        string                 `yaml:"device_id" env-default:""`
	Env             string                 `yaml:"env" env-default:"local" env-required:"true"`
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

// UpdateFromRemoteConfig updates the config instance with all fields from a remote configuration.
// This includes device_name, env, batteries, and schedules. This is thread-safe.
func UpdateFromRemoteConfig(deviceName string, env string, batteries []entity.BatteryConfig, schedules []entity.Schedule) {
	mu.Lock()
	defer mu.Unlock()
	if instance != nil {
		if deviceName != "" {
			instance.DeviceName = deviceName
		}
		if env != "" {
			instance.Env = env
		}
		instance.Batteries = batteries
		instance.Schedules = schedules
	}
}

// Save persists the current config instance to the YAML file it was loaded from.
// Returns an error if the config was not loaded or if writing fails.
// Preserves existing file permissions if the file exists, otherwise uses 0600 (rw-------)
// for security since config files may contain sensitive data like tokens and secrets.
func Save() error {
	// Copy the config and path while holding the lock, then release it before I/O
	mu.RLock()
	if instance == nil {
		mu.RUnlock()
		return fmt.Errorf("config not loaded")
	}
	if instancePath == "" {
		mu.RUnlock()
		return fmt.Errorf("config path not set")
	}

	// Create a copy of the config to marshal outside the lock
	cfgCopy := *instance
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

	// Write to a temporary file first, then rename for atomicity
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, fileMode); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath) // Clean up temp file on error
		return fmt.Errorf("rename config file: %w", err)
	}

	return nil
}

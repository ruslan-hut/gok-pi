// Package driver defines the interface for battery control drivers.
//
// Each battery vendor (Sonnen, SMA, Victron, etc.) implements the Driver interface
// to provide uniform access to battery control operations. Drivers self-register
// via init() functions and are resolved at runtime by name from the config.
package driver

import (
	"fmt"
	"gok-pi/battery/entity"
	"log/slog"
	"sync"
)

// Driver is the interface that all battery control implementations must satisfy.
// It provides status polling, charge/discharge control, and operating mode switching.
type Driver interface {
	Status() (*entity.SystemStatus, error)
	StartDischarge(power int) error
	StopDischarge() error
	StartCharge(power int) error
	StopCharge() error
	SwitchOperatingModeToManual(currentMode string) error
	SwitchOperatingModeToAuto(currentMode string) error
}

// Constructor creates a new Driver from battery configuration.
type Constructor func(cfg entity.BatteryConfig, log *slog.Logger) (Driver, error)

var (
	mu       sync.RWMutex
	registry = make(map[string]Constructor)
)

// Register adds a driver constructor under the given name.
// Typically called from a driver package's init() function.
// When adding a new driver, also add a corresponding option in the Web UI
// driver select (web/ui/src/components/config/BatteryConfigForm.tsx).
func Register(name string, fn Constructor) {
	mu.Lock()
	defer mu.Unlock()
	registry[name] = fn
}

// New creates a Driver for the given driver name using the battery config.
// An empty name defaults to "sonnen" for backward compatibility.
func New(name string, cfg entity.BatteryConfig, log *slog.Logger) (Driver, error) {
	if name == "" {
		name = "sonnen"
	}

	mu.RLock()
	fn, ok := registry[name]
	var names []string
	if !ok {
		names = make([]string, 0, len(registry))
		for n := range registry {
			names = append(names, n)
		}
	}
	mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown battery driver %q; available: %v", name, names)
	}

	return fn(cfg, log)
}

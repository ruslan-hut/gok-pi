package controller

import "sync"

// ModeCoordinator serializes operating-mode ownership for a single battery that is
// shared by the charge and discharge controllers. Each direction acquires manual
// ownership when it starts an operation and releases it when it stops. The battery
// is switched back to automatic mode only once no direction holds manual ownership,
// which prevents the two controllers from oscillating the operating mode when their
// schedules are adjacent or overlapping.
type ModeCoordinator struct {
	mu     sync.Mutex
	active map[string]bool
}

// NewModeCoordinator returns a coordinator with no active directions.
func NewModeCoordinator() *ModeCoordinator {
	return &ModeCoordinator{active: make(map[string]bool)}
}

// AcquireManual switches the battery to manual mode and records dir as active.
// switchToManual is invoked under the coordinator lock; if it fails the direction
// is not marked active and the error is returned.
func (m *ModeCoordinator) AcquireManual(dir string, switchToManual func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := switchToManual(); err != nil {
		return err
	}
	m.active[dir] = true
	return nil
}

// ReleaseToAuto records dir as inactive and switches the battery back to automatic
// mode only if no other direction still holds manual ownership.
func (m *ModeCoordinator) ReleaseToAuto(dir string, switchToAuto func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.active, dir)
	if len(m.active) > 0 {
		return nil
	}
	return switchToAuto()
}

// MarkActive records dir as active without changing the operating mode. It is used
// on startup when a controller adopts a battery that is already operating in manual
// mode, so the coordinator's view matches reality.
func (m *ModeCoordinator) MarkActive(dir string) {
	m.mu.Lock()
	m.active[dir] = true
	m.mu.Unlock()
}

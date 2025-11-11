package observers

import (
	"sync"
	"sync/atomic"
	"time"
)

type Snapshot struct {
	Name                  string    `json:"name"`
	RSOC                  float64   `json:"rsoc"`
	USOC                  float64   `json:"usoc"`
	RemainingCapacityWh   float64   `json:"remaining_capacity_wh"`
	ConsumptionW          float64   `json:"consumption_w"`
	PacTotalW             float64   `json:"pac_total_w"`
	BatteryDischarging    bool      `json:"battery_discharging"`
	BatteryDischargingSet bool      `json:"battery_discharging_set"`
	OperatingMode         string    `json:"operating_mode"`
	OperatingModeSet      bool      `json:"operating_mode_set"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type Listener func(Snapshot)

var (
	stateMu   sync.RWMutex
	snapshots = make(map[string]*Snapshot)

	listenerMu sync.RWMutex
	listeners  = make(map[int64]Listener)
	nextID     int64
)

func RegisterListener(listener Listener) (cancel func()) {
	id := atomic.AddInt64(&nextID, 1)

	listenerMu.Lock()
	listeners[id] = listener
	listenerMu.Unlock()

	return func() {
		listenerMu.Lock()
		delete(listeners, id)
		listenerMu.Unlock()
	}
}

func GetSnapshot(name string) (Snapshot, bool) {
	stateMu.RLock()
	defer stateMu.RUnlock()

	snap, ok := snapshots[name]
	if !ok {
		return Snapshot{}, false
	}
	return *snap, true
}

func GetSnapshots() []Snapshot {
	stateMu.RLock()
	defer stateMu.RUnlock()

	result := make([]Snapshot, 0, len(snapshots))
	for _, snap := range snapshots {
		result = append(result, *snap)
	}
	return result
}

func updateSnapshot(name string, apply func(*Snapshot)) {
	stateMu.Lock()
	snap, ok := snapshots[name]
	if !ok {
		snap = &Snapshot{Name: name}
		snapshots[name] = snap
	}
	apply(snap)
	snap.UpdatedAt = time.Now()
	copy := *snap
	stateMu.Unlock()

	notifyListeners(copy)
}

func notifyListeners(snapshot Snapshot) {
	listenerMu.RLock()
	defer listenerMu.RUnlock()

	for _, listener := range listeners {
		listener(snapshot)
	}
}

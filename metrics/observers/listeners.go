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
	BatteryCharging       bool      `json:"battery_charging"`
	BatteryChargingSet    bool      `json:"battery_charging_set"`
	OperatingMode         string    `json:"operating_mode"`
	OperatingModeSet      bool      `json:"operating_mode_set"`
	Status                string    `json:"status"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type Listener func(Snapshot)

var (
	stateMu   sync.RWMutex
	snapshots = make(map[string]*Snapshot)

	listenerMu sync.RWMutex
	listeners  = make(map[int64]Listener)
	nextID     int64

	// connFailMu guards connFailed, the set of batteries currently in a logged
	// failure state. Several pollers (the discharge/charge controllers and the
	// standalone monitor) poll the same battery on independent tickers, so this
	// is keyed by battery name and shared across all of them.
	connFailMu sync.Mutex
	connFailed = make(map[string]bool)
)

// ConnectionFailed records a battery poll failure and reports whether this is a
// new failure, i.e. a transition from healthy to failed. It returns true exactly
// once per outage — for the first poller to observe it — and false while the
// battery stays continuously unreachable. Callers use the return value to log a
// connection error once per outage instead of once per poll per poller.
func ConnectionFailed(name string) bool {
	connFailMu.Lock()
	defer connFailMu.Unlock()
	if connFailed[name] {
		return false
	}
	connFailed[name] = true
	return true
}

// ConnectionRecovered clears the failure state for a battery and reports whether
// it was previously marked failed, i.e. whether this is a genuine recovery
// transition. It returns true exactly once per recovery, for the first poller to
// observe the battery responding again.
func ConnectionRecovered(name string) bool {
	connFailMu.Lock()
	defer connFailMu.Unlock()
	if !connFailed[name] {
		return false
	}
	delete(connFailed, name)
	return true
}

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

// notifyListeners sends a snapshot copy to all registered listeners. Listeners are
// copied under the lock and invoked after releasing it, so a slow listener (one does
// network I/O) cannot stall every metric update, and a listener that re-enters
// RegisterListener cannot deadlock.
func notifyListeners(snapshot Snapshot) {
	listenerMu.RLock()
	fns := make([]Listener, 0, len(listeners))
	for _, listener := range listeners {
		fns = append(fns, listener)
	}
	listenerMu.RUnlock()

	for _, listener := range fns {
		listener(snapshot)
	}
}

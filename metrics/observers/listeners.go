package observers

import (
	"sync"
	"sync/atomic"
	"time"
)

type Snapshot struct {
	Name                  string  `json:"name"`
	RSOC                  float64 `json:"rsoc"`
	USOC                  float64 `json:"usoc"`
	RemainingCapacityWh   float64 `json:"remaining_capacity_wh"`
	ConsumptionW          float64 `json:"consumption_w"`
	PacTotalW             float64 `json:"pac_total_w"`
	BatteryDischarging    bool    `json:"battery_discharging"`
	BatteryDischargingSet bool    `json:"battery_discharging_set"`
	BatteryCharging       bool    `json:"battery_charging"`
	BatteryChargingSet    bool    `json:"battery_charging_set"`
	OperatingMode         string  `json:"operating_mode"`
	OperatingModeSet      bool    `json:"operating_mode_set"`
	Status                string  `json:"status"`
	// OverrideSource says why the battery is running outside its schedule:
	// "manual" for an operator command, "charger" for an EV charging session,
	// empty when the schedules are in charge. OverrideDirection is the direction
	// that set it, so the opposite-direction controller for the same battery
	// cannot clear an override it does not own.
	OverrideSource    string    `json:"override_source,omitempty"`
	OverrideDirection string    `json:"override_direction,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Listener func(Snapshot)

var (
	stateMu   sync.RWMutex
	snapshots = make(map[string]*Snapshot)

	listenerMu sync.RWMutex
	listeners  = make(map[int64]Listener)
	nextID     int64

	// connFailMu guards connFailed, the per-battery poll failure state. Several
	// pollers (the discharge/charge controllers and the standalone monitor) poll
	// the same battery on independent tickers, so this is keyed by battery name
	// and shared across all of them.
	connFailMu sync.Mutex
	connFailed = make(map[string]*failureState)
)

// FailureLogInterval is how often a battery that stays unreachable is re-logged.
// Logging only the healthy->failed transition makes a permanent outage
// indistinguishable from a blip that recovered seconds later: both leave exactly
// one line. Re-logging on this interval keeps a sustained outage visible in the
// log without emitting a line per poll.
const FailureLogInterval = 5 * time.Minute

type failureState struct {
	consecutive int
	lastLogged  time.Time
}

// ConnectionFailed records a battery poll failure and reports whether the caller
// should log it, along with the number of consecutive failures so far (1 on the
// first). It returns true for the healthy->failed transition and then again at
// most once per FailureLogInterval while the battery stays unreachable, so a
// sustained outage keeps producing evidence instead of going silent after one line.
func ConnectionFailed(name string) (shouldLog bool, consecutive int) {
	connFailMu.Lock()
	defer connFailMu.Unlock()

	state, ok := connFailed[name]
	if !ok {
		connFailed[name] = &failureState{consecutive: 1, lastLogged: time.Now()}
		return true, 1
	}

	state.consecutive++
	if time.Since(state.lastLogged) >= FailureLogInterval {
		state.lastLogged = time.Now()
		return true, state.consecutive
	}
	return false, state.consecutive
}

// ConnectionRecovered clears the failure state for a battery and reports whether
// it was previously marked failed, i.e. whether this is a genuine recovery
// transition, along with how many consecutive failures preceded the recovery.
// It returns true exactly once per recovery, for the first poller to observe the
// battery responding again.
func ConnectionRecovered(name string) (recovered bool, failures int) {
	connFailMu.Lock()
	defer connFailMu.Unlock()
	state, ok := connFailed[name]
	if !ok {
		return false, 0
	}
	delete(connFailed, name)
	return true, state.consecutive
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

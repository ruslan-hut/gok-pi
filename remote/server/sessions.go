// sessions.go tracks charge/discharge energy transfer sessions by observing telemetry.
//
// A session starts when a battery transitions to charging or discharging state,
// and ends when the state changes back. During a session, energy (Wh) is accumulated
// from power samples, and statistics (peak power, average power, SoC start/end,
// electricity price) are recorded. Sessions are persisted to SQLite via sessiondb.
package server

import (
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"gok-pi/electricity/pricefetcher"
	"gok-pi/remote/server/sessiondb"
)

// batteryState tracks the last known charge/discharge state for edge detection.
type batteryState struct {
	charging      bool
	discharging   bool
	initialized   bool
	operatingMode string // normalized: "manual" or "auto"
}

// activeSession tracks in-flight telemetry accumulation for an open session.
type activeSession struct {
	dbID       int64
	startedAt  time.Time
	lastSample time.Time
	energyWh   float64 // accumulated energy (Wh)
	powerSum   float64 // sum of power samples (W) for averaging
	peakPowerW float64
	socEnd     float64
	samples    int
	priceSum   float64 // sum of hourly prices seen
	priceHours int     // distinct hours counted
}

// SessionTracker watches telemetry, records charge/discharge sessions to the database,
// and accumulates energy from telemetry power samples.
type SessionTracker struct {
	log    *slog.Logger
	store  *sessiondb.Store
	prices *pricefetcher.Fetcher

	mu     sync.Mutex
	states map[string]*batteryState  // key: "agentID:batteryName"
	active map[string]*activeSession // key: "agentID:batteryName:type"
}

func NewSessionTracker(log *slog.Logger, store *sessiondb.Store, prices *pricefetcher.Fetcher) *SessionTracker {
	st := &SessionTracker{
		log:    log.With(slog.String("component", "session-tracker")),
		store:  store,
		prices: prices,
		states: make(map[string]*batteryState),
		active: make(map[string]*activeSession),
	}
	st.recoverOpenSessions()
	return st
}

// recoverOpenSessions restores active sessions from the database after restart.
func (st *SessionTracker) recoverOpenSessions() {
	open, err := st.store.GetOpenSessions()
	if err != nil {
		st.log.Warn("failed to recover open sessions", slog.Any("error", err))
		return
	}
	for _, rec := range open {
		key := rec.AgentID + ":" + rec.BatteryName + ":" + rec.Type
		st.active[key] = &activeSession{
			dbID:       rec.ID,
			startedAt:  rec.StartedAt,
			lastSample: rec.StartedAt,
			energyWh:   rec.EnergyWh,
			powerSum:   rec.AvgPowerW * float64(rec.Samples),
			peakPowerW: rec.PeakPowerW,
			socEnd:     rec.SocEnd,
			samples:    rec.Samples,
		}
		// Initialize state so we don't re-create the session
		stateKey := rec.AgentID + ":" + rec.BatteryName
		state, ok := st.states[stateKey]
		if !ok {
			state = &batteryState{initialized: true}
			st.states[stateKey] = state
		}
		switch rec.Type {
		case "charge":
			state.charging = true
		case "discharge":
			state.discharging = true
		}
		st.log.Info("recovered open session", slog.String("battery", rec.BatteryName), slog.String("type", rec.Type), slog.Int64("id", rec.ID))
	}
}

// OnTelemetry processes a telemetry update: detects session transitions and accumulates energy.
// Only manual-mode sessions are tracked; auto-mode activity is ignored.
func (st *SessionTracker) OnTelemetry(agentID string, snapshot TelemetrySnapshot) {
	if snapshot.Name == "" {
		return
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	stateKey := agentID + ":" + snapshot.Name
	prev, exists := st.states[stateKey]
	if !exists {
		prev = &batteryState{}
		st.states[stateKey] = prev
	}

	now := time.Now().UTC()

	// Update operating mode tracking
	if snapshot.OperatingModeSet {
		newMode := normalizeOperatingMode(snapshot.OperatingMode)
		oldMode := prev.operatingMode
		prev.operatingMode = newMode

		// If mode changed away from manual, close any active sessions
		if oldMode == "manual" && newMode != "manual" {
			for _, suffix := range []string{":charge", ":discharge"} {
				activeKey := stateKey + suffix
				if _, ok := st.active[activeKey]; ok {
					st.log.Info("mode changed from manual, closing session",
						slog.String("battery", snapshot.Name),
						slog.String("type", suffix[1:]),
						slog.String("new_mode", newMode),
					)
					st.endSession(activeKey, now, snapshot.USOC)
				}
			}
		}

		// If mode changed TO manual, start sessions for already-active states.
		// This handles the race where BatteryDischargingSet/BatteryChargingSet
		// arrived before OperatingModeSet on the first telemetry cycle.
		if oldMode != "manual" && newMode == "manual" && prev.initialized {
			if prev.discharging {
				activeKey := stateKey + ":discharge"
				if _, ok := st.active[activeKey]; !ok {
					st.startSession(activeKey, agentID, snapshot.Name, "discharge", now, snapshot.USOC)
				}
			}
			if prev.charging {
				activeKey := stateKey + ":charge"
				if _, ok := st.active[activeKey]; !ok {
					st.startSession(activeKey, agentID, snapshot.Name, "charge", now, snapshot.USOC)
				}
			}
		}
	}

	isManual := prev.operatingMode == "manual"

	// Process charging transitions
	if snapshot.BatteryChargingSet {
		activeKey := stateKey + ":charge"
		if !prev.initialized {
			prev.charging = snapshot.BatteryCharging
			if snapshot.BatteryCharging && isManual {
				st.startSession(activeKey, agentID, snapshot.Name, "charge", now, snapshot.USOC)
			}
		} else {
			if !prev.charging && snapshot.BatteryCharging {
				if isManual {
					st.startSession(activeKey, agentID, snapshot.Name, "charge", now, snapshot.USOC)
				}
			} else if prev.charging && !snapshot.BatteryCharging {
				st.endSession(activeKey, now, snapshot.USOC)
			}
			prev.charging = snapshot.BatteryCharging
		}
	}

	// Process discharging transitions
	if snapshot.BatteryDischargingSet {
		activeKey := stateKey + ":discharge"
		if !prev.initialized {
			prev.discharging = snapshot.BatteryDischarging
			if snapshot.BatteryDischarging && isManual {
				st.startSession(activeKey, agentID, snapshot.Name, "discharge", now, snapshot.USOC)
			}
		} else {
			if !prev.discharging && snapshot.BatteryDischarging {
				if isManual {
					st.startSession(activeKey, agentID, snapshot.Name, "discharge", now, snapshot.USOC)
				}
			} else if prev.discharging && !snapshot.BatteryDischarging {
				st.endSession(activeKey, now, snapshot.USOC)
			}
			prev.discharging = snapshot.BatteryDischarging
		}
	}

	if snapshot.BatteryChargingSet || snapshot.BatteryDischargingSet {
		prev.initialized = true
	}

	// Accumulate energy for active sessions of this battery
	st.accumulateEnergy(stateKey+":charge", snapshot.PacTotalW, snapshot.USOC, now)
	st.accumulateEnergy(stateKey+":discharge", snapshot.PacTotalW, snapshot.USOC, now)
}

func (st *SessionTracker) startSession(key, agentID, batteryName, sessionType string, now time.Time, soc float64) {
	rec := &sessiondb.SessionRecord{
		AgentID:     agentID,
		BatteryName: batteryName,
		Type:        sessionType,
		StartedAt:   now,
		SocStart:    soc,
	}
	id, err := st.store.InsertSession(rec)
	if err != nil {
		st.log.Warn("failed to insert session", slog.Any("error", err), slog.String("battery", batteryName))
		return
	}
	st.active[key] = &activeSession{
		dbID:       id,
		startedAt:  now,
		lastSample: now,
		socEnd:     soc,
	}
	st.log.Debug("session started", slog.String("type", sessionType), slog.String("battery", batteryName), slog.Int64("id", id))
}

func (st *SessionTracker) endSession(key string, now time.Time, soc float64) {
	sess, ok := st.active[key]
	if !ok {
		return
	}

	sess.socEnd = soc
	duration := now.Sub(sess.startedAt).Seconds()
	avgPower := 0.0
	if sess.samples > 0 {
		avgPower = sess.powerSum / float64(sess.samples)
	}

	avgPrice := st.getAvgPrice(sess.startedAt, now)
	costEur := sess.energyWh / 1e6 * avgPrice // energy_Wh / 1e6 * EUR/MWh = EUR
	// Signed cost: charge = negative (expense), discharge = positive (income)
	if strings.HasSuffix(key, ":charge") {
		costEur = -costEur
	}

	if err := st.store.CloseSession(
		sess.dbID, now, duration,
		sess.energyWh, avgPower, sess.peakPowerW,
		sess.socEnd, avgPrice, costEur, sess.samples,
	); err != nil {
		st.log.Warn("failed to close session", slog.Any("error", err), slog.Int64("id", sess.dbID))
	}

	st.log.Debug("session ended",
		slog.Int64("id", sess.dbID),
		slog.Float64("energy_wh", sess.energyWh),
		slog.Float64("cost_eur", costEur),
		slog.Float64("duration_s", duration),
	)
	delete(st.active, key)
}

func (st *SessionTracker) accumulateEnergy(key string, pacTotalW, soc float64, now time.Time) {
	sess, ok := st.active[key]
	if !ok {
		return
	}

	dt := now.Sub(sess.lastSample).Seconds()
	if dt <= 0 || dt > 120 { // skip if gap too large (>2min = likely reconnection)
		sess.lastSample = now
		return
	}

	// PacTotalW: positive = discharge, negative = charge
	power := math.Abs(pacTotalW)
	energyIncrement := power * dt / 3600.0 // Wh = W * s / 3600

	sess.energyWh += energyIncrement
	sess.powerSum += power
	sess.samples++
	if power > sess.peakPowerW {
		sess.peakPowerW = power
	}
	sess.socEnd = soc
	sess.lastSample = now

	// Periodically flush to DB (every 30 samples ≈ 5 min at 10s interval)
	if sess.samples%30 == 0 {
		avgPower := sess.powerSum / float64(sess.samples)
		avgPrice := st.getAvgPrice(sess.startedAt, now)
		costEur := sess.energyWh / 1e6 * avgPrice
		if strings.HasSuffix(key, ":charge") {
			costEur = -costEur
		}
		if err := st.store.UpdateSession(sess.dbID, sess.energyWh, avgPower, sess.peakPowerW, sess.socEnd, avgPrice, costEur, sess.samples); err != nil {
			st.log.Warn("failed to flush session", slog.Any("error", err), slog.Int64("id", sess.dbID))
		}
	}
}

// getAvgPrice looks up the average electricity price for the hours spanned by a session.
func (st *SessionTracker) getAvgPrice(start, end time.Time) float64 {
	state := st.prices.GetState()
	if state.Today == nil {
		return 0
	}

	startHour := start.Hour()
	endHour := end.Hour()
	if end.Minute() > 0 || end.Second() > 0 {
		endHour++
	}
	if endHour > 24 {
		endHour = 24
	}

	var sum float64
	var count int
	for _, p := range state.Today.Prices {
		if p.Hour >= startHour && p.Hour < endHour {
			sum += p.Price
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

// GetSummariesRange returns per-battery aggregated data for a time range.
func (st *SessionTracker) GetSummariesRange(agentID string, since, until time.Time) ([]sessiondb.BatterySummary, error) {
	summaries, err := st.store.GetSummariesRange(agentID, since, until)
	if err != nil {
		return nil, err
	}
	return st.enrichWithActiveSessions(summaries, agentID), nil
}

// GetSummaries returns per-battery aggregated data.
func (st *SessionTracker) GetSummaries(agentID string, hours int) ([]sessiondb.BatterySummary, error) {
	summaries, err := st.store.GetSummaries(agentID, hours)
	if err != nil {
		return nil, err
	}
	return st.enrichWithActiveSessions(summaries, agentID), nil
}

// enrichWithActiveSessions marks active charge/discharge flags on summaries
// and adds entries for batteries that only have active sessions (not yet in DB).
func (st *SessionTracker) enrichWithActiveSessions(summaries []sessiondb.BatterySummary, agentID string) []sessiondb.BatterySummary {
	st.mu.Lock()
	activeByBattery := make(map[string]map[string]bool)
	for k := range st.active {
		parts := splitSessionKey(k)
		if parts == nil {
			continue
		}
		if agentID != "" && parts[0] != agentID {
			continue
		}
		if _, ok := activeByBattery[parts[1]]; !ok {
			activeByBattery[parts[1]] = make(map[string]bool)
		}
		activeByBattery[parts[1]][parts[2]] = true
	}
	st.mu.Unlock()

	for i := range summaries {
		if types, ok := activeByBattery[summaries[i].BatteryName]; ok {
			summaries[i].ActiveCharge = types["charge"]
			summaries[i].ActiveDischarge = types["discharge"]
		}
	}

	existing := make(map[string]bool)
	for _, s := range summaries {
		existing[s.BatteryName] = true
	}
	for battery, types := range activeByBattery {
		if !existing[battery] {
			summaries = append(summaries, sessiondb.BatterySummary{
				BatteryName:     battery,
				ActiveCharge:    types["charge"],
				ActiveDischarge: types["discharge"],
			})
		}
	}

	return summaries
}

// GetRecentSessions returns sessions from the last N hours.
func (st *SessionTracker) GetRecentSessions(agentID string, hours int) ([]sessiondb.SessionRecord, error) {
	return st.store.GetRecentSessions(agentID, hours)
}

// Store returns the underlying database store for direct queries.
func (st *SessionTracker) Store() *sessiondb.Store {
	return st.store
}

// Cleanup removes old sessions beyond retention.
func (st *SessionTracker) Cleanup(retention time.Duration) {
	deleted, err := st.store.Cleanup(retention)
	if err != nil {
		st.log.Warn("session cleanup failed", slog.Any("error", err))
		return
	}
	if deleted > 0 {
		st.log.Info("session cleanup", slog.Int64("deleted", deleted))
	}
}

// normalizeOperatingMode converts the Sonnen API numeric operating mode
// ("1" = manual, "2" = auto) to a human-readable string.
func normalizeOperatingMode(raw string) string {
	switch raw {
	case "1", "manual":
		return "manual"
	case "2", "auto":
		return "auto"
	default:
		return raw
	}
}

func splitSessionKey(key string) []string {
	// key format: "agentID:batteryName:type"
	// Find last colon for type, then split the rest
	lastColon := -1
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == ':' {
			lastColon = i
			break
		}
	}
	if lastColon < 0 {
		return nil
	}
	typ := key[lastColon+1:]
	rest := key[:lastColon]

	firstColon := -1
	for i := 0; i < len(rest); i++ {
		if rest[i] == ':' {
			firstColon = i
			break
		}
	}
	if firstColon < 0 {
		return nil
	}
	return []string{rest[:firstColon], rest[firstColon+1:], typ}
}

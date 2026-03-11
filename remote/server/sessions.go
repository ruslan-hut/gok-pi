package server

import (
	"log/slog"
	"sync"
	"time"
)

// Session represents a recorded charge or discharge event.
type Session struct {
	BatteryName string     `json:"battery_name"`
	AgentID     string     `json:"agent_id"`
	Type        string     `json:"type"` // "charge" or "discharge"
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	DurationSec float64    `json:"duration_seconds,omitempty"`
}

type batteryState struct {
	charging    bool
	discharging bool
	initialized bool
}

// SessionTracker watches telemetry and records charge/discharge sessions.
type SessionTracker struct {
	log     *slog.Logger
	mu      sync.RWMutex
	states  map[string]*batteryState // key: "agentID:batteryName"
	active  map[string]*Session      // key: "agentID:batteryName:type"
	history []Session
}

func NewSessionTracker(log *slog.Logger) *SessionTracker {
	return &SessionTracker{
		log:    log.With(slog.String("component", "session-tracker")),
		states: make(map[string]*batteryState),
		active: make(map[string]*Session),
	}
}

// OnTelemetry processes a telemetry update and detects session transitions.
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

	// Process charging transitions (only when the field was explicitly set)
	if snapshot.BatteryChargingSet {
		if !prev.initialized {
			// First telemetry with this field — just record state, don't create session
			prev.charging = snapshot.BatteryCharging
		} else {
			activeKey := stateKey + ":charge"
			if !prev.charging && snapshot.BatteryCharging {
				// Started charging
				st.active[activeKey] = &Session{
					BatteryName: snapshot.Name,
					AgentID:     agentID,
					Type:        "charge",
					StartedAt:   now,
				}
				st.log.Debug("charge session started", slog.String("battery", snapshot.Name), slog.String("agent", agentID))
			} else if prev.charging && !snapshot.BatteryCharging {
				// Stopped charging
				if sess, ok := st.active[activeKey]; ok {
					ended := now
					sess.EndedAt = &ended
					sess.DurationSec = ended.Sub(sess.StartedAt).Seconds()
					st.history = append(st.history, *sess)
					delete(st.active, activeKey)
					st.log.Debug("charge session ended", slog.String("battery", snapshot.Name), slog.Float64("duration_s", sess.DurationSec))
				}
			}
			prev.charging = snapshot.BatteryCharging
		}
	}

	// Process discharging transitions
	if snapshot.BatteryDischargingSet {
		if !prev.initialized {
			prev.discharging = snapshot.BatteryDischarging
		} else {
			activeKey := stateKey + ":discharge"
			if !prev.discharging && snapshot.BatteryDischarging {
				st.active[activeKey] = &Session{
					BatteryName: snapshot.Name,
					AgentID:     agentID,
					Type:        "discharge",
					StartedAt:   now,
				}
				st.log.Debug("discharge session started", slog.String("battery", snapshot.Name), slog.String("agent", agentID))
			} else if prev.discharging && !snapshot.BatteryDischarging {
				if sess, ok := st.active[activeKey]; ok {
					ended := now
					sess.EndedAt = &ended
					sess.DurationSec = ended.Sub(sess.StartedAt).Seconds()
					st.history = append(st.history, *sess)
					delete(st.active, activeKey)
					st.log.Debug("discharge session ended", slog.String("battery", snapshot.Name), slog.Float64("duration_s", sess.DurationSec))
				}
			}
			prev.discharging = snapshot.BatteryDischarging
		}
	}

	// Mark as initialized after first telemetry with any Set field
	if snapshot.BatteryChargingSet || snapshot.BatteryDischargingSet {
		prev.initialized = true
	}
}

// GetSessions returns active and completed sessions for the given agent (or all if agentID is empty).
// Results are limited to the last 48 hours.
func (st *SessionTracker) GetSessions(agentID string) []Session {
	st.mu.RLock()
	defer st.mu.RUnlock()

	cutoff := time.Now().UTC().Add(-48 * time.Hour)
	var result []Session

	// Active sessions
	for _, sess := range st.active {
		if agentID != "" && sess.AgentID != agentID {
			continue
		}
		result = append(result, *sess)
	}

	// Completed sessions (within 48h)
	for _, sess := range st.history {
		if sess.StartedAt.Before(cutoff) {
			continue
		}
		if agentID != "" && sess.AgentID != agentID {
			continue
		}
		result = append(result, sess)
	}

	return result
}

// Cleanup removes completed sessions older than 48 hours.
func (st *SessionTracker) Cleanup() {
	st.mu.Lock()
	defer st.mu.Unlock()

	cutoff := time.Now().UTC().Add(-48 * time.Hour)
	filtered := st.history[:0]
	for _, sess := range st.history {
		if !sess.StartedAt.Before(cutoff) {
			filtered = append(filtered, sess)
		}
	}
	st.history = filtered
}

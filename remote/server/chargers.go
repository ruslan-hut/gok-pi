// chargers.go implements the inbound integration with the evsys EV charging
// central system.
//
//	evsys transaction.start ──POST /api/webhooks/evsys──▶ control server
//	                                                          │
//	                                        agent.command start_discharge
//	                                                          ▼
//	                                                   agent ──▶ battery
//
// When a customer starts a charging session, the linked battery is switched to
// discharge so the car draws stored energy instead of grid power; when the
// session stops, the battery is released back to its normal schedule.
//
// evsys delivers events at-least-once with retries for 24h (see evsys
// docs/WEBHOOKS.md), so every handler here is idempotent: the active session map
// is the source of truth, a repeated start for a known session is a no-op, and a
// stop for an unknown session is a no-op.

package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gok-pi/internal/lib/atomicfile"
)

// evsys event types we act on. Other types are acknowledged and ignored.
const (
	evsysTransactionStart = "transaction.start"
	evsysTransactionStop  = "transaction.stop"
)

// Wire commands sent to the agent. These match the constants in cmd/gok/main.go.
const (
	chargerCmdStartDischarge = "start_discharge"
	chargerCmdStopDischarge  = "stop_discharge"
)

// commandSourceCharger marks a discharge as driven by an EV charging session.
// The agent-side controller keeps an override with this source alive across
// config pushes, which would otherwise cancel it mid-session.
const commandSourceCharger = "charger"

// chargerSweepInterval is how often expired sessions are swept. It bounds how
// long a battery keeps discharging if a transaction.stop never arrives.
const chargerSweepInterval = time.Minute

// evsysEnvelope is the outer webhook message from evsys (webhook/envelope.go).
type evsysEnvelope struct {
	Id       string          `json:"id"`
	Type     string          `json:"type"`
	Source   string          `json:"source"`
	Time     time.Time       `json:"time"`
	Sequence int64           `json:"sequence"`
	Data     *evsysEventData `json:"data"`
}

// evsysEventData is the event payload (evsys internal/event_handler.go).
//
// Every field there is tagged omitempty, so zero values are absent from the wire
// rather than serialized: connector_id 0 and consumed 0 simply do not appear.
// location_id is also absent on OCPP 2.0.1 events, which is why ChargerLink
// supports charge point ids as a fallback match key.
type evsysEventData struct {
	ChargePointId string    `json:"charge_point_id"`
	ConnectorId   int       `json:"connector_id"`
	LocationId    string    `json:"location_id"`
	Evse          string    `json:"evse"`
	TransactionId int       `json:"transaction_id"`
	Username      string    `json:"username"`
	IdTag         string    `json:"id_tag"`
	Consumed      int       `json:"consumed"`
	Status        string    `json:"status"`
	Time          time.Time `json:"time"`
}

// ChargerSession is one active EV charging session driving a battery discharge.
type ChargerSession struct {
	Key            string    `json:"key"`
	LinkName       string    `json:"link_name"`
	AgentId        string    `json:"agent_id"`
	BatteryName    string    `json:"battery_name"`
	LocationId     string    `json:"location_id"`
	ChargePointId  string    `json:"charge_point_id"`
	ConnectorId    int       `json:"connector_id"`
	TransactionId  int       `json:"transaction_id"`
	IdTag          string    `json:"id_tag"`
	Username       string    `json:"username"`
	StartedAt      time.Time `json:"started_at"`
	PowerLimit     int       `json:"power_limit"`
	SocLimit       int       `json:"soc_limit"`
	MaxDurationMin int       `json:"max_duration_min"`
}

// expired reports whether the session has outlived its safety cap. A cap of 0
// means no cap.
func (s ChargerSession) expired(now time.Time) bool {
	if s.MaxDurationMin <= 0 {
		return false
	}
	return now.Sub(s.StartedAt) > time.Duration(s.MaxDurationMin)*time.Minute
}

// chargerStartPayload is the command payload sent with start_discharge.
type chargerStartPayload struct {
	Power      int    `json:"power"`
	PowerLimit int    `json:"power_limit"`
	SocLimit   int    `json:"soc_limit"`
	Source     string `json:"source"`
}

// chargerSessionStore keeps the active sessions, persisted to disk so that a
// control server restart cannot leave a battery discharging with nobody left to
// stop it.
type chargerSessionStore struct {
	mu       sync.RWMutex
	sessions map[string]ChargerSession
	path     string
}

func newChargerSessionStore(path string) *chargerSessionStore {
	s := &chargerSessionStore{
		sessions: make(map[string]ChargerSession),
		path:     path,
	}
	if path != "" {
		s.load()
	}
	return s
}

func (s *chargerSessionStore) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var list []ChargerSession
	if err := json.Unmarshal(data, &list); err != nil {
		return
	}
	for _, ses := range list {
		if ses.Key == "" {
			continue
		}
		s.sessions[ses.Key] = ses
	}
}

// persistLocked writes the current sessions to disk. Callers must hold the lock.
func (s *chargerSessionStore) persistLocked() error {
	if s.path == "" {
		return nil
	}
	list := make([]ChargerSession, 0, len(s.sessions))
	for _, ses := range s.sessions {
		list = append(list, ses)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Key < list[j].Key })

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, data, 0o644)
}

// Add records a session. It reports false if a session with the same key is
// already active, which is how duplicate webhook deliveries are absorbed.
func (s *chargerSessionStore) Add(ses ChargerSession) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.sessions[ses.Key]; exists {
		return false, nil
	}
	s.sessions[ses.Key] = ses
	return true, s.persistLocked()
}

// Remove drops a session. It reports false if the session was not active, which
// absorbs stop events for sessions we never saw start.
func (s *chargerSessionStore) Remove(key string) (ChargerSession, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ses, exists := s.sessions[key]
	if !exists {
		return ChargerSession{}, false, nil
	}
	delete(s.sessions, key)
	return ses, true, s.persistLocked()
}

// RemoveExpired drops every session past its safety cap and returns them.
func (s *chargerSessionStore) RemoveExpired(now time.Time) []ChargerSession {
	s.mu.Lock()
	defer s.mu.Unlock()

	var expired []ChargerSession
	for key, ses := range s.sessions {
		if ses.expired(now) {
			expired = append(expired, ses)
			delete(s.sessions, key)
		}
	}
	if len(expired) > 0 {
		_ = s.persistLocked()
	}
	return expired
}

// ForBattery returns the active sessions targeting one battery on one agent.
func (s *chargerSessionStore) ForBattery(agentID, battery string) []ChargerSession {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []ChargerSession
	for _, ses := range s.sessions {
		if ses.AgentId == agentID && ses.BatteryName == battery {
			result = append(result, ses)
		}
	}
	return result
}

// BatteriesForAgent returns the distinct batteries of one agent that currently
// have at least one active session.
func (s *chargerSessionStore) BatteriesForAgent(agentID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	seen := make(map[string]struct{})
	for _, ses := range s.sessions {
		if ses.AgentId == agentID {
			seen[ses.BatteryName] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

// List returns all active sessions, ordered by start time.
func (s *chargerSessionStore) List() []ChargerSession {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]ChargerSession, 0, len(s.sessions))
	for _, ses := range s.sessions {
		list = append(list, ses)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].StartedAt.Before(list[j].StartedAt) })
	return list
}

// chargerSessionKey identifies a charging session. The transaction id is the
// natural key, but it is omitempty on the wire, so a zero id falls back to the
// connector — enough to keep concurrent sessions on one charge point distinct.
func chargerSessionKey(data *evsysEventData) string {
	if data.TransactionId != 0 {
		return fmt.Sprintf("%s|tx:%d", data.ChargePointId, data.TransactionId)
	}
	return fmt.Sprintf("%s|conn:%d", data.ChargePointId, data.ConnectorId)
}

// handleEVSysWebhook receives charging session events from evsys.
//
// Status codes are chosen against the evsys retry policy: it retries any
// non-2xx for up to 24h, so anything we cannot ever succeed at (bad token,
// malformed body, unknown location) is acknowledged rather than retried, and
// only genuine internal failures return 5xx.
func (s *Server) handleEVSysWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Unlike the agent secret and the UI login, this endpoint does not fail open
	// when unconfigured: it mutates battery state and is reachable from the
	// public internet, so without a token it simply does not exist.
	token := strings.TrimSpace(s.cfg.ChargerWebhookToken)
	if token == "" {
		http.NotFound(w, r)
		return
	}
	if !validChargerToken(r.Header.Get("Authorization"), token) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var env evsysEnvelope
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		s.log.With(slog.Any("error", err)).Warn("evsys webhook: invalid payload")
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	if env.Data == nil {
		http.Error(w, "missing data", http.StatusBadRequest)
		return
	}

	log := s.log.With(
		slog.String("event", env.Type),
		slog.String("event_id", env.Id),
		slog.String("charge_point", env.Data.ChargePointId),
		slog.String("location", env.Data.LocationId),
		slog.Int("transaction", env.Data.TransactionId),
	)

	var err error
	switch env.Type {
	case evsysTransactionStart:
		err = s.onChargerSessionStart(env, log)
	case evsysTransactionStop:
		err = s.onChargerSessionStop(env, log)
	default:
		// Acknowledged so evsys stops retrying; we only subscribe to transactions.
		log.Debug("evsys webhook: ignoring event type")
	}

	if err != nil {
		log.With(slog.Any("error", err)).Error("evsys webhook: processing failed")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// validChargerToken checks the "Authorization: Token <secret>" header that evsys
// sends (webhook/dispatcher.go). The comparison is constant-time.
func validChargerToken(header, expected string) bool {
	value := strings.TrimSpace(header)
	if value == "" {
		return false
	}
	// Accept both "Token <secret>" as sent by evsys and a bare secret.
	if fields := strings.Fields(value); len(fields) == 2 && strings.EqualFold(fields[0], "token") {
		value = fields[1]
	}
	return subtle.ConstantTimeCompare([]byte(value), []byte(expected)) == 1
}

func (s *Server) onChargerSessionStart(env evsysEnvelope, log *slog.Logger) error {
	link, ok := s.chargerLinks.Find(env.Data.LocationId, env.Data.ChargePointId)
	if !ok {
		log.Debug("evsys webhook: no charger link for this session")
		return nil
	}

	startedAt := env.Data.Time
	if startedAt.IsZero() {
		startedAt = env.Time
	}
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}

	session := ChargerSession{
		Key:            chargerSessionKey(env.Data),
		LinkName:       link.Name,
		AgentId:        link.AgentId,
		BatteryName:    link.BatteryName,
		LocationId:     env.Data.LocationId,
		ChargePointId:  env.Data.ChargePointId,
		ConnectorId:    env.Data.ConnectorId,
		TransactionId:  env.Data.TransactionId,
		IdTag:          env.Data.IdTag,
		Username:       env.Data.Username,
		StartedAt:      startedAt.UTC(),
		PowerLimit:     link.PowerLimit,
		SocLimit:       link.SocLimit,
		MaxDurationMin: link.MaxDurationMin,
	}

	added, err := s.chargerSessions.Add(session)
	if err != nil {
		return fmt.Errorf("persisting charger session: %w", err)
	}
	if !added {
		log.Debug("evsys webhook: duplicate transaction.start ignored")
		return nil
	}

	log.With(
		slog.String("link", link.Name),
		slog.String("agent", link.AgentId),
		slog.String("battery", link.BatteryName),
	).Info("charging session started; switching battery to discharge")

	s.applyChargerDischarge(link.AgentId, link.BatteryName)
	s.broadcastChargerSessions()
	return nil
}

func (s *Server) onChargerSessionStop(env evsysEnvelope, log *slog.Logger) error {
	session, removed, err := s.chargerSessions.Remove(chargerSessionKey(env.Data))
	if err != nil {
		return fmt.Errorf("persisting charger session: %w", err)
	}
	if !removed {
		log.Debug("evsys webhook: transaction.stop for unknown session ignored")
		return nil
	}

	log.With(
		slog.String("link", session.LinkName),
		slog.String("agent", session.AgentId),
		slog.String("battery", session.BatteryName),
		slog.Int("consumed_wh", env.Data.Consumed),
	).Info("charging session stopped; releasing battery")

	s.applyChargerDischarge(session.AgentId, session.BatteryName)
	s.broadcastChargerSessions()
	return nil
}

// applyChargerDischarge reconciles one battery with the sessions currently
// active on it: discharge while at least one session runs, release when the last
// one ends. Reconciling the whole set rather than reacting to single events
// keeps concurrent sessions at one site from double-starting or ending each
// other's discharge.
//
// With several sessions the highest power and the most conservative SoC floor
// win, so no session is served below its configured rate and none discharges the
// battery past another's floor.
func (s *Server) applyChargerDischarge(agentID, battery string) {
	sessions := s.chargerSessions.ForBattery(agentID, battery)

	log := s.log.With(
		slog.String("agent", agentID),
		slog.String("battery", battery),
	)

	if len(sessions) == 0 {
		s.sendChargerCommand(agentID, CommandRequest{
			Command: chargerCmdStopDischarge,
			Target:  battery,
		}, log)
		return
	}

	power, soc := 0, 0
	for _, ses := range sessions {
		if ses.PowerLimit > power {
			power = ses.PowerLimit
		}
		if ses.SocLimit > soc {
			soc = ses.SocLimit
		}
	}

	payload, err := json.Marshal(chargerStartPayload{
		Power:      power,
		PowerLimit: power,
		SocLimit:   soc,
		Source:     commandSourceCharger,
	})
	if err != nil {
		log.With(slog.Any("error", err)).Error("encode charger discharge payload")
		return
	}

	s.sendChargerCommand(agentID, CommandRequest{
		Command: chargerCmdStartDischarge,
		Target:  battery,
		Payload: payload,
	}, log.With(slog.Int("power", power), slog.Int("soc_limit", soc), slog.Int("sessions", len(sessions))))
}

// sendChargerCommand dispatches a command and logs failures. An offline agent is
// not an error worth failing the webhook over: the session stays recorded and is
// re-asserted when the agent reconnects.
func (s *Server) sendChargerCommand(agentID string, req CommandRequest, log *slog.Logger) {
	if err := s.sendCommand(agentID, req); err != nil {
		log.With(slog.String("command", req.Command), slog.Any("error", err)).
			Warn("charger command not delivered; will be re-asserted on reconnect")
		return
	}
	log.With(slog.String("command", req.Command)).Info("charger command sent to agent")
}

// reassertChargerSessions re-sends the discharge command for every battery of an
// agent that still has active sessions. Called when an agent (re)connects: the
// agent may have restarted, and its override lives only in memory.
func (s *Server) reassertChargerSessions(agentID string) {
	for _, battery := range s.chargerSessions.BatteriesForAgent(agentID) {
		s.log.With(
			slog.String("agent", agentID),
			slog.String("battery", battery),
		).Info("re-asserting charger discharge after agent connect")
		s.applyChargerDischarge(agentID, battery)
	}
}

// runChargerSweeper enforces the per-link safety cap. Without it, a
// transaction.stop lost for good (evsys gives up after 24h) would leave a
// battery discharging until it hit its SoC floor.
func (s *Server) runChargerSweeper(ctx context.Context) {
	ticker := time.NewTicker(chargerSweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweepChargerSessions(time.Now().UTC())
		}
	}
}

func (s *Server) sweepChargerSessions(now time.Time) {
	expired := s.chargerSessions.RemoveExpired(now)
	if len(expired) == 0 {
		return
	}

	affected := make(map[[2]string]struct{}, len(expired))
	for _, ses := range expired {
		s.log.With(
			slog.String("link", ses.LinkName),
			slog.String("agent", ses.AgentId),
			slog.String("battery", ses.BatteryName),
			slog.String("charge_point", ses.ChargePointId),
			slog.Int("transaction", ses.TransactionId),
			slog.Int("max_duration_min", ses.MaxDurationMin),
		).Warn("charging session exceeded its maximum duration; releasing battery")
		affected[[2]string{ses.AgentId, ses.BatteryName}] = struct{}{}
	}

	for key := range affected {
		s.applyChargerDischarge(key[0], key[1])
	}
	s.broadcastChargerSessions()
}

// handleChargerLinks serves the charger link configuration. Reads are public
// like the rest of the read-only UI surface; writes require a token.
func (s *Server) handleChargerLinks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(s.chargerLinks.List()); err != nil {
			s.log.With(slog.Any("error", err)).Error("encode charger links response")
		}

	case http.MethodPut:
		var links []ChargerLink
		if err := json.NewDecoder(r.Body).Decode(&links); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if err := s.chargerLinks.Set(links); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.log.Info("charger links updated", slog.Int("count", len(links)))

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(s.chargerLinks.List()); err != nil {
			s.log.With(slog.Any("error", err)).Error("encode charger links response")
		}

	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleChargerSessions lists the currently active charging sessions.
func (s *Server) handleChargerSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(s.chargerSessions.List()); err != nil {
		s.log.With(slog.Any("error", err)).Error("encode charger sessions response")
	}
}

func (s *Server) broadcastChargerSessions() {
	s.broadcastUI(UIChargerSessionsBroadcast{
		Type:     "charger.sessions",
		Sessions: s.chargerSessions.List(),
		SentAt:   time.Now().UTC(),
	})
}

// Package server implements the central control server that aggregates agent
// telemetry, serves the web UI, and routes commands between UI and agents.
//
// Architecture:
//
//	Agents ←→ [WebSocket /api/agent] ←→ Server ←→ [WebSocket /api/ui] ←→ React UI
//	                                       ↕
//	                              [REST /api/agents/*]
//
// Message flow:
//   - Agent → Server: hello, telemetry (every 10s), heartbeat (every 30s), config sync
//   - Server → Agent: config push, commands (start/stop discharge/charge, set_limits, etc.)
//   - Server → UI:    agent summaries, telemetry broadcasts, config updates, price data
//   - UI → Server:    config changes (PUT /api/agents/{id}/config), commands (POST /api/agents/{id}/command)
//
// Background goroutines:
//   - Price fetcher: polls REData API for electricity prices (every 15 min)
//   - Auto-scheduler: generates charge/discharge schedules from prices (every 5 min)
//   - Session cleanup: removes sessions older than 1 year (every hour)
//
// Config persistence: agent configs are stored in a JSON file (data/agent-configs.json)
// with optimistic locking (revision field) to prevent concurrent update conflicts.
package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gok-pi/battery/entity"
	"gok-pi/electricity/autoschedule"
	"gok-pi/electricity/pricefetcher"
	"gok-pi/remote/server/sessiondb"

	"github.com/gorilla/websocket"
)

type Server struct {
	cfg Config
	log *slog.Logger

	upgrader websocket.Upgrader

	agentsMu sync.RWMutex
	agents   map[string]*agentConnection

	uiMu     sync.RWMutex
	uiClient map[*uiConnection]struct{}

	configs  *ConfigStore
	auth     *authManager
	prices   *pricefetcher.Fetcher
	sessions *SessionTracker
}

func New(cfg Config, log *slog.Logger) *Server {
	store, err := NewConfigStore(cfg.ConfigStore)
	if err != nil {
		log.With(slog.Any("error", err), slog.String("path", cfg.ConfigStore)).Error("initializing config store; falling back to in-memory")
		store, _ = NewConfigStore("")
	}

	sessionDBPath := cfg.SessionDB
	if sessionDBPath == "" {
		sessionDBPath = "data/sessions.db"
	}
	sessDB, err := sessiondb.Open(sessionDBPath, log)
	if err != nil {
		log.With(slog.Any("error", err), slog.String("path", sessionDBPath)).Error("opening session database; sessions will be disabled")
	} else {
		log.With(slog.String("path", sessionDBPath)).Info("session database opened")
	}

	prices := pricefetcher.New(log, "data/prices-cache.json")

	var sessions *SessionTracker
	if sessDB != nil {
		sessions = NewSessionTracker(log, sessDB, prices)
		log.Info("session tracker started")
	} else {
		log.Warn("session tracker disabled: no database available")
	}

	return &Server{
		cfg: cfg,
		log: log,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
		agents:   make(map[string]*agentConnection),
		uiClient: make(map[*uiConnection]struct{}),
		configs:  store,
		auth:     newAuthManager(cfg.UIUsername, cfg.UIPassword, log),
		prices:   prices,
		sessions: sessions,
	}
}

func (s *Server) ListenAndServe(addr string) error {
	ctx := context.Background()
	go s.prices.Run(ctx)
	go s.runAutoScheduler(ctx)
	go s.runSessionCleanup(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/agent", s.handleAgentWS)
	mux.HandleFunc("/api/ui", s.handleUIWS)
	mux.HandleFunc("/api/agents", s.handleAgents)
	mux.HandleFunc("/api/agents/", s.requireAuthForWrites(s.handleAgentRoutes))
	mux.HandleFunc("/api/prices", s.handlePrices)
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/db-stats", s.handleDBStats)
	mux.HandleFunc("/api/db/records", s.requireAuth(s.handleDBRecords))

	if s.cfg.UIStaticDir != "" {
		fs := http.FileServer(http.Dir(s.cfg.UIStaticDir))
		mux.Handle("/app/", http.StripPrefix("/app/", fs))

		downloadPath := filepath.Join(s.cfg.UIStaticDir, "downloads")
		if info, err := os.Stat(downloadPath); err == nil && info.IsDir() {
			versionFile := s.versionFilename()
			mux.Handle("/downloads/"+versionFile, s.handleVersionManifest(downloadPath))

			downloadFS := http.FileServer(http.Dir(downloadPath))
			mux.Handle("/downloads/", http.StripPrefix("/downloads/", downloadFS))
		} else if err != nil {
			s.log.With(slog.String("path", downloadPath), slog.Any("error", err)).Warn("downloads directory unavailable")
		}
	}

	s.log.Info("control server listening", slog.String("addr", addr))
	return http.ListenAndServe(addr, s.withLogging(mux))
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		s.log.Debug("http request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Float64("duration", time.Since(start).Seconds()))
	})
}

func (s *Server) handleAgentWS(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SharedSecret != "" {
		token := r.Header.Get(headerSharedSecret)
		if token != s.cfg.SharedSecret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.With(slog.String("remote", r.RemoteAddr), slog.String("path", r.URL.Path)).Error("upgrade agent websocket", slog.Any("error", err))
		return
	}

	ac := newAgentConnection(conn, r, s)
	go ac.run()
}

func (s *Server) handleUIWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.With(slog.String("remote", r.RemoteAddr)).Error("upgrade ui websocket", slog.Any("error", err))
		return
	}
	client := newUIConnection(conn, s)

	s.registerUI(client)
	go client.run()
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	summaries := s.snapshotAgents()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(summaries); err != nil {
		s.log.With(slog.Any("error", err)).Error("encode agents response")
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func (s *Server) handleAgentRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/agents/")
	if path == "" {
		http.Error(w, "agent id required", http.StatusBadRequest)
		return
	}

	segments := strings.Split(path, "/")
	agentID := segments[0]

	if agentID == "" {
		http.Error(w, "agent id required", http.StatusBadRequest)
		return
	}

	if len(segments) == 1 || (len(segments) == 2 && segments[1] == "command") {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleAgentCommand(w, r, agentID)
		return
	}

	if len(segments) == 2 && segments[1] == "config" {
		switch r.Method {
		case http.MethodGet:
			s.handleAgentConfigGet(w, r, agentID)
			return
		case http.MethodPut:
			s.handleAgentConfigPut(w, r, agentID)
			return
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
	}

	if len(segments) == 2 && segments[1] == "logs" {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleAgentLogs(w, r, agentID)
		return
	}

	http.Error(w, "not found", http.StatusNotFound)
}

func (s *Server) handleAgentCommand(w http.ResponseWriter, r *http.Request, agentID string) {
	var req CommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	if req.Command == "" {
		http.Error(w, "command required", http.StatusBadRequest)
		return
	}

	if req.Target == "" {
		http.Error(w, "target battery required", http.StatusBadRequest)
		return
	}

	if err := s.sendCommand(agentID, req); err != nil {
		s.log.With(slog.String("agent", agentID), slog.Any("error", err)).Error("sending command to agent")
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleAgentConfigGet(w http.ResponseWriter, _ *http.Request, agentID string) {
	cfg, ok := s.configs.Get(agentID)
	if !ok {
		http.Error(w, "config not found", http.StatusNotFound)
		return
	}

	// Merge goal_reached_time from agent connection into schedule objects
	s.agentsMu.RLock()
	agent, agentConnected := s.agents[agentID]
	var goalReached map[string]time.Time
	if agentConnected {
		agent.mu.RLock()
		if len(agent.scheduleGoalReached) > 0 {
			goalReached = make(map[string]time.Time, len(agent.scheduleGoalReached))
			for k, v := range agent.scheduleGoalReached {
				goalReached[k] = v
			}
		}
		agent.mu.RUnlock()
	}
	s.agentsMu.RUnlock()

	// Apply goal times to schedules
	if len(goalReached) > 0 {
		for i := range cfg.Schedules {
			if t, ok := goalReached[cfg.Schedules[i].Name]; ok {
				cfg.Schedules[i].GoalReachedTime = &t
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(cfg); err != nil {
		s.log.With(slog.Any("error", err)).Error("encode agent config response")
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func (s *Server) handleAgentConfigPut(w http.ResponseWriter, r *http.Request, agentID string) {
	var req AgentConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	cfg, err := s.configs.Save(agentID, req)
	if err != nil {
		if errors.Is(err, ErrConfigConflict) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		s.log.With(slog.Any("error", err)).Error("saving agent config")
		http.Error(w, "failed to persist config", http.StatusInternalServerError)
		return
	}

	s.broadcastConfigUpdated(agentID, cfg)
	if err := s.pushConfigToAgent(agentID, cfg); err != nil {
		s.log.With(slog.String("agent", agentID), slog.Any("error", err)).Warn("push config to agent")
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(cfg); err != nil {
		s.log.With(slog.Any("error", err)).Error("encode agent config response")
	}
}

func (s *Server) registerAgent(ac *agentConnection) {
	s.agentsMu.Lock()
	s.agents[ac.id] = ac
	s.agentsMu.Unlock()

	s.broadcastAgentSnapshot(ac.summary())

	if cfg, ok := s.configs.Get(ac.id); ok {
		if err := ac.pushConfig(cfg); err != nil {
			s.log.With(
				slog.String("agent", ac.id),
				slog.Any("error", err),
			).Warn("failed to push config to agent on registration")
		}
	}
}

func (s *Server) unregisterAgent(id string) {
	s.agentsMu.Lock()
	delete(s.agents, id)
	s.agentsMu.Unlock()

	s.broadcastAgentRemoved(id)
}

func (s *Server) snapshotAgents() []AgentSummary {
	s.agentsMu.RLock()
	defer s.agentsMu.RUnlock()

	result := make([]AgentSummary, 0, len(s.agents))
	for _, agent := range s.agents {
		result = append(result, agent.summary())
	}
	return result
}

func (s *Server) sendCommand(agentID string, req CommandRequest) error {
	s.agentsMu.RLock()
	agent, ok := s.agents[agentID]
	s.agentsMu.RUnlock()

	if !ok {
		return fmt.Errorf("agent %s not connected", agentID)
	}

	return agent.sendCommand(req)
}

func (s *Server) onTelemetry(agentID string, snapshot TelemetrySnapshot) {
	if s.sessions != nil {
		s.sessions.OnTelemetry(agentID, snapshot)
	}
	s.broadcastTelemetry(agentID, snapshot)
}

func (s *Server) onAgentSummary(agentID string) {
	s.agentsMu.RLock()
	agent, ok := s.agents[agentID]
	s.agentsMu.RUnlock()
	if !ok {
		return
	}
	s.broadcastAgentSnapshot(agent.summary())
}

func (s *Server) registerUI(client *uiConnection) {
	s.uiMu.Lock()
	s.uiClient[client] = struct{}{}
	s.uiMu.Unlock()

	initial := struct {
		Type    string         `json:"type"`
		Agents  []AgentSummary `json:"agents"`
		SentAt  time.Time      `json:"sent_at"`
		Message string         `json:"message"`
	}{
		Type:    "agents.snapshot",
		Agents:  s.snapshotAgents(),
		SentAt:  time.Now().UTC(),
		Message: "initial state",
	}
	client.sendJSON(initial)
}

func (s *Server) unregisterUI(client *uiConnection) {
	s.uiMu.Lock()
	delete(s.uiClient, client)
	s.uiMu.Unlock()
}

func (s *Server) broadcastTelemetry(agentID string, snapshot TelemetrySnapshot) {
	s.broadcastUI(UITelemetryBroadcast{
		Type:     "agent.telemetry",
		AgentID:  agentID,
		Snapshot: snapshot,
		SentAt:   time.Now().UTC(),
	})
}

func (s *Server) broadcastAgentSnapshot(summary AgentSummary) {
	s.broadcastUI(UIAgentSummaryBroadcast{
		Type:    "agent.summary",
		Agent:   summary,
		SentAt:  time.Now().UTC(),
		Message: "state updated",
	})
}

func (s *Server) broadcastAgentRemoved(agentID string) {
	s.broadcastUI(UIAgentRemovedBroadcast{
		Type:    "agent.removed",
		AgentID: agentID,
		SentAt:  time.Now().UTC(),
	})
}

func (s *Server) broadcastUI(message interface{}) {
	s.uiMu.RLock()
	defer s.uiMu.RUnlock()

	for client := range s.uiClient {
		client.sendJSON(message)
	}
}

func (s *Server) broadcastConfigUpdated(agentID string, cfg AgentConfig) {
	payload := UIConfigUpdate{
		Type:    "config.updated",
		AgentID: agentID,
		Config:  cfg,
		SentAt:  time.Now().UTC(),
		Message: "agent config updated",
	}
	s.broadcastUI(payload)
}

func (s *Server) pushConfigToAgent(agentID string, cfg AgentConfig) error {
	s.agentsMu.RLock()
	agent, ok := s.agents[agentID]
	s.agentsMu.RUnlock()

	if !ok {
		return fmt.Errorf("agent %s not connected", agentID)
	}

	return agent.pushConfig(cfg)
}

func (s *Server) onAgentConfigSync(agentID string, sync AgentConfigSync) {
	cfg, seeded, err := s.configs.Seed(agentID, sync.Config.Batteries, sync.Config.Schedules, sync.SentAt)
	if err != nil {
		s.log.With(
			slog.String("agent", agentID),
			slog.Any("error", err),
		).Warn("seed agent config from snapshot")
		return
	}
	if !seeded {
		return
	}

	s.log.With(
		slog.String("agent", agentID),
		slog.Int("revision", cfg.Revision),
	).Info("seeded agent config from agent snapshot")

	s.broadcastConfigUpdated(agentID, cfg)
}

func (s *Server) handleVersionManifest(downloadPath string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
		default:
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		hash, err := s.computeAgentHash(downloadPath)
		if err != nil {
			s.log.With(
				slog.String("downloads", downloadPath),
				slog.Any("error", err),
			).Error("compute agent hash for VERSION")
			http.Error(w, "version unavailable", http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		if r.Method == http.MethodHead {
			return
		}

		if _, err := fmt.Fprintln(w, hash); err != nil {
			s.log.With(slog.Any("error", err)).Warn("write VERSION response")
		}
	})
}

func (s *Server) computeAgentHash(downloadPath string) (string, error) {
	binaryPath, err := s.resolveAgentBinary(downloadPath)
	if err != nil {
		return "", err
	}

	file, err := os.Open(binaryPath)
	if err != nil {
		return "", fmt.Errorf("open agent binary: %w", err)
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			s.log.Warn("failed to close agent binary", slog.String("error", err.Error()))
		}
	}(file)

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("hash agent binary: %w", err)
	}

	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

func (s *Server) resolveAgentBinary(downloadPath string) (string, error) {
	if name := strings.TrimSpace(s.cfg.AgentBinary); name != "" {
		candidate := filepath.Join(downloadPath, name)
		info, err := os.Stat(candidate)
		if err != nil {
			return "", fmt.Errorf("stat agent binary %s: %w", candidate, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("agent binary is a directory: %s", candidate)
		}
		return candidate, nil
	}

	entries, err := os.ReadDir(downloadPath)
	if err != nil {
		return "", fmt.Errorf("read downloads directory: %w", err)
	}

	versionName := strings.ToLower(s.versionFilename())
	var candidates []string
	var fallbacks []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		lower := strings.ToLower(name)

		if lower == versionName {
			continue
		}
		if strings.HasSuffix(lower, ".sha256") {
			continue
		}

		fallbacks = append(fallbacks, name)

		if strings.Contains(lower, "updater") {
			continue
		}

		candidates = append(candidates, name)
	}

	var chosen string
	switch {
	case len(candidates) > 0:
		sort.Strings(candidates)
		chosen = candidates[0]
	case len(fallbacks) > 0:
		sort.Strings(fallbacks)
		chosen = fallbacks[0]
	default:
		return "", fmt.Errorf("no candidate binaries available in %s", downloadPath)
	}

	return filepath.Join(downloadPath, chosen), nil
}

func (s *Server) versionFilename() string {
	name := strings.TrimSpace(s.cfg.VersionFile)
	if name == "" {
		return "VERSION"
	}
	return name
}

func (s *Server) handlePrices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state := s.prices.GetState()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(state); err != nil {
		s.log.With(slog.Any("error", err)).Error("encode prices response")
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.sessions == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"summaries":[],"sessions":[]}`))
		return
	}

	agentID := r.URL.Query().Get("agent_id")
	sinceStr := r.URL.Query().Get("since")
	untilStr := r.URL.Query().Get("until")
	hoursStr := r.URL.Query().Get("hours")

	var summaries []sessiondb.BatterySummary
	var err error

	if sinceStr != "" {
		sinceTime, parseErr := time.Parse(time.RFC3339, sinceStr)
		if parseErr != nil {
			http.Error(w, "invalid 'since' format, use RFC3339", http.StatusBadRequest)
			return
		}
		var untilTime time.Time
		if untilStr != "" {
			untilTime, parseErr = time.Parse(time.RFC3339, untilStr)
			if parseErr != nil {
				http.Error(w, "invalid 'until' format, use RFC3339", http.StatusBadRequest)
				return
			}
		}
		summaries, err = s.sessions.GetSummariesRange(agentID, sinceTime, untilTime)
	} else {
		hours := 48
		if hoursStr != "" {
			if h, parseErr := strconv.Atoi(hoursStr); parseErr == nil && h > 0 {
				hours = h
			}
		}
		summaries, err = s.sessions.GetSummaries(agentID, hours)
	}
	if err != nil {
		s.log.With(slog.Any("error", err)).Error("get session summaries")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if summaries == nil {
		summaries = []sessiondb.BatterySummary{}
	}

	recentHours := 48
	if hoursStr != "" {
		if h, parseErr := strconv.Atoi(hoursStr); parseErr == nil && h > 0 {
			recentHours = h
		}
	}
	sessions, err := s.sessions.GetRecentSessions(agentID, recentHours)
	if err != nil {
		s.log.With(slog.Any("error", err)).Error("get recent sessions")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if sessions == nil {
		sessions = []sessiondb.SessionRecord{}
	}

	resp := struct {
		Summaries []sessiondb.BatterySummary `json:"summaries"`
		Sessions  []sessiondb.SessionRecord  `json:"sessions"`
	}{
		Summaries: summaries,
		Sessions:  sessions,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.log.With(slog.Any("error", err)).Error("encode sessions response")
	}
}

func (s *Server) handleDBStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.sessions == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"stats":null,"agents":[]}`))
		return
	}

	stats, err := s.sessions.Store().GetStats()
	if err != nil {
		s.log.With(slog.Any("error", err)).Error("get db stats")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	agents, err := s.sessions.Store().GetAgentStats()
	if err != nil {
		s.log.With(slog.Any("error", err)).Error("get agent db stats")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if agents == nil {
		agents = []sessiondb.AgentDBStats{}
	}

	resp := struct {
		Stats  *sessiondb.DBStats       `json:"stats"`
		Agents []sessiondb.AgentDBStats `json:"agents"`
	}{
		Stats:  stats,
		Agents: agents,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) runAutoScheduler(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	// Run once on startup after a short delay to let prices load
	time.Sleep(30 * time.Second)
	s.updateAutoSchedules()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.updateAutoSchedules()
		}
	}
}

func (s *Server) updateAutoSchedules() {
	state := s.prices.GetState()
	if state.Today == nil && state.Tomorrow == nil {
		return
	}

	configs := s.configs.snapshot()
	for agentID, cfg := range configs {
		autoScheds := s.computeAutoSchedules(cfg, state)

		updated, changed, err := s.configs.UpdateAutoSchedules(agentID, autoScheds)
		if err != nil {
			s.log.With(slog.String("agent", agentID), slog.Any("error", err)).Warn("auto-schedule update failed")
			continue
		}
		if !changed {
			continue
		}

		s.log.Info("auto-schedules updated", slog.String("agent", agentID), slog.Int("count", len(autoScheds)))
		s.broadcastConfigUpdated(agentID, updated)
		if err := s.pushConfigToAgent(agentID, updated); err != nil {
			s.log.With(slog.String("agent", agentID), slog.Any("error", err)).Warn("push auto-schedule config to agent")
		}
	}
}

// computeAutoSchedules generates auto-schedules for an agent using DB-backed persistence
// when available, falling back to in-memory generation when the DB is unavailable.
//
// DB-backed flow:
//  1. Build ComputedSchedule records from today's/tomorrow's price data
//  2. Upsert to DB (stable IDs via unique constraint on date+battery+type+hours)
//  3. Read back today's schedules from DB (with stable IDs)
//  4. Filter to this agent's enabled AutoSchedule batteries
//  5. Convert to legacy entity.Schedule with names "auto-{id}-{type}-{battery}"
func (s *Server) computeAutoSchedules(cfg AgentConfig, state pricefetcher.State) []entity.Schedule {
	// Fallback: if DB is not available, use in-memory generation
	if s.sessions == nil {
		return autoschedule.GenerateSchedules(cfg.Batteries, state.Today, state.Tomorrow)
	}

	store := s.sessions.Store()

	// Persist computed schedules for today and tomorrow
	if state.Today != nil {
		records := autoschedule.BuildComputedSchedules(cfg.Batteries, state.Today)
		if len(records) > 0 {
			if err := store.UpsertSchedules(records); err != nil {
				s.log.Warn("failed to upsert today's computed schedules", slog.Any("error", err))
			}
		}
	}
	if state.Tomorrow != nil {
		records := autoschedule.BuildComputedSchedules(cfg.Batteries, state.Tomorrow)
		if len(records) > 0 {
			if err := store.UpsertSchedules(records); err != nil {
				s.log.Warn("failed to upsert tomorrow's computed schedules", slog.Any("error", err))
			}
		}
	}

	// Read back today's schedules from DB (only today's date is pushed to agents)
	now := madridNow()
	todayDate := now.Format("2006-01-02")
	todayRecords, err := store.GetSchedulesByDate(todayDate)
	if err != nil {
		s.log.Warn("failed to read today's schedules from DB, falling back to in-memory",
			slog.Any("error", err))
		return autoschedule.GenerateSchedules(cfg.Batteries, state.Today, state.Tomorrow)
	}

	// Filter to only this agent's enabled AutoSchedule batteries
	agentBatteries := make(map[string]bool)
	for _, b := range cfg.Batteries {
		if b.Enabled && b.AutoSchedule {
			agentBatteries[b.Name] = true
		}
	}
	var agentRecords []sessiondb.ComputedSchedule
	for _, r := range todayRecords {
		if agentBatteries[r.BatteryName] {
			agentRecords = append(agentRecords, r)
		}
	}

	// Convert to legacy entity.Schedule format for agent consumption
	return autoschedule.ToLegacySchedules(agentRecords, now)
}

// madridNow returns the current time in Europe/Madrid timezone.
func madridNow() time.Time {
	loc, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		loc = time.FixedZone("CET", 3600)
	}
	return time.Now().In(loc)
}

func (s *Server) runSessionCleanup(ctx context.Context) {
	if s.sessions == nil {
		return
	}
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sessions.Cleanup(365 * 24 * time.Hour) // 1 year retention
			if store := s.sessions.Store(); store != nil {
				if n, err := store.CleanupSchedules(30 * 24 * time.Hour); err != nil {
					s.log.Warn("computed schedule cleanup failed", slog.Any("error", err))
				} else if n > 0 {
					s.log.Info("cleaned up old computed schedules", slog.Int64("deleted", n))
				}
			}
		}
	}
}

func (s *Server) handleDBRecords(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.sessions == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"records":[],"total":0,"limit":100,"offset":0,"agents":[]}`))
		return
	}

	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	result, err := s.sessions.Store().QuerySessions(sessiondb.SessionQuery{
		AgentID:  q.Get("agent_id"),
		Type:     q.Get("type"),
		DateFrom: q.Get("date_from"),
		DateTo:   q.Get("date_to"),
		Status:   q.Get("status"),
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		s.log.With(slog.Any("error", err)).Error("query db records")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if result.Records == nil {
		result.Records = []sessiondb.SessionRecord{}
	}

	agents, err := s.sessions.Store().GetDistinctAgents()
	if err != nil {
		s.log.With(slog.Any("error", err)).Error("get distinct agents")
		agents = []string{}
	}

	resp := struct {
		*sessiondb.SessionQueryResult
		Agents []string `json:"agents"`
	}{
		SessionQueryResult: result,
		Agents:             agents,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.log.With(slog.Any("error", err)).Error("encode db records response")
	}
}

func (s *Server) handleAgentLogs(w http.ResponseWriter, r *http.Request, agentID string) {
	s.agentsMu.RLock()
	agent, ok := s.agents[agentID]
	s.agentsMu.RUnlock()

	if !ok {
		http.Error(w, "agent not connected", http.StatusNotFound)
		return
	}

	req := LogRequest{
		Lines:  500,     // default to last 500 lines
		Stream: "agent", // default to agent logs
	}

	if r.Method == http.MethodPost {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid payload", http.StatusBadRequest)
			return
		}
	} else {
		// GET request with query params
		if linesStr := r.URL.Query().Get("lines"); linesStr != "" {
			if lines, err := strconv.Atoi(linesStr); err == nil && lines > 0 {
				req.Lines = lines
			}
		}
		if stream := r.URL.Query().Get("stream"); stream != "" {
			req.Stream = stream
		}
	}

	resp, err := agent.requestLogs(req, 10*time.Second)
	if err != nil {
		s.log.With(slog.String("agent", agentID), slog.Any("error", err)).Error("requesting logs from agent")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if resp.Error != "" {
		http.Error(w, resp.Error, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if _, err := w.Write([]byte(resp.Logs)); err != nil {
		s.log.With(slog.Any("error", err)).Warn("write logs response")
	}
}

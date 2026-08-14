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
//   - evsys → Server: EV charging session events (POST /api/webhooks/evsys), which
//     turn into discharge commands for the linked battery; see chargers.go
//
// Background goroutines:
//   - Price fetcher: polls REData API for electricity prices (every 15 min)
//   - Auto-scheduler: generates charge/discharge schedules from prices (every 5 min)
//   - Session cleanup: removes sessions older than 1 year (every hour)
//   - Charger sweeper: releases batteries held by expired EV sessions (every minute)
//
// Config persistence: agent configs are stored in a JSON file (data/agent-configs.json)
// with optimistic locking (revision field) to prevent concurrent update conflicts.
package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"gok-pi/battery/entity"
	"gok-pi/electricity/autoschedule"
	"gok-pi/electricity/pricefetcher"
	"gok-pi/remote/server/email"
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

	configs     *ConfigStore
	auth        *authManager
	prices      *pricefetcher.Fetcher
	sessions    *SessionTracker
	priceLimits *PriceLimitsStore

	// db is the raw session/history store. sessions wraps it for session tracking;
	// telemetry history is written and queried directly.
	db        *sessiondb.Store
	telemetry *telemetryRecorder

	chargerLinks    *ChargerLinkStore
	chargerSessions *chargerSessionStore

	emailBrevo     *email.BrevoClient
	emailScheduler *email.Scheduler
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

	priceLimitsStore := NewPriceLimitsStore("data/price-limits.json")

	chargerLinksPath := cfg.ChargerLinks
	if chargerLinksPath == "" {
		chargerLinksPath = "data/charger-links.json"
	}
	chargerSessionsPath := cfg.ChargerSessions
	if chargerSessionsPath == "" {
		chargerSessionsPath = "data/charger-sessions.json"
	}

	srv := &Server{
		cfg: cfg,
		log: log,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
		agents:      make(map[string]*agentConnection),
		uiClient:    make(map[*uiConnection]struct{}),
		configs:     store,
		auth:        newAuthManager(cfg.UIUsername, cfg.UIPassword, log),
		prices:      prices,
		sessions:    sessions,
		priceLimits: priceLimitsStore,
		db:          sessDB,

		chargerLinks:    NewChargerLinkStore(chargerLinksPath),
		chargerSessions: newChargerSessionStore(chargerSessionsPath),
	}

	if sessDB != nil {
		srv.telemetry = newTelemetryRecorder(log, sessDB)
		log.Info("telemetry history recorder started")
	} else {
		log.Warn("telemetry history disabled: no database available")
	}

	if strings.TrimSpace(cfg.ChargerWebhookToken) == "" {
		log.Info("evsys charger webhook disabled: no token configured")
	} else {
		log.Info("evsys charger webhook enabled",
			slog.String("links", chargerLinksPath),
			slog.Int("links_configured", len(srv.chargerLinks.List())),
			slog.Int("sessions_restored", len(srv.chargerSessions.List())),
		)
	}

	if err := cfg.EmailProvider.Validate(); err != nil {
		log.Warn("email provider misconfigured; email reports disabled", slog.Any("error", err))
	} else if cfg.EmailProvider.Enabled {
		srv.emailBrevo = email.NewBrevo(cfg.EmailProvider, log)
		statePath := cfg.EmailState
		if statePath == "" {
			statePath = "data/email-reports-state.json"
		}
		sched, err := email.NewScheduler(
			log,
			srv.emailBrevo,
			sessDB,
			prices,
			statePath,
			srv.listEmailJobs,
			srv.emailPriceLimits,
		)
		if err != nil {
			log.Error("initialize email scheduler", slog.Any("error", err))
		} else if sched != nil {
			srv.emailScheduler = sched
			log.Info("email scheduler initialised",
				slog.String("sender", cfg.EmailProvider.SenderEmail),
				slog.String("state_path", statePath),
			)
		} else {
			log.Warn("email scheduler not started: missing dependencies (session DB or brevo client)")
		}
	}

	return srv
}

// listEmailJobs returns the snapshot of agents to evaluate for email dispatch.
// Implements email.AgentLister.
func (s *Server) listEmailJobs() []email.AgentJob {
	configs := s.configs.snapshot()
	jobs := make([]email.AgentJob, 0, len(configs))
	for agentID, cfg := range configs {
		if cfg.EmailReports == nil {
			continue
		}
		jobs = append(jobs, email.AgentJob{
			AgentID:    agentID,
			DeviceName: cfg.DeviceName,
			Timezone:   cfg.Timezone,
			Reports:    *cfg.EmailReports,
		})
	}
	return jobs
}

// emailPriceLimits adapts the server PriceLimits type to email.PriceLimits.
func (s *Server) emailPriceLimits() email.PriceLimits {
	pl := s.priceLimits.Get()
	return email.PriceLimits{
		ChargeLimitEurMWh:    pl.ChargeLimitEurMWh,
		DischargeLimitEurMWh: pl.DischargeLimitEurMWh,
	}
}

func (s *Server) ListenAndServe(addr string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.prices.Run(ctx)
	go s.runAutoScheduler(ctx)
	go s.runSessionCleanup(ctx)
	go s.runChargerSweeper(ctx)
	go s.runTelemetryRecorder(ctx)
	if s.emailScheduler != nil {
		go s.emailScheduler.Run(ctx)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/agent", s.handleAgentWS)
	// The UI supports a read-only public mode (overview, telemetry, prices) that
	// works without login, so the telemetry WebSocket and agent list stay public.
	mux.HandleFunc("/api/ui", s.handleUIWS)
	mux.HandleFunc("/api/agents", s.handleAgents)
	// requireAuthForWrites: GETs (agent list, config view, logs) are readable in the
	// public read-only mode; only mutating methods (commands, config PUT, email-test)
	// require a valid token.
	mux.HandleFunc("/api/agents/", s.requireAuthForWrites(s.handleAgentRoutes))
	mux.HandleFunc("/api/prices", s.handlePrices)
	mux.HandleFunc("/api/prices/export", s.handlePricesExport)
	mux.HandleFunc("/api/price-limits", s.requireAuthForWrites(s.handlePriceLimits))
	mux.HandleFunc("/api/sessions", s.handleSessions)
	// History is read-only telemetry, same visibility as live telemetry and prices.
	mux.HandleFunc("/api/history", s.handleHistory)
	mux.HandleFunc("/api/history/coverage", s.handleHistoryCoverage)
	mux.HandleFunc("/api/db-stats", s.handleDBStats)
	mux.HandleFunc("/api/db/records", s.requireAuth(s.handleDBRecords))
	mux.HandleFunc("/api/email/status", s.handleEmailStatus)
	// EV charger integration: evsys authenticates with its own webhook token,
	// so this endpoint is outside the UI auth scheme entirely.
	mux.HandleFunc("/api/webhooks/evsys", s.handleEVSysWebhook)
	mux.HandleFunc("/api/charger-links", s.requireAuthForWrites(s.handleChargerLinks))
	mux.HandleFunc("/api/charger-sessions", s.handleChargerSessions)

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

	srv := &http.Server{
		Addr:              addr,
		Handler:           s.withLogging(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Graceful shutdown: on SIGINT/SIGTERM, stop background goroutines, drain the
	// HTTP server, and close the session DB so SQLite checkpoints its WAL cleanly.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		s.log.Info("shutting down control server", slog.String("signal", sig.String()))
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			s.log.With(slog.Any("error", err)).Warn("http server shutdown")
		}
		if s.sessions != nil {
			if store := s.sessions.Store(); store != nil {
				if err := store.Close(); err != nil {
					s.log.With(slog.Any("error", err)).Warn("closing session DB")
				}
			}
		}
	}()

	s.log.Info("control server listening", slog.String("addr", addr))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
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
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.SharedSecret)) != 1 {
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

	if len(segments) == 2 && segments[1] == "email-test" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleAgentEmailTest(w, r, agentID)
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
	old := s.agents[ac.id]
	s.agents[ac.id] = ac
	s.agentsMu.Unlock()

	// If a previous connection for this agent ID is still registered, the agent
	// reconnected before the server detected the old socket was dead (e.g. a
	// half-open link where we wait out pongWait). Force the stale connection closed
	// so its goroutines exit promptly. Its cleanup will no-op against the registry
	// thanks to the identity check in unregisterAgent, so it cannot evict this one.
	if old != nil && old != ac {
		s.log.With(slog.String("agent", ac.id)).Info("replacing stale agent connection")
		_ = old.conn.Close()
	}

	s.broadcastAgentSnapshot(ac.summary())

	if cfg, ok := s.configs.Get(ac.id); ok {
		if err := ac.pushConfig(cfg); err != nil {
			s.log.With(
				slog.String("agent", ac.id),
				slog.Any("error", err),
			).Warn("failed to push config to agent on registration")
		}
	}

	// A charger-driven discharge lives only in the agent's memory, so an agent
	// that restarted (or missed the command while offline) needs it re-asserted.
	s.reassertChargerSessions(ac.id)
}

func (s *Server) unregisterAgent(ac *agentConnection) {
	s.agentsMu.Lock()
	// Only remove the entry if it still points to THIS connection. A reconnect may
	// have already replaced it with a newer connection under the same ID; deleting
	// blindly would orphan that live connection (gone from the UI, commands fail
	// with "agent not connected") until its socket dies or the server restarts.
	if current, ok := s.agents[ac.id]; !ok || current != ac {
		s.agentsMu.Unlock()
		return
	}
	delete(s.agents, ac.id)
	s.agentsMu.Unlock()

	s.log.With(slog.String("agent", ac.id)).Info("agent disconnected")
	s.telemetry.Forget(ac.id)
	s.broadcastAgentRemoved(ac.id)
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
	s.telemetry.Observe(agentID, snapshot, time.Now())
	s.broadcastTelemetry(agentID, snapshot)
}

// runTelemetryRecorder flushes buffered history and re-checks whether connected
// agents are still delivering telemetry.
func (s *Server) runTelemetryRecorder(ctx context.Context) {
	ticker := time.NewTicker(telemetryTick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.telemetry.Flush(time.Now())
			return
		case now := <-ticker.C:
			s.telemetry.Flush(now)
			s.checkTelemetryStalls(now)
		}
	}
}

// checkTelemetryStalls logs and broadcasts the transition when a connected agent
// stops (or starts) delivering telemetry. Only connected agents are considered: a
// disconnect is already reported on its own, and reporting it twice would bury the
// case this exists for — a live connection whose telemetry silently stopped.
func (s *Server) checkTelemetryStalls(now time.Time) {
	s.agentsMu.RLock()
	agents := make([]*agentConnection, 0, len(s.agents))
	for _, agent := range s.agents {
		agents = append(agents, agent)
	}
	s.agentsMu.RUnlock()

	for _, agent := range agents {
		stalled, changed, since := agent.evaluateTelemetryStall(now, telemetryStallAfter)
		if !changed {
			continue
		}
		if stalled {
			agent.log().With(
				slog.Time("last_telemetry_at", since),
				slog.Duration("quiet_for", now.Sub(since).Truncate(time.Second)),
			).Error("agent connected but telemetry stopped arriving")
		}
		s.broadcastAgentSnapshot(agent.summary())
	}
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
	// Marshal once and fan the bytes out to every UI client, instead of having each
	// client's write loop re-encode the identical payload via WriteJSON. With U clients
	// this turns U JSON serializations per broadcast into one.
	data, err := json.Marshal(message)
	if err != nil {
		s.log.With(slog.Any("error", err)).Error("marshal ui broadcast")
		return
	}

	s.uiMu.RLock()
	defer s.uiMu.RUnlock()

	for client := range s.uiClient {
		client.sendBytes(data)
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

// handleAgentEmailTest sends a one-off "[TEST]" preview of the daily report for
// yesterday to the given recipients. Useful for verifying Brevo wiring and
// inbox routing without waiting for the next scheduled send.
//
// Request body (optional): {"recipients": ["a@x.y", "b@x.y"]}
// When recipients is empty, the agent's stored EmailReports.Recipients is used.
// State (last-sent date) is NOT touched, so the regular morning report still fires.
func (s *Server) handleAgentEmailTest(w http.ResponseWriter, r *http.Request, agentID string) {
	if s.emailScheduler == nil {
		http.Error(w, "email reports not enabled on this server", http.StatusServiceUnavailable)
		return
	}

	cfg, ok := s.configs.Get(agentID)
	if !ok {
		http.Error(w, "agent config not found; save the configuration first", http.StatusNotFound)
		return
	}

	var body struct {
		Recipients []string `json:"recipients"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid payload", http.StatusBadRequest)
			return
		}
	}

	recipients := body.Recipients
	if len(recipients) == 0 && cfg.EmailReports != nil {
		recipients = cfg.EmailReports.Recipients
	}
	cleaned := make([]string, 0, len(recipients))
	for _, addr := range recipients {
		addr = strings.TrimSpace(addr)
		if addr != "" {
			cleaned = append(cleaned, addr)
		}
	}
	if len(cleaned) == 0 {
		http.Error(w, "no recipients configured", http.StatusBadRequest)
		return
	}

	job := email.AgentJob{
		AgentID:    agentID,
		DeviceName: cfg.DeviceName,
		Timezone:   cfg.Timezone,
		Reports:    email.EmailReportsAdapter(cfg.EmailReports, cleaned),
	}

	if err := s.emailScheduler.SendTestDaily(r.Context(), job, s.emailPriceLimits()); err != nil {
		s.log.Error("send test email", slog.String("agent", agentID), slog.Any("error", err))
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"sent":       true,
		"recipients": cleaned,
	})
}

// handleEmailStatus reports whether the email provider is configured and active.
// The API key itself is never returned. Used by the UI to show an info badge so
// admins know whether per-agent email settings will actually fire.
func (s *Server) handleEmailStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := struct {
		Enabled    bool   `json:"enabled"`
		Sender     string `json:"sender,omitempty"`
		Configured bool   `json:"configured"`
	}{
		Enabled:    s.emailScheduler != nil,
		Sender:     s.cfg.EmailProvider.SenderEmail,
		Configured: s.cfg.EmailProvider.Enabled,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handlePrices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state := s.prices.GetState()

	// Wrap the state with price limits for the UI
	resp := struct {
		pricefetcher.State
		PriceLimits PriceLimits `json:"price_limits"`
	}{
		State:       state,
		PriceLimits: s.priceLimits.Get(),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.log.With(slog.Any("error", err)).Error("encode prices response")
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func (s *Server) handlePricesExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")
	if startStr == "" || endStr == "" {
		http.Error(w, "start and end query parameters required (YYYY-MM-DD)", http.StatusBadRequest)
		return
	}

	start, err := time.Parse("2006-01-02", startStr)
	if err != nil {
		http.Error(w, "invalid start date, expected YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	end, err := time.Parse("2006-01-02", endStr)
	if err != nil {
		http.Error(w, "invalid end date, expected YYYY-MM-DD", http.StatusBadRequest)
		return
	}

	if end.Before(start) {
		http.Error(w, "end date must be after start date", http.StatusBadRequest)
		return
	}
	if end.Sub(start) > 90*24*time.Hour {
		http.Error(w, "maximum export range is 90 days", http.StatusBadRequest)
		return
	}

	client := s.prices.Client()

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=prices_%s_%s.csv", startStr, endStr))

	// Write CSV header
	w.Write([]byte("date,hour,price_eur_mwh\n"))

	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		prices, err := client.FetchPrices(r.Context(), d)
		if err != nil {
			s.log.Warn("export: failed to fetch prices for date",
				slog.String("date", d.Format("2006-01-02")),
				slog.Any("error", err))
			continue
		}
		dateStr := d.Format("2006-01-02")
		for _, p := range prices {
			line := fmt.Sprintf("%s,%d,%.2f\n", dateStr, p.Hour, p.Price)
			w.Write([]byte(line))
		}
	}
}

func (s *Server) handlePriceLimits(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.priceLimits.Get())

	case http.MethodPut:
		var limits PriceLimits
		if err := json.NewDecoder(r.Body).Decode(&limits); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if limits.ChargeLimitEurMWh < 0 || limits.DischargeLimitEurMWh < 0 {
			http.Error(w, "limits cannot be negative", http.StatusBadRequest)
			return
		}
		if err := s.priceLimits.Set(limits); err != nil {
			s.log.With(slog.Any("error", err)).Error("saving price limits")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		s.log.Info("price limits updated",
			slog.Float64("charge_limit", limits.ChargeLimitEurMWh),
			slog.Float64("discharge_limit", limits.DischargeLimitEurMWh))

		// Trigger auto-schedule recomputation with new limits
		go s.updateAutoSchedules()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(limits)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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

// handleHistory serves minute-resolution battery history: SoC, house load and
// battery power over a time window. Query params: agent_id, battery, since/until
// (RFC3339) or hours (default 24), limit.
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.db == nil {
		writeJSON(w, s.log, struct {
			Points []sessiondb.TelemetryPoint `json:"points"`
		}{Points: []sessiondb.TelemetryPoint{}})
		return
	}

	since, until, err := parseWindow(r, 24*time.Hour)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Flush first so the newest minute is present: without it the chart's right edge
	// is always up to one tick stale.
	s.telemetry.Flush(time.Now())

	q := sessiondb.HistoryQuery{
		AgentID:     r.URL.Query().Get("agent_id"),
		BatteryName: r.URL.Query().Get("battery"),
		Since:       since,
		Until:       until,
		Limit:       20000, // ~2 weeks of one battery at minute resolution
	}
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if limit, convErr := strconv.Atoi(limitStr); convErr == nil && limit > 0 {
			q.Limit = limit
		}
	}

	points, err := s.db.QueryTelemetryHistory(q)
	if err != nil {
		s.log.With(slog.Any("error", err)).Error("query telemetry history")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if points == nil {
		points = []sessiondb.TelemetryPoint{}
	}

	// until is a pointer so an open-ended window omits it: omitempty does not apply
	// to a time.Time value, which would otherwise serialize as year 1.
	var untilOut *time.Time
	if !until.IsZero() {
		untilOut = &until
	}

	writeJSON(w, s.log, struct {
		Points []sessiondb.TelemetryPoint `json:"points"`
		Since  time.Time                  `json:"since"`
		Until  *time.Time                 `json:"until,omitempty"`
	}{Points: points, Since: since, Until: untilOut})
}

// handleHistoryCoverage reports how complete the stored history is, per hour. It
// answers "did telemetry actually arrive during that window" — a complete hour is
// 60 buckets and 360 samples per battery at the agent's 10s poll interval.
func (s *Server) handleHistoryCoverage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.db == nil {
		writeJSON(w, s.log, struct {
			Hours []sessiondb.HourCoverage `json:"hours"`
		}{Hours: []sessiondb.HourCoverage{}})
		return
	}

	since, until, err := parseWindow(r, 48*time.Hour)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	hours, err := s.db.TelemetryCoverage(r.URL.Query().Get("agent_id"), since, until)
	if err != nil {
		s.log.With(slog.Any("error", err)).Error("query telemetry coverage")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if hours == nil {
		hours = []sessiondb.HourCoverage{}
	}

	writeJSON(w, s.log, struct {
		Hours           []sessiondb.HourCoverage `json:"hours"`
		ExpectedBuckets int                      `json:"expected_buckets"` // per hour, per battery
		ExpectedSamples int                      `json:"expected_samples"`
	}{Hours: hours, ExpectedBuckets: 60, ExpectedSamples: 360})
}

// parseWindow reads a since/until or hours window from the query string.
func parseWindow(r *http.Request, defaultSpan time.Duration) (since, until time.Time, err error) {
	query := r.URL.Query()

	if sinceStr := query.Get("since"); sinceStr != "" {
		since, err = time.Parse(time.RFC3339, sinceStr)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid 'since' format, use RFC3339")
		}
		if untilStr := query.Get("until"); untilStr != "" {
			until, err = time.Parse(time.RFC3339, untilStr)
			if err != nil {
				return time.Time{}, time.Time{}, fmt.Errorf("invalid 'until' format, use RFC3339")
			}
		}
		return since, until, nil
	}

	span := defaultSpan
	if hoursStr := query.Get("hours"); hoursStr != "" {
		if hours, convErr := strconv.Atoi(hoursStr); convErr == nil && hours > 0 {
			span = time.Duration(hours) * time.Hour
		}
	}
	return time.Now().UTC().Add(-span), time.Time{}, nil
}

// writeJSON encodes a response body, logging (but not surfacing) an encode failure:
// the status line is already on the wire by then.
func writeJSON(w http.ResponseWriter, log *slog.Logger, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.With(slog.Any("error", err)).Error("encode response")
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

	limits := s.priceLimits.Get()
	configs := s.configs.snapshot()
	for agentID, cfg := range configs {
		autoScheds := s.computeAutoSchedules(cfg, state, limits)

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
func (s *Server) computeAutoSchedules(cfg AgentConfig, state pricefetcher.State, limits PriceLimits) []entity.Schedule {
	// Fallback: if DB is not available, use in-memory generation
	if s.sessions == nil {
		return autoschedule.GenerateSchedules(cfg.Batteries, state.Today, state.Tomorrow, limits.ChargeLimitEurMWh, limits.DischargeLimitEurMWh)
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
		return autoschedule.GenerateSchedules(cfg.Batteries, state.Today, state.Tomorrow, limits.ChargeLimitEurMWh, limits.DischargeLimitEurMWh)
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

	// Convert to legacy entity.Schedule format for agent consumption, applying price limits
	return autoschedule.ToLegacySchedules(agentRecords, now, limits.ChargeLimitEurMWh, limits.DischargeLimitEurMWh)
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
				// Minute-resolution history is ~1440 rows per battery per day, so a
				// quarter of a year stays small while covering any question an
				// operator asks about "what happened back then".
				if n, err := store.CleanupHistory(historyRetention); err != nil {
					s.log.Warn("telemetry history cleanup failed", slog.Any("error", err))
				} else if n > 0 {
					s.log.Info("cleaned up old telemetry history", slog.Int64("deleted", n))
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

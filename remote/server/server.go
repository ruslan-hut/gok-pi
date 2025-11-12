package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

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
}

func New(cfg Config, log *slog.Logger) *Server {
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
	}
}

func (s *Server) ListenAndServe(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/agent", s.handleAgentWS)
	mux.HandleFunc("/api/ui", s.handleUIWS)
	mux.HandleFunc("/api/agents", s.handleAgents)
	mux.HandleFunc("/api/agents/", s.handleAgentCommand)

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

func (s *Server) handleAgentCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agentID := filepath.Base(r.URL.Path)
	if agentID == "" {
		http.Error(w, "agent id required", http.StatusBadRequest)
		return
	}

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

func (s *Server) registerAgent(ac *agentConnection) {
	s.agentsMu.Lock()
	s.agents[ac.id] = ac
	s.agentsMu.Unlock()

	s.broadcastAgentSnapshot(ac.summary())
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
	payload := struct {
		Type     string            `json:"type"`
		AgentID  string            `json:"agent_id"`
		Snapshot TelemetrySnapshot `json:"snapshot"`
		SentAt   time.Time         `json:"sent_at"`
	}{
		Type:     "agent.telemetry",
		AgentID:  agentID,
		Snapshot: snapshot,
		SentAt:   time.Now().UTC(),
	}
	s.broadcastUI(payload)
}

func (s *Server) broadcastAgentSnapshot(summary AgentSummary) {
	payload := struct {
		Type    string       `json:"type"`
		Agent   AgentSummary `json:"agent"`
		SentAt  time.Time    `json:"sent_at"`
		Message string       `json:"message"`
	}{
		Type:    "agent.summary",
		Agent:   summary,
		SentAt:  time.Now().UTC(),
		Message: "state updated",
	}
	s.broadcastUI(payload)
}

func (s *Server) broadcastAgentRemoved(agentID string) {
	payload := struct {
		Type    string    `json:"type"`
		AgentID string    `json:"agent_id"`
		SentAt  time.Time `json:"sent_at"`
	}{
		Type:    "agent.removed",
		AgentID: agentID,
		SentAt:  time.Now().UTC(),
	}
	s.broadcastUI(payload)
}

func (s *Server) broadcastUI(message interface{}) {
	s.uiMu.RLock()
	defer s.uiMu.RUnlock()

	for client := range s.uiClient {
		client.sendJSON(message)
	}
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
	defer file.Close()

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

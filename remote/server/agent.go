package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WebSocket message type constants define the protocol between agents and the control server.
//
// Agent → Server messages:
//   - agent.hello:        Initial handshake with agent metadata (ID, env, hostname, version)
//   - agent.telemetry:    Battery status snapshot (SoC, capacity, power, operating mode)
//   - agent.heartbeat:    Keep-alive signal (sent every 30s)
//   - agent.config:       Agent's current config snapshot (batteries + schedules)
//   - agent.log.response: Response to a log request with log file contents
//
// Server → Agent messages:
//   - server.config.push:  Push updated config (batteries, schedules, limits) to agent
//   - server.log.request:  Request agent to send back its log file contents
const (
	agentMessageHello       = "agent.hello"
	agentMessageTelemetry   = "agent.telemetry"
	agentMessageHeartbeat   = "agent.heartbeat"
	agentMessageCommand     = "agent.command"
	agentMessageConfig      = "agent.config"
	agentMessageLogResponse = "agent.log.response"

	serverMessageConfigPush = "server.config.push"
	serverMessageLogRequest = "server.log.request"
)

// WebSocket liveness tuning. The server pings agents every pingPeriod and requires
// a pong (or any frame) within pongWait, so a silently-dropped agent connection is
// detected within ~pongWait instead of lingering until TCP eventually errors.
// pingPeriod must be < pongWait. writeWait bounds a single write so a black-holed
// socket cannot block the write loop forever.
const (
	pongWait   = 70 * time.Second
	pingPeriod = 30 * time.Second
	writeWait  = 10 * time.Second
)

// agentConnection represents a single connected agent's WebSocket session.
// It manages the agent's lifecycle: handshake → read/write loops → cleanup.
// Each agent is identified by its ID (from config.yml device_id or hostname).
type agentConnection struct {
	id       string          // Unique agent identifier (from hello message)
	info     AgentDescriptor // Agent metadata (ID, env, hostname, version)
	lastSeen time.Time       // Timestamp of last received message (telemetry OR heartbeat)

	// lastTelemetryAt tracks the telemetry stream specifically. lastSeen cannot
	// stand in for it: the 30s heartbeat refreshes lastSeen, so an agent whose
	// telemetry has stopped still reads as perfectly healthy everywhere lastSeen
	// is used. telemetryFrames counts frames on this connection.
	lastTelemetryAt  time.Time
	telemetryFrames  uint64
	connectedAt      time.Time
	telemetryStalled bool

	conn *websocket.Conn // Underlying WebSocket connection
	req  *http.Request   // Original HTTP upgrade request (for remote addr logging)
	s    *Server         // Back-reference to parent server

	send chan interface{} // Outbound message queue (buffered, 32 slots)
	done chan struct{}    // Closed when connection terminates; signals goroutines to exit

	mu                  sync.RWMutex                 // Guards telemetry + scheduleGoalReached
	telemetry           map[string]TelemetrySnapshot // Latest telemetry per battery name
	scheduleGoalReached map[string]time.Time         // Goal reached timestamps per schedule name

	logRequestsMu sync.RWMutex                     // Guards logRequests map
	logRequests   map[string]chan AgentLogResponse // Pending log request-response pairs by request ID
}

func newAgentConnection(conn *websocket.Conn, r *http.Request, s *Server) *agentConnection {
	return &agentConnection{
		conn:        conn,
		req:         r,
		s:           s,
		send:        make(chan interface{}, 32),
		done:        make(chan struct{}),
		telemetry:   make(map[string]TelemetrySnapshot),
		logRequests: make(map[string]chan AgentLogResponse),
	}
}

// run is the agent connection lifecycle: handshake → register → read/write loops → unregister.
// It blocks until the connection drops (either side). On exit, the agent is unregistered
// from the server and UI clients are notified via "agent.removed" broadcast.
func (a *agentConnection) run() {
	defer func() {
		_ = a.conn.Close()
		close(a.done)
		if a.id != "" {
			a.s.unregisterAgent(a)
		}
	}()

	if err := a.handshake(); err != nil {
		a.log().With(slog.Any("error", err)).Warn("agent handshake failed")
		return
	}

	a.s.registerAgent(a)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		a.writeLoop()
	}()
	go func() {
		defer wg.Done()
		a.readLoop()
	}()
	wg.Wait()
}

func (a *agentConnection) handshake() error {
	if err := a.conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return err
	}

	_, message, err := a.conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("read hello: %w", err)
	}

	var hello AgentHello
	if err := json.Unmarshal(message, &hello); err != nil {
		return fmt.Errorf("decode hello: %w", err)
	}
	if hello.Type != agentMessageHello {
		return fmt.Errorf("expected hello, got %s", hello.Type)
	}
	if hello.Agent.ID == "" {
		return fmt.Errorf("missing agent id")
	}

	// Arm liveness detection for the rest of the connection: require a frame
	// (telemetry, heartbeat, or pong) within pongWait, refreshed on every pong.
	if err := a.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		return err
	}
	a.conn.SetPongHandler(func(string) error {
		return a.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	a.info = hello.Agent
	a.id = hello.Agent.ID
	a.lastSeen = time.Now()
	a.connectedAt = a.lastSeen

	a.log().Info("agent connected",
		slog.String("env", hello.Agent.Env),
		slog.String("hostname", hello.Agent.Hostname),
		slog.String("version", hello.Agent.Version),
	)

	return nil
}

func (a *agentConnection) readLoop() {
	// Closing the conn unblocks writeLoop's pending write/ping when read fails.
	defer func() { _ = a.conn.Close() }()
	// Refresh the read deadline on every received frame, not only on pongs, so an
	// agent that keeps sending telemetry but never pongs is still considered alive.
	for {
		_, message, err := a.conn.ReadMessage()
		if err != nil {
			a.log().With(slog.Any("error", err)).Warn("agent read error")
			return
		}
		_ = a.conn.SetReadDeadline(time.Now().Add(pongWait))
		a.handleMessage(message)
	}
}

func (a *agentConnection) writeLoop() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	// Closing the conn unblocks readLoop's pending ReadMessage when a write fails.
	defer func() { _ = a.conn.Close() }()

	for {
		select {
		case <-a.done:
			return
		case msg := <-a.send:
			_ = a.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := a.conn.WriteJSON(msg); err != nil {
				a.log().With(slog.Any("error", err)).Warn("agent write error")
				return
			}
		case <-ticker.C:
			if err := a.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait)); err != nil {
				a.log().With(slog.Any("error", err)).Warn("agent ping failed")
				return
			}
		}
	}
}

func (a *agentConnection) handleMessage(message []byte) {
	var base AgentMessage
	if err := json.Unmarshal(message, &base); err != nil {
		a.log().With(slog.Any("error", err)).Warn("decode agent message")
		return
	}

	switch base.Type {
	case agentMessageTelemetry:
		var telemetry AgentTelemetry
		if err := json.Unmarshal(message, &telemetry); err != nil {
			a.log().With(slog.Any("error", err)).Warn("decode telemetry message")
			return
		}
		a.updateTelemetry(telemetry)
	case agentMessageHeartbeat:
		var heartbeat AgentHeartbeat
		if err := json.Unmarshal(message, &heartbeat); err != nil {
			a.log().With(slog.Any("error", err)).Warn("decode heartbeat")
			return
		}
		a.updateHeartbeat(heartbeat)
	case agentMessageConfig:
		var cfg AgentConfigSync
		if err := json.Unmarshal(message, &cfg); err != nil {
			a.log().With(slog.Any("error", err)).Warn("decode config sync")
			return
		}
		a.updateScheduleGoalReached(cfg.Config.ScheduleGoalReached)
		a.s.onAgentConfigSync(a.id, cfg)
	case agentMessageLogResponse:
		var logResp AgentLogResponse
		if err := json.Unmarshal(message, &logResp); err != nil {
			a.log().With(slog.Any("error", err)).Warn("decode log response")
			return
		}
		a.handleLogResponse(logResp)
	default:
		a.log().With(slog.String("type", base.Type)).Debug("received unhandled agent message")
	}
}

func (a *agentConnection) updateTelemetry(msg AgentTelemetry) {
	a.mu.Lock()
	a.telemetry[msg.Snapshot.Name] = msg.Snapshot
	now := time.Now()
	a.lastSeen = now
	a.lastTelemetryAt = now
	a.telemetryFrames++
	resumed := a.telemetryStalled
	a.telemetryStalled = false
	a.mu.Unlock()

	if resumed {
		a.log().Info("agent telemetry resumed")
	}
	// Broadcast only the per-battery delta. We deliberately do NOT also broadcast a
	// full agent summary here: the summary would re-serialize and fan out the entire
	// telemetry + goalReached map to every UI client on every ~10s frame, which is the
	// same data the delta already carries. last_seen / connected status stay fresh via
	// the 30s heartbeat summary (offline grace in the UI is 2 minutes), and goalReached
	// changes are pushed via the config-sync path. a.lastSeen is still updated above so
	// REST snapshots and the next heartbeat report an accurate timestamp.
	a.s.onTelemetry(a.id, msg.Snapshot)
}

func (a *agentConnection) updateHeartbeat(_ AgentHeartbeat) {
	a.mu.Lock()
	a.lastSeen = time.Now()
	a.mu.Unlock()
	a.s.onAgentSummary(a.id)
}

func (a *agentConnection) updateScheduleGoalReached(goalReached map[string]time.Time) {
	a.mu.Lock()
	a.scheduleGoalReached = goalReached
	a.mu.Unlock()
	a.s.onAgentSummary(a.id)
}

func (a *agentConnection) pushConfig(cfg AgentConfig) error {
	payload := ConfigPush{
		Type:    serverMessageConfigPush,
		AgentID: a.id,
		Config:  cfg,
		SentAt:  time.Now().UTC(),
	}

	select {
	case <-a.done:
		return fmt.Errorf("agent connection closed")
	case a.send <- payload:
		return nil
	default:
		return fmt.Errorf("agent send buffer full")
	}
}

func (a *agentConnection) summary() AgentSummary {
	a.mu.RLock()
	defer a.mu.RUnlock()

	telemetry := make(map[string]TelemetrySnapshot, len(a.telemetry))
	for k, v := range a.telemetry {
		telemetry[k] = v
	}

	var goalReached map[string]time.Time
	if len(a.scheduleGoalReached) > 0 {
		goalReached = make(map[string]time.Time, len(a.scheduleGoalReached))
		for k, v := range a.scheduleGoalReached {
			goalReached[k] = v
		}
	}

	var lastTelemetry *time.Time
	if !a.lastTelemetryAt.IsZero() {
		at := a.lastTelemetryAt
		lastTelemetry = &at
	}

	return AgentSummary{
		Agent:               a.info,
		LastSeen:            a.lastSeen,
		LastTelemetryAt:     lastTelemetry,
		TelemetryFrames:     a.telemetryFrames,
		TelemetryStalled:    a.telemetryStalled,
		Telemetry:           telemetry,
		ScheduleGoalReached: goalReached,
	}
}

// evaluateTelemetryStall decides whether this connection's telemetry has gone
// quiet, and reports whether that verdict just changed so the caller logs the
// transition once rather than on every tick.
//
// The reference point is the last frame, or the moment the agent connected if it
// has never sent one: "connected and immediately silent" is the same fault as
// "connected and then went silent", and is just as invisible from lastSeen.
func (a *agentConnection) evaluateTelemetryStall(now time.Time, after time.Duration) (stalled, changed bool, quietSince time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()

	since := a.lastTelemetryAt
	if since.IsZero() {
		since = a.connectedAt
	}
	if since.IsZero() {
		return false, false, time.Time{}
	}

	stalled = now.Sub(since) >= after
	changed = stalled != a.telemetryStalled
	a.telemetryStalled = stalled
	return stalled, changed, since
}

func (a *agentConnection) sendCommand(req CommandRequest) error {
	command := OutgoingCommand{
		Type:      agentMessageCommand,
		Command:   req.Command,
		Target:    req.Target,
		RequestID: fmt.Sprintf("%s-%d", a.id, time.Now().UnixNano()),
		Payload:   req.Payload,
	}

	select {
	case <-a.done:
		return fmt.Errorf("agent connection closed")
	case a.send <- command:
		return nil
	default:
		return fmt.Errorf("agent command buffer full")
	}
}

func (a *agentConnection) requestLogs(req LogRequest, timeout time.Duration) (AgentLogResponse, error) {
	requestID := fmt.Sprintf("%s-logs-%d", a.id, time.Now().UnixNano())

	ch := make(chan AgentLogResponse, 1)
	a.logRequestsMu.Lock()
	a.logRequests[requestID] = ch
	a.logRequestsMu.Unlock()

	payload := struct {
		Type      string    `json:"type"`
		RequestID string    `json:"request_id"`
		Lines     int       `json:"lines,omitempty"`
		Stream    string    `json:"stream,omitempty"`
		SentAt    time.Time `json:"sent_at"`
	}{
		Type:      serverMessageLogRequest,
		RequestID: requestID,
		Lines:     req.Lines,
		Stream:    req.Stream,
		SentAt:    time.Now().UTC(),
	}

	select {
	case <-a.done:
		a.logRequestsMu.Lock()
		delete(a.logRequests, requestID)
		close(ch)
		a.logRequestsMu.Unlock()
		return AgentLogResponse{}, fmt.Errorf("agent connection closed")
	case a.send <- payload:
		// Wait for response with timeout
		select {
		case resp := <-ch:
			return resp, nil
		case <-time.After(timeout):
			a.logRequestsMu.Lock()
			delete(a.logRequests, requestID)
			close(ch)
			a.logRequestsMu.Unlock()
			return AgentLogResponse{}, fmt.Errorf("log request timed out")
		case <-a.done:
			a.logRequestsMu.Lock()
			delete(a.logRequests, requestID)
			close(ch)
			a.logRequestsMu.Unlock()
			return AgentLogResponse{}, fmt.Errorf("agent connection closed")
		}
	default:
		a.logRequestsMu.Lock()
		delete(a.logRequests, requestID)
		close(ch)
		a.logRequestsMu.Unlock()
		return AgentLogResponse{}, fmt.Errorf("agent send buffer full")
	}
}

func (a *agentConnection) handleLogResponse(resp AgentLogResponse) {
	a.logRequestsMu.Lock()
	ch, ok := a.logRequests[resp.RequestID]
	if ok {
		delete(a.logRequests, resp.RequestID)
	}
	a.logRequestsMu.Unlock()

	if ok {
		select {
		case ch <- resp:
		default:
		}
		close(ch)
	}
}

func (a *agentConnection) log() *slog.Logger {
	return a.s.log.With(
		slog.String("agent_id", a.id),
		slog.String("remote_addr", a.req.RemoteAddr),
	)
}

// Package wsclient implements the agent-side WebSocket client for connecting
// to the central control server.
//
// It maintains a persistent connection with automatic reconnection using exponential
// backoff (configurable initial/max seconds). On each connection it:
//  1. Dials the control server with shared secret + agent ID headers
//  2. Sends "agent.hello" with agent metadata (ID, env, hostname, version)
//  3. Sends initial telemetry snapshots and config snapshot
//  4. Starts concurrent read/write loops
//
// Write loop multiplexes three sources:
//   - Telemetry snapshots from battery observers (buffered, newest-wins on overflow)
//   - Config snapshots triggered by config changes
//   - Heartbeats every 30 seconds
//
// Read loop dispatches incoming messages:
//   - "server.config.push" → config updates channel (consumed by cmd/gok)
//   - "server.log.request" → reads local log files and responds
//   - Other messages       → command channel (consumed by cmd/gok for battery control)
//
// Both Commands() and ConfigUpdates() channels are consumed by the main agent
// goroutine in cmd/gok/main.go to route commands to discharger/charger workers.
package wsclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gok-pi/battery/entity"
	"gok-pi/internal/config"
	"gok-pi/internal/lib/sl"
	"gok-pi/metrics/observers"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const (
	headerSharedSecret = "X-GOK-Shared-Secret"
	headerAgentID      = "X-GOK-Agent-ID"
	headerAgentEnv     = "X-GOK-Agent-Env"

	defaultHeartbeatInterval = 30 * time.Second
	defaultDialTimeout       = 10 * time.Second
	defaultWriteTimeout      = 10 * time.Second
)

const (
	messageTypeConfigPush  = "server.config.push"
	messageTypeAgentConfig = "agent.config"
	messageTypeLogRequest  = "server.log.request"
	messageTypeLogResponse = "agent.log.response"
)

type AgentMetadata struct {
	ID  string
	Env string
}

type AgentInfo struct {
	ID       string `json:"id"`
	Env      string `json:"env"`
	Hostname string `json:"hostname"`
	Version  string `json:"version"`
}

type Command struct {
	Type      string          `json:"type"`
	Command   string          `json:"command"`
	Target    string          `json:"target,omitempty"`
	RequestID string          `json:"request_id,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Raw       IncomingMessage `json:"-"`
}

type IncomingMessage struct {
	Type      string          `json:"type"`
	Command   string          `json:"command,omitempty"`
	Target    string          `json:"target,omitempty"`
	RequestID string          `json:"request_id,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type helloMessage struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Agent     AgentInfo `json:"agent"`
}

type heartbeatMessage struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Agent     AgentInfo `json:"agent"`
}

type telemetryEnvelope struct {
	Type      string             `json:"type"`
	Timestamp time.Time          `json:"timestamp"`
	Agent     AgentInfo          `json:"agent"`
	Snapshot  observers.Snapshot `json:"snapshot"`
}

type Client struct {
	cfg config.RemoteControl
	log *slog.Logger

	agent AgentInfo

	startOnce       sync.Once
	onConnected     func() // invoked once a connection is fully established (resets backoff)
	telemetryCh     chan observers.Snapshot
	commands        chan Command
	configs         chan ConfigUpdate
	configSync      chan configSnapshot
	initialConfig   *configSnapshot
	initialConfigMu sync.RWMutex
}

func New(cfg config.RemoteControl, meta AgentMetadata, log *slog.Logger) *Client {
	if meta.Env == "" {
		meta.Env = "default"
	}
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}
	if meta.ID == "" {
		meta.ID = hostname
	}
	return &Client{
		cfg: cfg,
		log: log.With(sl.Module("remote.wsclient")),
		agent: AgentInfo{
			ID:       meta.ID,
			Env:      meta.Env,
			Hostname: hostname,
			Version:  detectVersion(),
		},
		telemetryCh: make(chan observers.Snapshot, 64),
		commands:    make(chan Command, 32),
		configs:     make(chan ConfigUpdate, 8),
		configSync:  make(chan configSnapshot, 4),
	}
}

func (c *Client) Commands() <-chan Command {
	return c.commands
}

func (c *Client) ConfigUpdates() <-chan ConfigUpdate {
	return c.configs
}

func (c *Client) PublishConfigSnapshot(batteries []entity.BatteryConfig, schedules []entity.Schedule) {
	// Extract goal reached times from schedules
	goalReached := make(map[string]time.Time)
	for _, s := range schedules {
		if s.GoalReachedTime != nil {
			goalReached[s.Name] = *s.GoalReachedTime
		}
	}
	snapshot := configSnapshot{
		Batteries:           entity.CloneBatteryConfigs(batteries),
		Schedules:           entity.CloneSchedules(schedules),
		ScheduleGoalReached: goalReached,
	}
	c.initialConfigMu.Lock()
	c.initialConfig = &snapshot
	c.initialConfigMu.Unlock()
	select {
	case c.configSync <- snapshot:
	default:
		c.log.With(slog.String("component", "config")).Warn("config snapshot dropped; buffer full")
	}
}

func (c *Client) Run(ctx context.Context) {
	c.startOnce.Do(func() {
		go c.run(ctx)
	})
}

// run is the main reconnection loop. It connects to the control server, serves
// until the connection drops, then reconnects with exponential backoff.
// Backoff doubles on each failure (e.g., 5s → 10s → 20s → ... → max) and resets
// to the initial value once a connection is successfully established, so a link
// that flaps and then stabilizes does not stay pinned at the maximum delay.
func (c *Client) run(ctx context.Context) {
	defer close(c.commands)
	defer close(c.configs)

	cancelObserver := observers.RegisterListener(func(snapshot observers.Snapshot) {
		c.enqueueSnapshot(snapshot)
	})
	defer cancelObserver()

	initialBackoff := time.Duration(c.cfg.Reconnect.InitialSeconds) * time.Second
	if initialBackoff <= 0 {
		initialBackoff = 5 * time.Second
	}
	maxBackoff := time.Duration(c.cfg.Reconnect.MaxSeconds) * time.Second
	if maxBackoff < initialBackoff {
		maxBackoff = initialBackoff
	}

	backoff := initialBackoff
	for {
		c.onConnected = func() { backoff = initialBackoff }
		err := c.connectAndServe(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
				return
			}
			c.log.With(sl.Err(err)).Error("websocket connection ended")
		} else {
			if ctx.Err() != nil {
				return
			}
			c.log.Info("websocket connection closed, retrying")
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
			backoff = minDuration(backoff*2, maxBackoff)
		}
	}
}

// enqueueSnapshot adds a telemetry snapshot to the send buffer using a "newest-wins" strategy.
// If the buffer is full, it drops the oldest snapshot to make room for the new one.
// This ensures the control server always receives the most recent battery state,
// even if the WebSocket write loop is temporarily slower than the observer update rate.
func (c *Client) enqueueSnapshot(snapshot observers.Snapshot) {
	select {
	case c.telemetryCh <- snapshot:
	default:
		// Buffer full: drop oldest, then enqueue newest
		select {
		case <-c.telemetryCh:
		default:
		}
		select {
		case c.telemetryCh <- snapshot:
		default:
		}
	}
}

func (c *Client) connectAndServe(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, defaultDialTimeout)
	defer cancel()

	headers := http.Header{}
	if c.cfg.SharedSecret != "" {
		headers.Set(headerSharedSecret, c.cfg.SharedSecret)
	}
	headers.Set(headerAgentID, c.agent.ID)
	headers.Set(headerAgentEnv, c.agent.Env)

	conn, resp, err := websocket.Dial(dialCtx, c.cfg.ServerURL, &websocket.DialOptions{
		HTTPHeader: headers,
	})
	if resp != nil && resp.Body != nil {
		defer func() {
			_ = resp.Body.Close()
		}()
	}
	if err != nil {
		return fmt.Errorf("dial websocket: %w", err)
	}
	defer func(conn *websocket.Conn, code websocket.StatusCode, reason string) {
		_ = conn.Close(code, reason)
	}(conn, websocket.StatusInternalError, "internal error")

	if err := c.sendHello(ctx, conn); err != nil {
		return err
	}

	if err := c.sendInitialSnapshots(ctx, conn); err != nil {
		return err
	}

	// Connection is fully established (dialed, hello + initial snapshots sent);
	// reset the reconnect backoff so the next drop retries quickly.
	if c.onConnected != nil {
		c.onConnected()
	}

	errCh := make(chan error, 2)

	go func() {
		errCh <- c.readLoop(ctx, conn)
	}()

	go func() {
		errCh <- c.writeLoop(ctx, conn)
	}()

	err = <-errCh
	// ensure other goroutine exits
	_ = conn.Close(websocket.StatusNormalClosure, "closing")
	<-errCh

	return err
}

func (c *Client) readLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		var raw json.RawMessage
		if err := wsjson.Read(ctx, conn, &raw); err != nil {
			return fmt.Errorf("read message: %w", err)
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return fmt.Errorf("decode message envelope: %w", err)
		}
		if envelope.Type == "" {
			continue
		}
		switch envelope.Type {
		case messageTypeConfigPush:
			c.handleConfigPush(raw)
		case messageTypeLogRequest:
			c.handleLogRequest(ctx, conn, raw)
		default:
			var msg IncomingMessage
			if err := json.Unmarshal(raw, &msg); err != nil {
				c.log.With(sl.Err(err)).Warn("decode incoming command")
				continue
			}
			c.dispatchCommand(msg)
		}
	}
}

func (c *Client) writeLoop(ctx context.Context, conn *websocket.Conn) error {
	heartbeatTicker := time.NewTicker(defaultHeartbeatInterval)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case snapshot := <-c.telemetryCh:
			if err := c.writeTelemetry(ctx, conn, snapshot); err != nil {
				return err
			}
		case cfg := <-c.configSync:
			if err := c.writeConfigSnapshot(ctx, conn, cfg); err != nil {
				return err
			}
		case <-heartbeatTicker.C:
			if err := c.writeHeartbeat(ctx, conn); err != nil {
				return err
			}
		}
	}
}

// writeJSON writes a message bounded by defaultWriteTimeout so a stalled socket
// cannot block the caller indefinitely.
func (c *Client) writeJSON(ctx context.Context, conn *websocket.Conn, v interface{}) error {
	wctx, cancel := context.WithTimeout(ctx, defaultWriteTimeout)
	defer cancel()
	return wsjson.Write(wctx, conn, v)
}

func (c *Client) sendHello(ctx context.Context, conn *websocket.Conn) error {
	msg := helloMessage{
		Type:      "agent.hello",
		Timestamp: time.Now().UTC(),
		Agent:     c.agent,
	}
	if err := c.writeJSON(ctx, conn, msg); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}
	return nil
}

func (c *Client) sendInitialSnapshots(ctx context.Context, conn *websocket.Conn) error {
	initial := observers.GetSnapshots()
	for _, snap := range initial {
		if err := c.writeTelemetry(ctx, conn, snap); err != nil {
			return err
		}
	}
	c.initialConfigMu.RLock()
	cfg := c.initialConfig
	c.initialConfigMu.RUnlock()
	if cfg != nil {
		if err := c.writeConfigSnapshot(ctx, conn, *cfg); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) writeHeartbeat(ctx context.Context, conn *websocket.Conn) error {
	msg := heartbeatMessage{
		Type:      "agent.heartbeat",
		Timestamp: time.Now().UTC(),
		Agent:     c.agent,
	}
	if err := c.writeJSON(ctx, conn, msg); err != nil {
		return fmt.Errorf("send heartbeat: %w", err)
	}
	return nil
}

func (c *Client) writeTelemetry(ctx context.Context, conn *websocket.Conn, snapshot observers.Snapshot) error {
	msg := telemetryEnvelope{
		Type:      "agent.telemetry",
		Timestamp: time.Now().UTC(),
		Agent:     c.agent,
		Snapshot:  snapshot,
	}
	if err := c.writeJSON(ctx, conn, msg); err != nil {
		return fmt.Errorf("send telemetry: %w", err)
	}
	return nil
}

func (c *Client) writeConfigSnapshot(ctx context.Context, conn *websocket.Conn, snapshot configSnapshot) error {
	msg := struct {
		Type      string        `json:"type"`
		Timestamp time.Time     `json:"sent_at"`
		Config    configPayload `json:"config"`
	}{
		Type:      messageTypeAgentConfig,
		Timestamp: time.Now().UTC(),
		Config:    configPayload(snapshot),
	}
	if err := c.writeJSON(ctx, conn, msg); err != nil {
		return fmt.Errorf("send config snapshot: %w", err)
	}
	return nil
}

func (c *Client) dispatchCommand(msg IncomingMessage) {
	command := Command{
		Type:      msg.Type,
		Command:   msg.Command,
		Target:    msg.Target,
		RequestID: msg.RequestID,
		Payload:   msg.Payload,
		Raw:       msg,
	}

	select {
	case c.commands <- command:
	default:
		c.log.With(
			slog.String("type", msg.Type),
			slog.String("command", msg.Command),
		).Warn("command dropped; buffer full")
	}
}

func (c *Client) handleConfigPush(raw json.RawMessage) {
	var payload struct {
		Type    string             `json:"type"`
		AgentID string             `json:"agent_id"`
		Config  entity.AgentConfig `json:"config"`
		SentAt  time.Time          `json:"sent_at"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		c.log.With(sl.Err(err)).Error("decode config push")
		return
	}

	update := ConfigUpdate{
		AgentID: payload.AgentID,
		Config:  payload.Config,
		SentAt:  payload.SentAt,
	}

	select {
	case c.configs <- update:
	default:
		c.log.With(slog.String("component", "config"), slog.String("agent", payload.AgentID)).Warn("config update dropped; buffer full")
	}
}

func (c *Client) handleLogRequest(ctx context.Context, conn *websocket.Conn, raw json.RawMessage) {
	var req struct {
		Type      string    `json:"type"`
		RequestID string    `json:"request_id"`
		Lines     int       `json:"lines,omitempty"`
		Stream    string    `json:"stream,omitempty"`
		SentAt    time.Time `json:"sent_at"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		c.log.With(sl.Err(err)).Error("decode log request")
		return
	}

	logs, err := c.readLogs(req.Stream, req.Lines)

	resp := struct {
		Type      string    `json:"type"`
		RequestID string    `json:"request_id"`
		Logs      string    `json:"logs,omitempty"`
		Error     string    `json:"error,omitempty"`
		SentAt    time.Time `json:"sent_at"`
	}{
		Type:      messageTypeLogResponse,
		RequestID: req.RequestID,
		SentAt:    time.Now().UTC(),
	}

	if err != nil {
		resp.Error = err.Error()
	} else {
		resp.Logs = logs
	}

	if err := c.writeJSON(ctx, conn, resp); err != nil {
		c.log.With(sl.Err(err)).Error("send log response")
	}
}

func (c *Client) readLogs(stream string, lines int) (string, error) {
	if lines <= 0 {
		lines = 500
	}
	if lines > 10000 {
		lines = 10000 // cap at 10k lines
	}

	var logPath string
	var candidates []string
	switch stream {
	case "updater", "agent-updater":
		// Autoupdater logs - check environment variable first, then common locations
		if envDir := os.Getenv("GOK_UPDATE_LOG_DIR"); envDir != "" {
			candidates = append(candidates, filepath.Join(envDir, "gok-pi.log"))
		}
		// Check common updater log locations
		candidates = append(candidates,
			"/var/log/gok-updater/gok-pi.log",
			"/var/log/gok/gok-pi.log",
			"/var/log/gok-pi.log",
		)
	case "agent", "":
		// Agent logs - check environment variable first
		if envDir := os.Getenv("GOK_LOG_DIR"); envDir != "" {
			candidates = append(candidates, filepath.Join(envDir, "gok-pi.log"))
		}
		// Check common agent log locations
		candidates = append(candidates,
			"/var/log/gok/gok-pi.log",
			"/var/log/gok-pi.log",
		)
	default:
		return "", fmt.Errorf("unknown log stream: %s", stream)
	}

	// Find the first existing log file
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			logPath = candidate
			break
		}
	}

	// If no log file found, use the first candidate (will return error on open)
	if logPath == "" {
		if len(candidates) > 0 {
			logPath = candidates[0]
		} else {
			return "", fmt.Errorf("no log path configured for stream: %s", stream)
		}
	}

	file, err := os.Open(logPath)
	if err != nil {
		return "", fmt.Errorf("open log file: %w", err)
	}
	defer func(file *os.File) {
		_ = file.Close()
	}(file)

	var allLines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		allLines = append(allLines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read log file: %w", err)
	}

	// Return last N lines
	start := 0
	if len(allLines) > lines {
		start = len(allLines) - lines
	}
	return strings.Join(allLines[start:], "\n"), nil
}

type ConfigUpdate struct {
	AgentID string
	Config  entity.AgentConfig
	SentAt  time.Time
}

type configSnapshot struct {
	Batteries           []entity.BatteryConfig
	Schedules           []entity.Schedule
	ScheduleGoalReached map[string]time.Time
}

type configPayload struct {
	Batteries           []entity.BatteryConfig `json:"batteries"`
	Schedules           []entity.Schedule      `json:"schedules"`
	ScheduleGoalReached map[string]time.Time   `json:"schedule_goal_reached,omitempty"`
}

func detectVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

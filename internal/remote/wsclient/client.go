package wsclient

import (
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
	"runtime/debug"
	"sync"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const (
	headerSharedSecret = "X-GOK-Shared-Secret"
	headerAgentID      = "X-GOK-Agent-ID"
	headerAgentEnv     = "X-GOK-Agent-Env"

	defaultHeartbeatInterval = 30 * time.Second
	defaultDialTimeout       = 10 * time.Second
)

const (
	messageTypeConfigPush  = "server.config.push"
	messageTypeAgentConfig = "agent.config"
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
	snapshot := configSnapshot{
		Batteries: cloneBatteryConfigs(batteries),
		Schedules: cloneSchedules(schedules),
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

func (c *Client) enqueueSnapshot(snapshot observers.Snapshot) {
	select {
	case c.telemetryCh <- snapshot:
	default:
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

	conn, _, err := websocket.Dial(dialCtx, c.cfg.ServerURL, &websocket.DialOptions{
		HTTPHeader: headers,
	})
	if err != nil {
		return fmt.Errorf("dial websocket: %w", err)
	}
	defer conn.Close(websocket.StatusInternalError, "internal error")

	if err := c.sendHello(ctx, conn); err != nil {
		return err
	}

	if err := c.sendInitialSnapshots(ctx, conn); err != nil {
		return err
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

func (c *Client) sendHello(ctx context.Context, conn *websocket.Conn) error {
	msg := helloMessage{
		Type:      "agent.hello",
		Timestamp: time.Now().UTC(),
		Agent:     c.agent,
	}
	if err := wsjson.Write(ctx, conn, msg); err != nil {
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
	if err := wsjson.Write(ctx, conn, msg); err != nil {
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
	if err := wsjson.Write(ctx, conn, msg); err != nil {
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
	if err := wsjson.Write(ctx, conn, msg); err != nil {
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
		Type    string      `json:"type"`
		AgentID string      `json:"agent_id"`
		Config  AgentConfig `json:"config"`
		SentAt  time.Time   `json:"sent_at"`
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

type AgentConfig struct {
	Revision  int                    `json:"revision"`
	UpdatedAt time.Time              `json:"updated_at"`
	Batteries []entity.BatteryConfig `json:"batteries"`
	Schedules []entity.Schedule      `json:"schedules"`
}

type ConfigUpdate struct {
	AgentID string
	Config  AgentConfig
	SentAt  time.Time
}

type configSnapshot struct {
	Batteries []entity.BatteryConfig
	Schedules []entity.Schedule
}

type configPayload struct {
	Batteries []entity.BatteryConfig `json:"batteries"`
	Schedules []entity.Schedule      `json:"schedules"`
}

func cloneBatteryConfigs(in []entity.BatteryConfig) []entity.BatteryConfig {
	if len(in) == 0 {
		return nil
	}
	out := make([]entity.BatteryConfig, len(in))
	copy(out, in)
	return out
}

func cloneSchedules(in []entity.Schedule) []entity.Schedule {
	if len(in) == 0 {
		return nil
	}
	out := make([]entity.Schedule, len(in))
	copy(out, in)
	return out
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

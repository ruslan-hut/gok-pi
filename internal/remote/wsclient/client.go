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
	"fmt"
	"gok-pi/battery/entity"
	"gok-pi/internal/config"
	"gok-pi/internal/lib/sl"
	"gok-pi/internal/remote/spool"
	"gok-pi/metrics/observers"
	"log/slog"
	"math/rand"
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

	// defaultReplyWriteTimeout bounds request/response replies (logs, diagnostics).
	// A log response can carry thousands of lines, which on a slow uplink takes far
	// longer than a telemetry frame; sharing the 10s budget made a large reply time
	// out and take the connection down with it.
	defaultReplyWriteTimeout = 60 * time.Second

	// defaultSpoolFlushInterval bounds how long buffered telemetry can wait if an
	// append signal is missed; live appends normally wake the loop immediately.
	defaultSpoolFlushInterval = 5 * time.Second
	// spoolBatchSize caps how many snapshots are read per flush iteration.
	spoolBatchSize = 200
)

const (
	messageTypeConfigPush   = "server.config.push"
	messageTypeAgentConfig  = "agent.config"
	messageTypeLogRequest   = "server.log.request"
	messageTypeLogResponse  = "agent.log.response"
	messageTypeDiagRequest  = "server.diag.request"
	messageTypeDiagResponse = "agent.diag.response"
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
	Type      string       `json:"type"`
	Timestamp time.Time    `json:"timestamp"`
	Agent     AgentInfo    `json:"agent"`
	Spool     *SpoolHealth `json:"spool,omitempty"`
}

// outboundMessage is a reply handed to the write loop. Replies are produced by
// the read loop but must not be written there: the connection serialises writes
// behind a single lock, so a slow reply and a telemetry frame would each be
// waiting on the other's deadline.
type outboundMessage struct {
	payload interface{}
	timeout time.Duration
	what    string
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
	outbound        chan outboundMessage // replies queued by the read loop, written by the write loop
	spool           *spool.Spool         // durable telemetry buffer; nil = in-memory only
	spoolSignal     chan struct{}        // wakes the write loop when new spool data is appended
	health          *uplinkHealth        // counters behind the heartbeat and diagnostics RPC
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
		outbound:    make(chan outboundMessage, 8),
		spoolSignal: make(chan struct{}, 1),
		health:      newUplinkHealth(),
		commands:    make(chan Command, 32),
		configs:     make(chan ConfigUpdate, 8),
		configSync:  make(chan configSnapshot, 4),
	}
}

// UseSpool enables durable telemetry buffering. When set, every snapshot is
// persisted to disk before sending and replayed in order on reconnect, so data
// collected during an outage (or across a restart) is not lost. Must be called
// before Run. If never called, the client falls back to an in-memory buffer.
func (c *Client) UseSpool(sp *spool.Spool) {
	c.spool = sp
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
		// The telemetry listener and the spool janitor are bound to the process,
		// not to the connection. Snapshots must keep reaching the spool while the
		// uplink is down — that is what makes an outage replayable instead of a
		// permanent hole in the history — so nothing here may stop when a
		// connection does.
		cancelObserver := observers.RegisterListener(func(snapshot observers.Snapshot) {
			c.enqueueSnapshot(snapshot)
		})
		go func() {
			<-ctx.Done()
			cancelObserver()
		}()

		if c.spool != nil {
			go c.spoolMaintenance(ctx)
		}

		go c.run(ctx)
	})
}

// run is the main reconnection loop. It connects to the control server, serves
// until the connection drops, then reconnects with exponential backoff.
// Backoff doubles on each failure (e.g., 5s → 10s → 20s → ... → max) and resets
// to the initial value once a connection is successfully established, so a link
// that flaps and then stabilizes does not stay pinned at the maximum delay.
//
// It returns only when ctx is cancelled. Every connection error — including a
// deadline from a per-write timeout — is retryable: treating those as a shutdown
// signal once left an agent with a live process, a working battery loop and no
// uplink at all until the next restart, silently, for thirteen hours.
func (c *Client) run(ctx context.Context) {
	defer close(c.commands)
	defer close(c.configs)

	// Belt and braces: the loop below has no non-ctx exit, so if this ever fires
	// the reason must be in the log rather than inferred from missing telemetry.
	defer func() {
		if ctx.Err() == nil {
			c.log.Error("remote control loop exited while the agent is still running; uplink is down until restart")
		}
	}()

	initialBackoff := time.Duration(c.cfg.Reconnect.InitialSeconds) * time.Second
	if initialBackoff <= 0 {
		initialBackoff = 5 * time.Second
	}
	maxBackoff := time.Duration(c.cfg.Reconnect.MaxSeconds) * time.Second
	if maxBackoff < initialBackoff {
		maxBackoff = initialBackoff
	}

	// Connection state is tracked across reconnect attempts so the link going
	// down and coming back up are each logged once, at INFO, as discrete events.
	// onConnected runs synchronously from connectAndServe in this same goroutine,
	// so sharing these locals needs no extra synchronization.
	backoff := initialBackoff
	connected := false
	everConnected := false
	c.onConnected = func() {
		backoff = initialBackoff
		connected = true
		if everConnected {
			c.health.recordReconnect()
			c.log.Info("control server connection restored", slog.String("url", c.cfg.ServerURL))
		} else {
			c.log.Info("control server connection established", slog.String("url", c.cfg.ServerURL))
		}
		everConnected = true
	}

	for {
		err := c.connectAndServe(ctx)
		wasConnected := connected
		connected = false

		if ctx.Err() != nil {
			return
		}

		switch {
		case wasConnected && err != nil:
			c.log.With(sl.Err(err)).Info("control server connection lost")
		case wasConnected:
			c.log.Info("control server connection lost")
		case err != nil:
			// Still down: a dial/handshake attempt failed. Logged at WARN to avoid
			// masking it, but it is not a fresh lost-connection event.
			c.log.With(sl.Err(err)).Warn("control server connection attempt failed")
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(jitter(backoff)):
			backoff = minDuration(backoff*2, maxBackoff)
		}
	}
}

// spoolMaintenance periodically prunes the durable telemetry buffer so it cannot
// grow without bound: delivered rows are kept briefly for debugging, and any row
// past the hard retention is removed even if never delivered (long outage backstop).
func (c *Client) spoolMaintenance(ctx context.Context) {
	const (
		interval           = time.Hour
		deliveredRetention = 24 * time.Hour
		hardRetention      = 30 * 24 * time.Hour
	)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		// Logged every hour whatever the outcome, with the backlog alongside.
		// "deleted=0 pending=4200 oldest=9h" is the line that would have made a
		// stalled uplink obvious; "deleted=360" alone said nothing, and silence
		// when nothing was deleted said less.
		deleted, err := c.spool.Prune(deliveredRetention, hardRetention)
		if err != nil {
			c.log.With(sl.Err(err)).Error("prune telemetry spool")
		} else {
			c.health.recordPrune(deleted)
			log := c.log.With(slog.Int64("deleted", deleted))
			if stats, statsErr := c.spool.Stats(); statsErr == nil {
				log = log.With(slog.Int64("pending", stats.Pending), slog.Int64("total", stats.Total))
				if stats.OldestUndelivered != nil {
					log = log.With(slog.Duration("oldest_undelivered",
						time.Since(*stats.OldestUndelivered).Truncate(time.Second)))
				}
			}
			log.Info("pruned telemetry spool")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// enqueueSnapshot adds a telemetry snapshot to the send buffer using a "newest-wins" strategy.
// If the buffer is full, it drops the oldest snapshot to make room for the new one.
// This ensures the control server always receives the most recent battery state,
// even if the WebSocket write loop is temporarily slower than the observer update rate.
func (c *Client) enqueueSnapshot(snapshot observers.Snapshot) {
	// Durable path: persist every snapshot to disk, then nudge the write loop.
	// Nothing is dropped here — the spool survives disconnects and restarts.
	if c.spool != nil {
		err := c.spool.Append(snapshot)
		c.health.recordAppend(err)
		if err != nil {
			c.log.With(sl.Err(err)).Error("failed to spool telemetry snapshot")
			return
		}
		select {
		case c.spoolSignal <- struct{}{}:
		default: // a wake-up is already pending
		}
		return
	}

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

	// Replies queued against the previous connection answer request IDs the server
	// has already abandoned; sending them wastes the uplink a reconnect just got back.
	c.drainOutbound()

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
			c.handleLogRequest(raw)
		case messageTypeDiagRequest:
			c.handleDiagRequest(raw)
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

	// Spooled mode: replay any backlog (including data buffered while offline) before
	// serving live updates, then drain on each append-signal. The ticker is a safety
	// net in case a wake-up signal was coalesced away.
	if c.spool != nil {
		if err := c.flushSpool(ctx, conn); err != nil {
			return err
		}
		spoolTicker := time.NewTicker(defaultSpoolFlushInterval)
		defer spoolTicker.Stop()
		backlogTicker := time.NewTicker(backlogCheckInterval)
		defer backlogTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-c.spoolSignal:
				if err := c.flushSpool(ctx, conn); err != nil {
					return err
				}
			case <-spoolTicker.C:
				if err := c.flushSpool(ctx, conn); err != nil {
					return err
				}
			case <-backlogTicker.C:
				if err := c.checkBacklog(); err != nil {
					// Returning tears the connection down so run() redials. A
					// backlog this old means the write path is not draining even
					// though the socket still reads, and only a fresh connection
					// (and a fresh write loop) has been seen to clear it.
					return err
				}
			case cfg := <-c.configSync:
				if err := c.writeConfigSnapshot(ctx, conn, cfg); err != nil {
					return err
				}
			case msg := <-c.outbound:
				if err := c.writeReply(ctx, conn, msg); err != nil {
					return err
				}
			case <-heartbeatTicker.C:
				if err := c.writeHeartbeat(ctx, conn); err != nil {
					return err
				}
				pingCtx, cancel := context.WithTimeout(ctx, defaultWriteTimeout)
				err := conn.Ping(pingCtx)
				cancel()
				if err != nil {
					return fmt.Errorf("ping: %w", err)
				}
			}
		}
	}

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
		case msg := <-c.outbound:
			if err := c.writeReply(ctx, conn, msg); err != nil {
				return err
			}
		case <-heartbeatTicker.C:
			if err := c.writeHeartbeat(ctx, conn); err != nil {
				return err
			}
			// Actively probe the link. Without this, a half-open connection (the
			// server is gone but no FIN/RST reached us) is only detected when the OS
			// TCP stack finally gives up — up to ~15 minutes — during which the agent
			// believes it is connected and never reconnects. An explicit ping with a
			// bounded wait surfaces a dead path within one interval and drops out of
			// the write loop so run() can reconnect.
			pingCtx, cancel := context.WithTimeout(ctx, defaultWriteTimeout)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return fmt.Errorf("ping: %w", err)
			}
		}
	}
}

// flushSpool sends all undelivered telemetry from the durable buffer in order,
// marking each batch delivered only after it is successfully written. A send error
// returns immediately so the connection is torn down and the same backlog is
// retried after reconnect; a batch is never marked delivered unless it was sent.
func (c *Client) flushSpool(ctx context.Context, conn *websocket.Conn) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		entries, err := c.spool.Undelivered(spoolBatchSize)
		if err != nil {
			// A read failure does not drop the connection — the next signal/tick
			// retries — but it is counted and surfaced, because repeated failures
			// here stop delivery entirely while the link still looks healthy.
			c.health.recordReadError(err)
			c.log.With(sl.Err(err)).Error("read telemetry spool")
			return nil
		}
		if len(entries) == 0 {
			return nil
		}
		for _, e := range entries {
			if err := c.writeTelemetry(ctx, conn, e.Snapshot); err != nil {
				c.health.recordFlushError(err)
				return err
			}
		}
		lastID := entries[len(entries)-1].ID
		if err := c.spool.MarkDelivered(lastID); err != nil {
			// Rows were sent but not marked, so they will be resent. Left
			// unreported this is silent duplication now and a spool that never
			// prunes later.
			c.health.recordMarkError(err)
			c.log.With(sl.Err(err)).Error("mark telemetry delivered")
			return nil
		}
		c.health.recordDelivered(len(entries))
		if len(entries) < spoolBatchSize {
			return nil
		}
	}
}

// writeJSON writes a message bounded by defaultWriteTimeout so a stalled socket
// cannot block the caller indefinitely. A deadline here fails the connection, not
// the client: run() redials.
func (c *Client) writeJSON(ctx context.Context, conn *websocket.Conn, v interface{}) error {
	return c.writeJSONTimeout(ctx, conn, v, defaultWriteTimeout)
}

func (c *Client) writeJSONTimeout(ctx context.Context, conn *websocket.Conn, v interface{}, timeout time.Duration) error {
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return wsjson.Write(wctx, conn, v)
}

// writeReply sends a queued request/response reply from the write loop, so it
// never contends with a telemetry frame for the connection's write lock.
func (c *Client) writeReply(ctx context.Context, conn *websocket.Conn, msg outboundMessage) error {
	timeout := msg.timeout
	if timeout <= 0 {
		timeout = defaultWriteTimeout
	}
	if err := c.writeJSONTimeout(ctx, conn, msg.payload, timeout); err != nil {
		return fmt.Errorf("send %s: %w", msg.what, err)
	}
	return nil
}

func (c *Client) drainOutbound() {
	for {
		select {
		case <-c.outbound:
		default:
			return
		}
	}
}

// enqueueReply hands a reply to the write loop. A full buffer drops the reply
// rather than blocking the read loop: the server's request times out on its own,
// and a blocked read loop would stop commands and config pushes entirely.
func (c *Client) enqueueReply(what string, payload interface{}, timeout time.Duration) {
	select {
	case c.outbound <- outboundMessage{payload: payload, timeout: timeout, what: what}:
	default:
		c.log.With(slog.String("reply", what)).Warn("reply dropped; outbound buffer full")
	}
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
	// In spooled mode the write loop replays the full backlog (which already
	// includes the latest snapshots), so sending them here would only duplicate.
	if c.spool == nil {
		initial := observers.GetSnapshots()
		for _, snap := range initial {
			if err := c.writeTelemetry(ctx, conn, snap); err != nil {
				return err
			}
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

// checkBacklog fails the connection when the oldest undelivered snapshot has
// aged past backlogStallAfter. Telemetry is flushed within seconds of being
// appended, so a backlog measured in quarter-hours is a stuck write path, not a
// slow one — and it produces no error of its own, which is precisely why it can
// run for hours unnoticed.
func (c *Client) checkBacklog() error {
	if c.spool == nil {
		return nil
	}

	stats, err := c.spool.Stats()
	if err != nil {
		c.health.recordReadError(err)
		c.log.With(sl.Err(err)).Warn("read telemetry spool stats")
		return nil
	}
	if stats.OldestUndelivered == nil {
		return nil
	}

	age := time.Since(*stats.OldestUndelivered)
	if age < backlogStallAfter {
		return nil
	}

	c.health.recordForcedRedial()
	c.log.With(
		slog.Int64("pending", stats.Pending),
		slog.Duration("oldest_undelivered", age.Truncate(time.Second)),
	).Error("telemetry backlog is not draining; dropping the connection to retry")

	return fmt.Errorf("telemetry backlog stalled: %d pending, oldest %s", stats.Pending, age.Truncate(time.Second))
}

// spoolHealth builds the heartbeat's uplink summary, tolerating a spool that is
// disabled or momentarily unreadable.
func (c *Client) spoolHealth() *SpoolHealth {
	var stats *spool.Stats
	if c.spool != nil {
		if s, err := c.spool.Stats(); err == nil {
			stats = &s
		} else {
			c.health.recordReadError(err)
		}
	}
	health := c.health.spoolHealth(stats, time.Now())
	return &health
}

func (c *Client) writeHeartbeat(ctx context.Context, conn *websocket.Conn) error {
	msg := heartbeatMessage{
		Type:      "agent.heartbeat",
		Timestamp: time.Now().UTC(),
		Agent:     c.agent,
		Spool:     c.spoolHealth(),
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
		Config  agentConfigPayload `json:"config"`
		SentAt  time.Time          `json:"sent_at"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		c.log.With(sl.Err(err)).Error("decode config push")
		return
	}

	update := ConfigUpdate{
		AgentID: payload.AgentID,
		Config:  payload.Config.toEntity(),
		SentAt:  payload.SentAt,
	}

	select {
	case c.configs <- update:
	default:
		c.log.With(slog.String("component", "config"), slog.String("agent", payload.AgentID)).Warn("config update dropped; buffer full")
	}
}

func (c *Client) handleLogRequest(raw json.RawMessage) {
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

	c.enqueueReply("log response", resp, defaultReplyWriteTimeout)
}

// handleDiagRequest answers an on-demand health request. It is read-only: it
// reports what the agent has been doing without touching the battery, the
// schedules, or the connection.
func (c *Client) handleDiagRequest(raw json.RawMessage) {
	var req struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		c.log.With(sl.Err(err)).Error("decode diagnostics request")
		return
	}

	resp := DiagResponse{
		Type:      messageTypeDiagResponse,
		RequestID: req.RequestID,
		Diag:      c.diagnostics(),
		SentAt:    time.Now().UTC(),
	}

	c.enqueueReply("diagnostics response", resp, defaultWriteTimeout)
}

// diagnostics gathers the agent's current health. The battery readings come from
// the observers rather than a fresh poll, so asking for diagnostics never adds
// load to the battery API or perturbs what is being diagnosed.
func (c *Client) diagnostics() Diagnostics {
	now := time.Now()

	diag := Diagnostics{
		Agent:      c.agent,
		UptimeSec:  int64(c.health.uptime(now).Seconds()),
		Goroutines: goroutineCount(),
		Uplink:     c.health.snapshot(),
		Batteries:  observers.GetSnapshots(),
	}

	if c.spool != nil {
		if stats, err := c.spool.Stats(); err != nil {
			diag.SpoolError = err.Error()
		} else {
			diag.Spool = &stats
		}
	}

	c.initialConfigMu.RLock()
	if cfg := c.initialConfig; cfg != nil {
		diag.Config = ConfigDiagnostics{
			Batteries: len(cfg.Batteries),
			Schedules: len(cfg.Schedules),
		}
	}
	c.initialConfigMu.RUnlock()

	return diag
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

// agentConfigPayload decodes a pushed config, typing only the parts the agent
// acts on. A config push also carries settings meant for other parts of the
// system — email report subscriptions, for one — and decoding those into their
// real types makes the agent reject the whole message when the server's schema
// for a field it never reads moves ahead of the binary on the device. That has
// happened: an agent ran on a stale config, silently, until it was rebuilt.
// Anything not typed here is ignored by encoding/json and cannot break the push.
type agentConfigPayload struct {
	DeviceName string                 `json:"device_name,omitempty"`
	Env        string                 `json:"env,omitempty"`
	Timezone   string                 `json:"timezone,omitempty"`
	Revision   int                    `json:"revision"`
	UpdatedAt  time.Time              `json:"updated_at"`
	Batteries  []entity.BatteryConfig `json:"batteries"`
	Schedules  []entity.Schedule      `json:"schedules"`
}

func (p agentConfigPayload) toEntity() entity.AgentConfig {
	return entity.AgentConfig{
		DeviceName: p.DeviceName,
		Env:        p.Env,
		Timezone:   p.Timezone,
		Revision:   p.Revision,
		UpdatedAt:  p.UpdatedAt,
		Batteries:  p.Batteries,
		Schedules:  p.Schedules,
	}
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

// version is injected at build time via -ldflags "-X gok-pi/internal/remote/wsclient.version=...".
// When empty, detectVersion falls back to the module build info.
var version string

func detectVersion() string {
	if version != "" {
		return version
	}
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

// jitter spreads a backoff delay by +/-20%. Without it every agent in the fleet
// retries on the same cadence, so a control server coming back from a restart is
// hit by all of them simultaneously, and a shared DNS failure produces lockstep
// retries from every device behind the same resolver.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	spread := float64(d) * 0.2
	return time.Duration(float64(d) - spread + rand.Float64()*2*spread)
}

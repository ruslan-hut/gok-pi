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

const (
	agentMessageHello     = "agent.hello"
	agentMessageTelemetry = "agent.telemetry"
	agentMessageHeartbeat = "agent.heartbeat"
	agentMessageCommand   = "agent.command"
)

type agentConnection struct {
	id       string
	info     AgentDescriptor
	lastSeen time.Time

	conn *websocket.Conn
	req  *http.Request
	s    *Server

	send chan interface{}
	done chan struct{}

	mu        sync.RWMutex
	telemetry map[string]TelemetrySnapshot
}

func newAgentConnection(conn *websocket.Conn, r *http.Request, s *Server) *agentConnection {
	return &agentConnection{
		conn:      conn,
		req:       r,
		s:         s,
		send:      make(chan interface{}, 32),
		done:      make(chan struct{}),
		telemetry: make(map[string]TelemetrySnapshot),
	}
}

func (a *agentConnection) run() {
	defer func() {
		_ = a.conn.Close()
		close(a.done)
		if a.id != "" {
			a.s.unregisterAgent(a.id)
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

	a.conn.SetReadDeadline(time.Time{})

	a.info = hello.Agent
	a.id = hello.Agent.ID
	a.lastSeen = hello.Timestamp

	a.log().Info("agent connected",
		slog.String("env", hello.Agent.Env),
		slog.String("hostname", hello.Agent.Hostname),
		slog.String("version", hello.Agent.Version),
	)

	return nil
}

func (a *agentConnection) readLoop() {
	for {
		select {
		case <-a.done:
			return
		default:
			_, message, err := a.conn.ReadMessage()
			if err != nil {
				a.log().With(slog.Any("error", err)).Warn("agent read error")
				return
			}
			a.handleMessage(message)
		}
	}
}

func (a *agentConnection) writeLoop() {
	for {
		select {
		case <-a.done:
			return
		case msg := <-a.send:
			if err := a.conn.WriteJSON(msg); err != nil {
				a.log().With(slog.Any("error", err)).Warn("agent write error")
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
	default:
		a.log().With(slog.String("type", base.Type)).Debug("received unhandled agent message")
	}
}

func (a *agentConnection) updateTelemetry(msg AgentTelemetry) {
	a.mu.Lock()
	a.telemetry[msg.Snapshot.Name] = msg.Snapshot
	a.lastSeen = msg.Timestamp
	a.mu.Unlock()
	a.s.onTelemetry(a.id, msg.Snapshot)
	a.s.onAgentSummary(a.id)
}

func (a *agentConnection) updateHeartbeat(msg AgentHeartbeat) {
	a.mu.Lock()
	a.lastSeen = msg.Timestamp
	a.mu.Unlock()
	a.s.onAgentSummary(a.id)
}

func (a *agentConnection) summary() AgentSummary {
	a.mu.RLock()
	defer a.mu.RUnlock()

	telemetry := make(map[string]TelemetrySnapshot, len(a.telemetry))
	for k, v := range a.telemetry {
		telemetry[k] = v
	}

	return AgentSummary{
		Agent:     a.info,
		LastSeen:  a.lastSeen,
		Telemetry: telemetry,
	}
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
	case a.send <- command:
		return nil
	default:
		return fmt.Errorf("agent command buffer full")
	}
}

func (a *agentConnection) log() *slog.Logger {
	return a.s.log.With(
		slog.String("agent_id", a.id),
		slog.String("remote_addr", a.req.RemoteAddr),
	)
}

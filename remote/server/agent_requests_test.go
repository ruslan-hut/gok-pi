package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func newTestAgentConnection() *agentConnection {
	return &agentConnection{
		id:           "agent-1",
		connectedAt:  time.Now(),
		telemetry:    make(map[string]TelemetrySnapshot),
		send:         make(chan interface{}, 4),
		done:         make(chan struct{}),
		req:          &http.Request{},
		s:            New(Config{}, testLogger()),
		logRequests:  newPendingRequests[AgentLogResponse](),
		diagRequests: newPendingRequests[AgentDiagResponse](),
	}
}

func TestRequestDiagnosticsReturnsAgentReply(t *testing.T) {
	a := newTestAgentConnection()

	// Stand in for the agent: read the outgoing request and answer it.
	go func() {
		msg := <-a.send
		payload, ok := msg.(struct {
			Type      string    `json:"type"`
			RequestID string    `json:"request_id"`
			SentAt    time.Time `json:"sent_at"`
		})
		if !ok || payload.Type != serverMessageDiagRequest {
			return
		}
		a.diagRequests.resolve(payload.RequestID, AgentDiagResponse{
			Type:        agentMessageDiagResponse,
			RequestID:   payload.RequestID,
			Diagnostics: json.RawMessage(`{"uptime_sec":42}`),
		})
	}()

	resp, err := a.requestDiagnostics(2 * time.Second)
	if err != nil {
		t.Fatalf("request diagnostics: %v", err)
	}
	if string(resp.Diagnostics) != `{"uptime_sec":42}` {
		t.Fatalf("expected the agent's payload verbatim, got %s", resp.Diagnostics)
	}
}

func TestRequestDiagnosticsTimesOut(t *testing.T) {
	a := newTestAgentConnection()

	start := time.Now()
	_, err := a.requestDiagnostics(150 * time.Millisecond)
	if err == nil {
		t.Fatal("expected a timeout when the agent never answers")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("expected the timeout to be honoured, waited %s", time.Since(start))
	}

	// A timed-out request must not leave its waiter behind, or a late reply would
	// resolve a request nobody is listening for and the map would grow forever.
	a.diagRequests.mu.Lock()
	pending := len(a.diagRequests.m)
	a.diagRequests.mu.Unlock()
	if pending != 0 {
		t.Fatalf("expected the abandoned request to be cleaned up, %d left", pending)
	}
}

func TestRequestDiagnosticsFailsOnClosedConnection(t *testing.T) {
	a := newTestAgentConnection()
	close(a.done)

	if _, err := a.requestDiagnostics(time.Second); err == nil {
		t.Fatal("expected a closed connection to fail fast")
	}
}

// A reply that arrives after its waiter gave up must be dropped quietly rather
// than blocking the read loop that delivered it.
func TestResolveWithoutWaiterIsSafe(t *testing.T) {
	pending := newPendingRequests[AgentLogResponse]()
	pending.resolve("unknown", AgentLogResponse{Logs: "late"})
}

func TestHeartbeatCapturesSpoolHealth(t *testing.T) {
	a := newTestAgentConnection()

	a.updateHeartbeat(AgentHeartbeat{
		Spool: &AgentSpoolHealth{Pending: 1200, OldestUndeliveredAgeSec: 3600, Appended: 5000, Delivered: 3800},
	})

	summary := a.summary()
	if summary.Spool == nil {
		t.Fatal("expected the heartbeat's spool health to reach the summary")
	}
	if summary.Spool.Pending != 1200 || summary.Spool.OldestUndeliveredAgeSec != 3600 {
		t.Fatalf("unexpected spool health: %+v", summary.Spool)
	}

	// A heartbeat without spool health (an older agent) must not erase what the
	// server already knows.
	a.updateHeartbeat(AgentHeartbeat{})
	if summary := a.summary(); summary.Spool == nil || summary.Spool.Pending != 1200 {
		t.Fatalf("expected the previous spool health to be retained, got %+v", summary.Spool)
	}
}

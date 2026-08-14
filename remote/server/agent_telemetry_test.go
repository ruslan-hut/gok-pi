package server

import (
	"net/http"
	"testing"
	"time"
)

func TestEvaluateTelemetryStallReportsTransitionsOnce(t *testing.T) {
	base := time.Date(2026, 8, 12, 20, 30, 0, 0, time.UTC)
	a := &agentConnection{connectedAt: base, lastTelemetryAt: base}

	stalled, changed, _ := a.evaluateTelemetryStall(base.Add(telemetryStallAfter-time.Second), telemetryStallAfter)
	if stalled || changed {
		t.Fatalf("expected no stall before the threshold, got stalled=%v changed=%v", stalled, changed)
	}

	stalled, changed, since := a.evaluateTelemetryStall(base.Add(telemetryStallAfter+time.Second), telemetryStallAfter)
	if !stalled || !changed {
		t.Fatalf("expected the stall to be reported once it crosses the threshold, got stalled=%v changed=%v", stalled, changed)
	}
	if !since.Equal(base) {
		t.Fatalf("expected the quiet period to date from the last frame, got %s", since)
	}

	// Still quiet: the verdict holds, but it is no longer a change to log.
	stalled, changed, _ = a.evaluateTelemetryStall(base.Add(10*time.Minute), telemetryStallAfter)
	if !stalled || changed {
		t.Fatalf("expected a sustained stall to stop re-reporting, got stalled=%v changed=%v", stalled, changed)
	}
}

// An agent that connects and never sends a frame is as broken as one that goes
// quiet later, and lastSeen hides both — the heartbeat keeps it fresh either way.
func TestEvaluateTelemetryStallCoversAgentThatNeverSent(t *testing.T) {
	base := time.Date(2026, 8, 12, 20, 30, 0, 0, time.UTC)
	a := &agentConnection{connectedAt: base}

	stalled, changed, since := a.evaluateTelemetryStall(base.Add(telemetryStallAfter+time.Second), telemetryStallAfter)
	if !stalled || !changed {
		t.Fatalf("expected a silent-since-connect agent to be reported, got stalled=%v changed=%v", stalled, changed)
	}
	if !since.Equal(base) {
		t.Fatalf("expected the quiet period to date from the connection, got %s", since)
	}
}

func TestUpdateTelemetryClearsStall(t *testing.T) {
	base := time.Date(2026, 8, 12, 20, 30, 0, 0, time.UTC)
	a := &agentConnection{
		connectedAt: base,
		telemetry:   make(map[string]TelemetrySnapshot),
		req:         &http.Request{},
		s:           New(Config{}, testLogger()),
	}

	if stalled, _, _ := a.evaluateTelemetryStall(base.Add(5*time.Minute), telemetryStallAfter); !stalled {
		t.Fatal("expected a stall to be recorded first")
	}

	a.updateTelemetry(AgentTelemetry{Snapshot: TelemetrySnapshot{Name: "BAT-001"}})

	stalled, changed, _ := a.evaluateTelemetryStall(time.Now(), telemetryStallAfter)
	if stalled || changed {
		t.Fatalf("expected a fresh frame to clear the stall, got stalled=%v changed=%v", stalled, changed)
	}
	if a.summary().TelemetryStalled {
		t.Fatal("summary should report a healthy stream after telemetry resumed")
	}
	if a.summary().LastTelemetryAt == nil {
		t.Fatal("summary should carry the last telemetry timestamp once a frame arrived")
	}
}

// A connection with no telemetry omits the timestamp rather than reporting the
// zero time, which renders as a real (and absurd) date.
func TestSummaryOmitsMissingTelemetryTimestamp(t *testing.T) {
	a := &agentConnection{
		connectedAt: time.Now(),
		telemetry:   make(map[string]TelemetrySnapshot),
		req:         &http.Request{},
		s:           New(Config{}, testLogger()),
	}

	if got := a.summary().LastTelemetryAt; got != nil {
		t.Fatalf("expected no telemetry timestamp, got %v", got)
	}
}

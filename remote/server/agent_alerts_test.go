package server

import (
	"testing"
	"time"
)

func TestAgentWatcherAlertsOncePerOutage(t *testing.T) {
	base := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	w := newAgentWatcher(nil, base)

	w.connected("agent-1", base)
	if due := w.dueForAlert(base.Add(time.Hour), agentOfflineAfter); len(due) != 0 {
		t.Fatalf("a connected agent must never be reported, got %d", len(due))
	}

	w.disconnected("agent-1", base.Add(time.Minute))
	if due := w.dueForAlert(base.Add(5*time.Minute), agentOfflineAfter); len(due) != 0 {
		t.Fatalf("expected no alert before the threshold, got %d", len(due))
	}

	due := w.dueForAlert(base.Add(time.Minute+agentOfflineAfter), agentOfflineAfter)
	if len(due) != 1 || due[0].agentID != "agent-1" {
		t.Fatalf("expected one outage for agent-1, got %+v", due)
	}
	if !due[0].since.Equal(base.Add(time.Minute)) {
		t.Fatalf("expected the outage to start at the disconnect, got %s", due[0].since)
	}

	if again := w.dueForAlert(base.Add(3*time.Hour), agentOfflineAfter); len(again) != 0 {
		t.Fatalf("the same outage must not be reported twice, got %d", len(again))
	}
}

func TestAgentWatcherReportsRecoveryOnlyAfterAnAlert(t *testing.T) {
	base := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	w := newAgentWatcher(nil, base)

	// A brief drop that never crossed the threshold produces no recovery mail:
	// nobody was told it went down.
	w.connected("agent-1", base)
	w.disconnected("agent-1", base.Add(time.Minute))
	if recovered, _ := w.connected("agent-1", base.Add(2*time.Minute)); recovered {
		t.Fatal("expected no recovery notice for an outage that was never reported")
	}

	w.disconnected("agent-1", base.Add(3*time.Minute))
	w.dueForAlert(base.Add(3*time.Minute+agentOfflineAfter), agentOfflineAfter)

	recovered, since := w.connected("agent-1", base.Add(time.Hour))
	if !recovered {
		t.Fatal("expected a recovery notice after a reported outage")
	}
	if !since.Equal(base.Add(3 * time.Minute)) {
		t.Fatalf("expected the outage start, got %s", since)
	}

	// And the next outage starts clean.
	w.disconnected("agent-1", base.Add(2*time.Hour))
	if due := w.dueForAlert(base.Add(2*time.Hour+agentOfflineAfter), agentOfflineAfter); len(due) != 1 {
		t.Fatalf("expected the next outage to be reportable, got %d", len(due))
	}
}

// A known agent that is already down when the control server starts is the case
// that silently produced nothing before: it never connects, so it never
// disconnects either.
func TestAgentWatcherSeedsKnownAgentsAsOffline(t *testing.T) {
	base := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	w := newAgentWatcher([]string{"agent-1"}, base)

	if due := w.dueForAlert(base.Add(time.Minute), agentOfflineAfter); len(due) != 0 {
		t.Fatalf("expected no alert before the threshold, got %d", len(due))
	}
	if due := w.dueForAlert(base.Add(agentOfflineAfter), agentOfflineAfter); len(due) != 1 {
		t.Fatalf("expected the never-seen agent to be reported, got %d", len(due))
	}
}

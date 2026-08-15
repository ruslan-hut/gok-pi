package server

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"gok-pi/battery/entity"
	"gok-pi/remote/server/email"
)

const (
	// agentOfflineAfter is how long an agent may be missing before the outage is
	// reported. The agent redials with a jittered backoff and the updater restarts
	// it within seconds, so a gap this long is not a reconnect — it is a device,
	// a network or an uplink that is not coming back on its own.
	agentOfflineAfter = 10 * time.Minute

	// agentAlertSendTimeout bounds one alert delivery. Alerts are sent from the
	// telemetry tick, which must not be held up by a slow mail provider.
	agentAlertSendTimeout = 30 * time.Second
)

// agentPresence is what the watcher remembers about one agent between ticks.
type agentPresence struct {
	connected bool
	since     time.Time // when the current state began
	alerted   bool      // an offline alert was sent for the current outage
}

// agentWatcher turns connect/disconnect events into at most one offline alert
// per outage, and one recovery notice for each alert raised. A flapping link
// therefore produces one pair of emails, not one per flap.
type agentWatcher struct {
	mu    sync.Mutex
	state map[string]*agentPresence
}

// outageNotice is an agent whose outage has just crossed the alert threshold.
type outageNotice struct {
	agentID string
	since   time.Time
}

// newAgentWatcher seeds every agent the server already knows about as
// disconnected. Without the seed, an agent that is down while the control server
// restarts is never alerted on: it is absent, and absence alone raises nothing.
func newAgentWatcher(known []string, now time.Time) *agentWatcher {
	w := &agentWatcher{state: make(map[string]*agentPresence, len(known))}
	for _, id := range known {
		w.state[id] = &agentPresence{since: now}
	}
	return w
}

// connected records a connection. It reports whether this closes an outage that
// was alerted on, and how long that outage lasted.
func (w *agentWatcher) connected(agentID string, now time.Time) (recovered bool, since time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()

	p, ok := w.state[agentID]
	if !ok {
		w.state[agentID] = &agentPresence{connected: true, since: now}
		return false, now
	}
	if p.connected {
		return false, p.since
	}

	recovered = p.alerted
	since = p.since
	p.connected = true
	p.since = now
	p.alerted = false
	return recovered, since
}

func (w *agentWatcher) disconnected(agentID string, now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()

	p, ok := w.state[agentID]
	if !ok {
		w.state[agentID] = &agentPresence{since: now}
		return
	}
	if !p.connected {
		return
	}
	p.connected = false
	p.since = now
	p.alerted = false
}

// dueForAlert returns the outages that have just crossed the threshold, marking
// them so the same outage is only ever reported once.
func (w *agentWatcher) dueForAlert(now time.Time, after time.Duration) []outageNotice {
	w.mu.Lock()
	defer w.mu.Unlock()

	var due []outageNotice
	for agentID, p := range w.state {
		if p.connected || p.alerted || now.Sub(p.since) < after {
			continue
		}
		p.alerted = true
		due = append(due, outageNotice{agentID: agentID, since: p.since})
	}
	return due
}

// checkAgentOutages runs on the telemetry tick and mails the outages that have
// just crossed the threshold.
func (s *Server) checkAgentOutages(now time.Time) {
	for _, notice := range s.agentWatch.dueForAlert(now, agentOfflineAfter) {
		s.log.With(
			slog.String("agent", notice.agentID),
			slog.Time("last_connected_at", notice.since),
			slog.Duration("offline_for", now.Sub(notice.since).Truncate(time.Second)),
		).Error("agent has not reconnected")
		s.sendConnectivityAlert(email.AlertOffline, notice.agentID, notice.since, now)
	}
}

// sendConnectivityAlert mails an uplink transition to the recipients subscribed
// to alerts for this agent — a separate opt-in from the scheduled reports, since
// a customer who wants a monthly summary has no use for a 3am outage notice.
// Delivery runs in its own goroutine so the caller — a lock-free tick or a
// connection handler — is never held up by the mail provider.
func (s *Server) sendConnectivityAlert(kind email.AlertKind, agentID string, since, now time.Time) {
	if s.emailBrevo == nil {
		return
	}

	cfg, ok := s.configs.Get(agentID)
	if !ok || cfg.EmailReports == nil || !cfg.EmailReports.Enabled {
		return
	}

	recipients := cfg.EmailReports.RecipientsFor(entity.EmailKindAlerts)
	if len(recipients) == 0 {
		return
	}

	subject, html, err := email.RenderConnectivityAlert(email.ConnectivityAlert{
		Kind:       kind,
		AgentID:    agentID,
		DeviceName: cfg.DeviceName,
		Timezone:   cfg.Timezone,
		Since:      since,
		Now:        now,
	})
	if err != nil {
		s.log.With(slog.String("agent", agentID), slog.Any("error", err)).Error("render connectivity alert")
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), agentAlertSendTimeout)
		defer cancel()

		if err := s.emailBrevo.Send(ctx, recipients, subject, html); err != nil {
			s.log.With(
				slog.String("agent", agentID),
				slog.String("alert", string(kind)),
				slog.Any("error", err),
			).Error("send connectivity alert")
			return
		}
		s.log.With(
			slog.String("agent", agentID),
			slog.String("alert", string(kind)),
			slog.Int("recipients", len(recipients)),
		).Info("connectivity alert sent")
	}()
}

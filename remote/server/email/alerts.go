package email

import (
	"fmt"
	"strings"
	"time"
)

// AlertKind distinguishes the two uplink transitions worth an email.
type AlertKind string

const (
	// AlertOffline is sent once per outage, when an agent has been gone long
	// enough that a flapping link or a restart has been ruled out.
	AlertOffline AlertKind = "offline"
	// AlertRecovered closes an outage that was alerted on, so an inbox never
	// leaves an open question.
	AlertRecovered AlertKind = "recovered"
)

// ConnectivityAlert describes an agent whose uplink state just changed. It is
// deliberately thin: the control server knows nothing about why an agent went
// quiet, and guessing in the email would be worse than saying what is known.
type ConnectivityAlert struct {
	Kind       AlertKind
	AgentID    string
	DeviceName string
	Timezone   string
	Since      time.Time // when the agent was last connected
	Now        time.Time
}

type alertView struct {
	Title      string
	Heading    string
	SubHeading string
	Accent     string
	AgentID    string
	DeviceName string
	Since      string
	Duration   string
	Note       string
}

// RenderConnectivityAlert produces (subject, htmlBody) for an uplink alert.
func RenderConnectivityAlert(a ConnectivityAlert) (string, string, error) {
	name := strings.TrimSpace(a.DeviceName)
	if name == "" {
		name = a.AgentID
	}

	outage := a.Now.Sub(a.Since)
	view := alertView{
		AgentID:    a.AgentID,
		DeviceName: name,
		Since:      formatTimeLocal(a.Since, a.Timezone),
		Duration:   formatOutage(outage),
	}

	var subject string
	switch a.Kind {
	case AlertRecovered:
		subject = fmt.Sprintf("%s is back online", name)
		view.Title = subject
		view.Heading = fmt.Sprintf("%s reconnected", name)
		view.SubHeading = fmt.Sprintf("The control server is receiving telemetry again after %s.", formatOutage(outage))
		view.Accent = "#16a34a"
		view.Note = "Readings buffered on the device while it was offline are replayed on reconnect, so the history should fill back in."
	default:
		subject = fmt.Sprintf("%s is offline", name)
		view.Title = subject
		view.Heading = fmt.Sprintf("%s stopped reporting", name)
		view.SubHeading = fmt.Sprintf("No connection to the control server for %s.", formatOutage(outage))
		view.Accent = "#dc2626"
		view.Note = "The device may still be charging and discharging on its local schedule — the control server simply cannot see it, and remote commands will not reach it until it reconnects."
	}

	var buf strings.Builder
	if err := templates.ExecuteTemplate(&buf, "alert.gohtml", view); err != nil {
		return "", "", fmt.Errorf("render alert email: %w", err)
	}
	return "GOK-Pi: " + subject, buf.String(), nil
}

// formatOutage renders a duration the way it gets read in an alert: whole
// minutes, hours where they exist, and nothing more precise than that.
func formatOutage(d time.Duration) string {
	if d < time.Minute {
		return "less than a minute"
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	switch {
	case hours == 0:
		return fmt.Sprintf("%dm", minutes)
	case minutes == 0:
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
}

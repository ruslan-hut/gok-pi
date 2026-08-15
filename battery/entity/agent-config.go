package entity

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// AgentConfig represents the configuration for a single agent, used in the WebSocket
// protocol between agents and the control server.
type AgentConfig struct {
	DeviceName   string              `json:"device_name,omitempty"`
	Env          string              `json:"env,omitempty"`
	Timezone     string              `json:"timezone,omitempty"`
	Revision     int                 `json:"revision"`
	UpdatedAt    time.Time           `json:"updated_at"`
	Batteries    []BatteryConfig     `json:"batteries"`
	Schedules    []Schedule          `json:"schedules"`
	EmailReports *EmailReportsConfig `json:"email_reports,omitempty"`
}

// Email delivery kinds a recipient can subscribe to.
const (
	EmailKindDaily   = "daily"
	EmailKindWeekly  = "weekly"
	EmailKindMonthly = "monthly"
	EmailKindAlerts  = "alerts"
)

// EmailRecipient is one address and what it is subscribed to. Subscriptions are
// per address because the people who want a monthly summary are not necessarily
// the people who should hear that a device stopped reporting at 3am.
type EmailRecipient struct {
	Address string `json:"address"`
	Daily   bool   `json:"daily"`
	Weekly  bool   `json:"weekly"`
	Monthly bool   `json:"monthly"`
	Alerts  bool   `json:"alerts"`
}

// WantsKind reports whether this recipient is subscribed to a delivery kind.
func (r EmailRecipient) WantsKind(kind string) bool {
	switch kind {
	case EmailKindDaily:
		return r.Daily
	case EmailKindWeekly:
		return r.Weekly
	case EmailKindMonthly:
		return r.Monthly
	case EmailKindAlerts:
		return r.Alerts
	default:
		return false
	}
}

// EmailReportsConfig holds per-agent email delivery settings, edited from the web UI
// and consumed by the control server's email scheduler. The agent ignores this field.
// Enabled and the Daily/Weekly/Monthly flags are the master switches for the agent;
// each recipient then opts in to the kinds they want.
type EmailReportsConfig struct {
	Enabled    bool             `json:"enabled"`
	Recipients []EmailRecipient `json:"recipients"`
	Daily      bool             `json:"daily"`
	Weekly     bool             `json:"weekly"`    // sent Monday for Mon–Sun prior
	Monthly    bool             `json:"monthly"`   // sent on day 1 for previous month
	SendHour   int              `json:"send_hour"` // 0–23, in agent timezone
}

// UnmarshalJSON accepts both the current recipient objects and the original
// plain-string list, so a stored config written before per-recipient
// subscriptions existed keeps delivering exactly what it delivered before.
// Migrated addresses inherit the agent's report toggles and are NOT subscribed
// to alerts: nobody opted in to those, and mailing them by surprise is worse
// than a missing alert.
func (c *EmailReportsConfig) UnmarshalJSON(data []byte) error {
	type alias EmailReportsConfig
	var raw struct {
		alias
		Recipients []json.RawMessage `json:"recipients"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	*c = EmailReportsConfig(raw.alias)
	c.Recipients = nil

	for _, item := range raw.Recipients {
		trimmed := strings.TrimSpace(string(item))
		if trimmed == "" || trimmed == "null" {
			continue
		}
		if trimmed[0] == '"' {
			var addr string
			if err := json.Unmarshal(item, &addr); err != nil {
				return fmt.Errorf("decode recipient: %w", err)
			}
			if addr = strings.TrimSpace(addr); addr == "" {
				continue
			}
			c.Recipients = append(c.Recipients, EmailRecipient{
				Address: addr,
				Daily:   raw.Daily,
				Weekly:  raw.Weekly,
				Monthly: raw.Monthly,
			})
			continue
		}
		var r EmailRecipient
		if err := json.Unmarshal(item, &r); err != nil {
			return fmt.Errorf("decode recipient: %w", err)
		}
		if r.Address = strings.TrimSpace(r.Address); r.Address == "" {
			continue
		}
		c.Recipients = append(c.Recipients, r)
	}
	return nil
}

// RecipientsFor returns the addresses subscribed to one delivery kind.
func (c *EmailReportsConfig) RecipientsFor(kind string) []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.Recipients))
	for _, r := range c.Recipients {
		if addr := strings.TrimSpace(r.Address); addr != "" && r.WantsKind(kind) {
			out = append(out, addr)
		}
	}
	return out
}

// CloneEmailReports returns a deep copy of the email reports config (or nil).
func CloneEmailReports(in *EmailReportsConfig) *EmailReportsConfig {
	if in == nil {
		return nil
	}
	out := *in
	if in.Recipients != nil {
		out.Recipients = make([]EmailRecipient, len(in.Recipients))
		copy(out.Recipients, in.Recipients)
	}
	return &out
}

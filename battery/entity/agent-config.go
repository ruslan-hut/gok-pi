package entity

import "time"

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

// EmailReportsConfig holds per-agent email report delivery settings, edited from the web UI
// and consumed by the control server's email scheduler. The agent ignores this field.
type EmailReportsConfig struct {
	Enabled    bool     `json:"enabled"`
	Recipients []string `json:"recipients"`
	Daily      bool     `json:"daily"`
	Weekly     bool     `json:"weekly"`    // sent Monday for Mon–Sun prior
	Monthly    bool     `json:"monthly"`   // sent on day 1 for previous month
	SendHour   int      `json:"send_hour"` // 0–23, in agent timezone
}

// CloneEmailReports returns a deep copy of the email reports config (or nil).
func CloneEmailReports(in *EmailReportsConfig) *EmailReportsConfig {
	if in == nil {
		return nil
	}
	out := *in
	if in.Recipients != nil {
		out.Recipients = make([]string, len(in.Recipients))
		copy(out.Recipients, in.Recipients)
	}
	return &out
}

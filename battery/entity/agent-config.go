package entity

import "time"

// AgentConfig represents the configuration for a single agent, used in the WebSocket
// protocol between agents and the control server.
type AgentConfig struct {
	DeviceName string          `json:"device_name,omitempty"`
	Env        string          `json:"env,omitempty"`
	Timezone   string          `json:"timezone,omitempty"`
	Revision   int             `json:"revision"`
	UpdatedAt  time.Time       `json:"updated_at"`
	Batteries  []BatteryConfig `json:"batteries"`
	Schedules  []Schedule      `json:"schedules"`
}

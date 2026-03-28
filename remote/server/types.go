// types.go defines the WebSocket protocol message types and HTTP API request/response
// structures shared between agents, the control server, and the web UI.
//
// All WebSocket messages include a "type" field for routing and a "sent_at"/"timestamp"
// field for ordering. JSON field names use snake_case to match the React UI conventions.

package server

import (
	"encoding/json"
	"time"

	"gok-pi/battery/entity"
	"gok-pi/metrics/observers"
)

// HTTP header keys used for agent authentication and identification during WebSocket upgrade.
const (
	headerSharedSecret = "X-GOK-Shared-Secret" // Shared secret for agent auth
	headerAgentID      = "X-GOK-Agent-ID"      // Agent's unique device ID
	headerAgentEnv     = "X-GOK-Agent-Env"     // Agent's environment label (e.g., "production")
)

// Config holds the control server configuration, typically populated from CLI flags or YAML.
type Config struct {
	SharedSecret string // If set, agents must present this secret to connect
	UIStaticDir  string // Path to React UI build output (web/ui/dist)
	AgentBinary  string // Optional: explicit agent binary name in downloads dir
	VersionFile  string // Name of the VERSION manifest file (default: "VERSION")
	ConfigStore  string // Path to agent-configs.json persistence file
	SessionDB    string // Path to SQLite session database
	UIUsername   string // Optional: username for UI login (empty = no auth)
	UIPassword   string // Optional: password for UI login
}

// TelemetrySnapshot is an alias for observers.Snapshot to avoid duplicating the struct definition.
type TelemetrySnapshot = observers.Snapshot

type AgentHello struct {
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Agent     AgentDescriptor `json:"agent"`
}

type AgentDescriptor struct {
	ID       string `json:"id"`
	Env      string `json:"env"`
	Hostname string `json:"hostname"`
	Version  string `json:"version"`
}

type AgentTelemetry struct {
	Type      string            `json:"type"`
	Timestamp time.Time         `json:"timestamp"`
	Agent     AgentDescriptor   `json:"agent"`
	Snapshot  TelemetrySnapshot `json:"snapshot"`
}

type AgentHeartbeat struct {
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Agent     AgentDescriptor `json:"agent"`
}

type AgentMessage struct {
	Type string `json:"type"`
}

type CommandRequest struct {
	Command string          `json:"command"`
	Target  string          `json:"target"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type OutgoingCommand struct {
	Type      string          `json:"type"`
	Command   string          `json:"command"`
	Target    string          `json:"target"`
	RequestID string          `json:"request_id"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type ConfigPush struct {
	Type    string      `json:"type"`
	AgentID string      `json:"agent_id"`
	Config  AgentConfig `json:"config"`
	SentAt  time.Time   `json:"sent_at"`
	Request string      `json:"request_id,omitempty"`
}

type UIConfigUpdate struct {
	Type    string      `json:"type"`
	AgentID string      `json:"agent_id"`
	Config  AgentConfig `json:"config"`
	SentAt  time.Time   `json:"sent_at"`
	Message string      `json:"message"`
}

type AgentSummary struct {
	Agent               AgentDescriptor              `json:"agent"`
	LastSeen            time.Time                    `json:"last_seen"`
	Telemetry           map[string]TelemetrySnapshot `json:"telemetry"`
	ScheduleGoalReached map[string]time.Time         `json:"schedule_goal_reached,omitempty"`
}

type AgentConfigSnapshot struct {
	Batteries           []entity.BatteryConfig `json:"batteries"`
	Schedules           []entity.Schedule      `json:"schedules"`
	ChargeSchedules     []entity.Schedule      `json:"charge_schedules"`
	ScheduleGoalReached map[string]time.Time   `json:"schedule_goal_reached,omitempty"`
}

type AgentConfigSync struct {
	Type   string              `json:"type"`
	Config AgentConfigSnapshot `json:"config"`
	SentAt time.Time           `json:"sent_at"`
}

type UITelemetryBroadcast struct {
	Type     string            `json:"type"`
	AgentID  string            `json:"agent_id"`
	Snapshot TelemetrySnapshot `json:"snapshot"`
	SentAt   time.Time         `json:"sent_at"`
}

type UIAgentSummaryBroadcast struct {
	Type    string       `json:"type"`
	Agent   AgentSummary `json:"agent"`
	SentAt  time.Time    `json:"sent_at"`
	Message string       `json:"message"`
}

type UIAgentRemovedBroadcast struct {
	Type    string    `json:"type"`
	AgentID string    `json:"agent_id"`
	SentAt  time.Time `json:"sent_at"`
}

type AgentLogResponse struct {
	Type      string    `json:"type"`
	RequestID string    `json:"request_id"`
	Logs      string    `json:"logs"`
	Error     string    `json:"error,omitempty"`
	SentAt    time.Time `json:"sent_at"`
}

type LogRequest struct {
	Lines  int    `json:"lines,omitempty"`
	Stream string `json:"stream,omitempty"` // "agent" or "updater"
}

// PriceLimits defines absolute price thresholds for auto-schedule filtering.
// A value of 0 means "no limit" (feature disabled for that direction).
type PriceLimits struct {
	ChargeLimitEurMWh    float64 `json:"charge_limit_eur_mwh"`    // max price to allow charging; 0 = no limit
	DischargeLimitEurMWh float64 `json:"discharge_limit_eur_mwh"` // min price to allow discharging; 0 = no limit
}

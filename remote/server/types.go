package server

import (
	"encoding/json"
	"time"

	"gok-pi/battery/entity"
)

const (
	headerSharedSecret = "X-GOK-Shared-Secret"
	headerAgentID      = "X-GOK-Agent-ID"
	headerAgentEnv     = "X-GOK-Agent-Env"
)

type Config struct {
	SharedSecret string
	UIStaticDir  string
	AgentBinary  string
	VersionFile  string
	ConfigStore  string
}

type TelemetrySnapshot struct {
	Name                  string    `json:"name"`
	RSOC                  float64   `json:"rsoc"`
	USOC                  float64   `json:"usoc"`
	RemainingCapacityWh   float64   `json:"remaining_capacity_wh"`
	ConsumptionW          float64   `json:"consumption_w"`
	PacTotalW             float64   `json:"pac_total_w"`
	BatteryDischarging    bool      `json:"battery_discharging"`
	BatteryDischargingSet bool      `json:"battery_discharging_set"`
	OperatingMode         string    `json:"operating_mode"`
	OperatingModeSet      bool      `json:"operating_mode_set"`
	UpdatedAt             time.Time `json:"updated_at"`
}

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
	Agent     AgentDescriptor              `json:"agent"`
	LastSeen  time.Time                    `json:"last_seen"`
	Telemetry map[string]TelemetrySnapshot `json:"telemetry"`
}

type AgentConfigSnapshot struct {
	Batteries []entity.BatteryConfig `json:"batteries"`
	Schedules []entity.Schedule      `json:"schedules"`
}

type AgentConfigSync struct {
	Type   string              `json:"type"`
	Config AgentConfigSnapshot `json:"config"`
	SentAt time.Time           `json:"sent_at"`
}

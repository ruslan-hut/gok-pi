export interface AgentDescriptor {
  id: string;
  env: string;
  hostname: string;
  version: string;
}

export interface TelemetrySnapshot {
  name: string;
  rsoc: number;
  usoc: number;
  remaining_capacity_wh: number;
  consumption_w: number;
  pac_total_w: number;
  battery_discharging: boolean;
  battery_discharging_set: boolean;
  battery_charging?: boolean;
  battery_charging_set?: boolean;
  operating_mode: string;
  operating_mode_set: boolean;
  status: string;
  updated_at: string;
}

export interface AgentSummary {
  agent: AgentDescriptor;
  last_seen: string;
  telemetry: Record<string, TelemetrySnapshot>;
  connected?: boolean;
}

export interface BatteryConfig {
  name: string;
  url: string;
  token: string;
  enabled: boolean;
  capacity_limit: number;
}

export interface ScheduleConfig {
  name?: string;
  type?: string; // "charge" or "discharge" (defaults to "discharge")
  start_time: string;
  stop_time: string;
  battery_name: string;
  enabled: boolean;
  power_limit: number;
  soc_limit: number;
}

export interface AgentConfig {
  device_name?: string;
  revision: number;
  updated_at: string;
  batteries: BatteryConfig[];
  schedules: ScheduleConfig[];
}

export interface AgentsSnapshotMessage {
  type: "agents.snapshot";
  agents: AgentSummary[];
}

export interface AgentTelemetryMessage {
  type: "agent.telemetry";
  agent_id: string;
  snapshot: TelemetrySnapshot;
}

export interface AgentSummaryMessage {
  type: "agent.summary";
  agent: AgentSummary;
}

export interface AgentRemovedMessage {
  type: "agent.removed";
  agent_id: string;
}

export interface ConfigUpdatedMessage {
  type: "config.updated";
  agent_id: string;
  config: AgentConfig;
  sent_at: string;
  message: string;
}

export type DashboardMessage =
  | AgentsSnapshotMessage
  | AgentTelemetryMessage
  | AgentSummaryMessage
  | AgentRemovedMessage
  | ConfigUpdatedMessage;


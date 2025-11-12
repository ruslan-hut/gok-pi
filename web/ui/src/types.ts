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
  operating_mode: string;
  operating_mode_set: boolean;
  updated_at: string;
}

export interface AgentSummary {
  agent: AgentDescriptor;
  last_seen: string;
  telemetry: Record<string, TelemetrySnapshot>;
  connected?: boolean;
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

export type DashboardMessage =
  | AgentsSnapshotMessage
  | AgentTelemetryMessage
  | AgentSummaryMessage
  | AgentRemovedMessage;


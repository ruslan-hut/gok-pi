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
  schedule_goal_reached?: Record<string, string>;
  connected?: boolean;
}

export interface BatteryConfig {
  name: string;
  driver?: string;
  url: string;
  token: string;
  enabled: boolean;
  auto_schedule?: boolean;
  capacity_limit: number;
  power_limit?: number;  // Default power limit in W (used when no schedule is active)
  soc_limit?: number;    // Default SoC limit in % (used when no schedule is active)
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
  run_once?: boolean;
  goal_reached_time?: string; // ISO timestamp, set when run_once goal is reached
}

export interface AgentConfig {
  device_name?: string;
  env?: string;
  timezone?: string;
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

// Electricity prices types

export interface HourlyPrice {
  datetime: string;
  hour: number;
  price_eur_mwh: number;
}

export interface ScheduleWindow {
  start_hour: number;
  end_hour: number;
  avg_price_eur_mwh: number;
}

export interface DaySchedule {
  charge_windows: ScheduleWindow[] | null;
  discharge_windows: ScheduleWindow[] | null;
}

export interface PriceStats {
  min_price_eur_mwh: number;
  max_price_eur_mwh: number;
  avg_price_eur_mwh: number;
  low_eur_mwh: number;         // low percentile threshold — charge below this
  high_eur_mwh: number;        // high percentile threshold — discharge above this
  charge_percentile: number;   // e.g. 20 (means P20)
  discharge_percentile: number; // e.g. 80 (means P80)
}

export interface DayData {
  date: string;
  prices: HourlyPrice[];
  schedule: DaySchedule;
  stats: PriceStats;
}

export interface PricesState {
  today: DayData | null;
  tomorrow: DayData | null;
  last_updated: string;
  last_error?: string;
  next_update: string;
}

// Dashboard UI types

export type AgentSummaryWithDeviceName = AgentSummary & {
  device_name?: string;
};

export type AgentsMap = Record<string, AgentSummaryWithDeviceName>;

export interface CommandState {
  power: number;
  powerLimit: number;
  socLimit: number;
}

export type TabId = "monitor" | "prices" | "configure" | "tools";

export type DeviceTab = "monitor" | "configure" | "tools";

export type AppPage =
  | { page: "overview" }
  | { page: "electricity" }
  | { page: "database" }
  | { page: "device"; agentId: string; tab: DeviceTab };

// Database stats types

export interface DBStats {
  file_size_bytes: number;
  total_sessions: number;
  open_sessions: number;
  oldest_session?: string;
  newest_session?: string;
}

export interface AgentDBStats {
  agent_id: string;
  total_sessions: number;
  charge_sessions: number;
  discharge_sessions: number;
  total_energy_wh: number;
  net_cost_eur: number;
}

export interface DBStatsResponse {
  stats: DBStats | null;
  agents: AgentDBStats[];
}

// Charging session types

export interface SessionRecord {
  id: number;
  battery_name: string;
  agent_id: string;
  type: "charge" | "discharge";
  started_at: string;
  ended_at?: string;
  duration_seconds?: number;
  energy_wh: number;
  avg_power_w: number;
  peak_power_w: number;
  soc_start: number;
  soc_end: number;
  avg_price_eur_mwh: number;
  cost_eur: number;
  samples: number;
}

export interface BatterySummary {
  battery_name: string;
  charge_energy_wh: number;
  charge_cost_eur: number;
  discharge_energy_wh: number;
  discharge_cost_eur: number;
  net_cost_eur: number;
  active_charge: boolean;
  active_discharge: boolean;
}

export interface SessionsResponse {
  summaries: BatterySummary[];
  sessions: SessionRecord[];
}

// Database inspector types

export interface DBRecordsQuery {
  agent_id?: string;
  type?: string;
  date_from?: string;
  date_to?: string;
  status?: string;
  limit?: number;
  offset?: number;
}

export interface DBRecordsResponse {
  records: SessionRecord[];
  total: number;
  limit: number;
  offset: number;
  agents: string[];
}


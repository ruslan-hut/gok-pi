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
  // Why the battery runs outside its schedule: "charger" for an EV charging
  // session, "manual" for an operator command, absent when schedule-driven.
  override_source?: string;
  override_direction?: string;
  updated_at: string;
}

/** ChargerLink maps an evsys charging location to one battery on one agent. */
export interface ChargerLink {
  name: string;
  enabled: boolean;
  location_id: string;
  charge_point_ids: string[];
  agent_id: string;
  battery_name: string;
  power_limit: number;
  soc_limit: number;
  max_duration_min: number;
}

/** ChargerSession is an EV charging session currently driving a discharge. */
export interface ChargerSession {
  key: string;
  link_name: string;
  agent_id: string;
  battery_name: string;
  location_id: string;
  charge_point_id: string;
  connector_id: number;
  transaction_id: number;
  id_tag: string;
  username: string;
  started_at: string;
  power_limit: number;
  soc_limit: number;
  max_duration_min: number;
}

export interface AgentSummary {
  agent: AgentDescriptor;
  last_seen: string;
  // last_seen is refreshed by the agent's 30s heartbeat as well, so it cannot
  // tell a healthy agent from one whose telemetry stopped. These three describe
  // the telemetry stream on its own.
  last_telemetry_at?: string;
  telemetry_frames?: number;
  telemetry_stalled?: boolean;
  // The agent's own view of its uplink, from its last heartbeat. Where
  // telemetry_stalled says the server is receiving nothing, this says whether the
  // agent is holding telemetry it cannot deliver — the two have different fixes.
  spool?: AgentSpoolHealth;
  telemetry: Record<string, TelemetrySnapshot>;
  schedule_goal_reached?: Record<string, string>;
  connected?: boolean;
}

/**
 * AgentSpoolHealth is the agent's durable telemetry buffer as of its last
 * heartbeat. pending is normally a handful of snapshots in flight;
 * oldest_undelivered_age_sec is the one that matters — telemetry is flushed
 * within seconds of being recorded, so a backlog measured in minutes means the
 * uplink has stopped draining.
 */
export interface AgentSpoolHealth {
  pending: number;
  oldest_undelivered_age_sec?: number;
  appended: number;
  delivered: number;
  flush_errors?: number;
  last_delivery_at?: string;
  last_error?: string;
}

/**
 * TelemetryPoint is one minute of recorded history for a battery. samples is how
 * many frames the agent actually delivered for that minute — six is a complete
 * minute at the 10s poll interval, so a lower count marks telemetry that was lost
 * rather than a battery that was idle.
 */
export interface TelemetryPoint {
  agent_id: string;
  battery_name: string;
  bucket: string;
  usoc: number;
  rsoc: number;
  remaining_capacity_wh: number;
  consumption_avg_w: number;
  consumption_max_w: number;
  pac_avg_w: number;
  pac_min_w: number;
  pac_max_w: number;
  operating_mode: string;
  samples: number;
}

export interface HistoryResponse {
  points: TelemetryPoint[];
  since: string;
  until?: string;
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

/** One address and the deliveries it is subscribed to. */
export interface EmailRecipient {
  address: string;
  daily: boolean;
  weekly: boolean;
  monthly: boolean;
  /** Offline and recovery notices — opted into separately from the reports. */
  alerts: boolean;
}

export interface EmailReportsConfig {
  enabled: boolean;
  recipients: EmailRecipient[];
  daily: boolean;
  weekly: boolean;
  monthly: boolean;
  send_hour: number;
}

export interface AgentConfig {
  device_name?: string;
  env?: string;
  timezone?: string;
  revision: number;
  updated_at: string;
  batteries: BatteryConfig[];
  schedules: ScheduleConfig[];
  email_reports?: EmailReportsConfig | null;
}

export interface EmailProviderStatus {
  enabled: boolean;
  configured: boolean;
  sender?: string;
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

export interface PriceLimits {
  charge_limit_eur_mwh: number;    // max price to allow charging; 0 = no limit
  discharge_limit_eur_mwh: number; // min price to allow discharging; 0 = no limit
}

export interface PricesState {
  today: DayData | null;
  tomorrow: DayData | null;
  last_updated: string;
  last_error?: string;
  next_update: string;
  price_limits: PriceLimits;
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


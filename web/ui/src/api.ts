import type { AgentConfig, AgentSummary, ChargerLink, ChargerSession, DBRecordsQuery, DBRecordsResponse, DBStatsResponse, EmailProviderStatus, HistoryResponse, PriceLimits, PricesState, SessionsResponse } from "./types";

const AUTH_TOKEN_KEY = "gok-pi-auth-token";
const AUTH_EXPIRY_KEY = "gok-pi-auth-expires";

// wsTokenParam returns "?token=<token>" for authenticating the UI WebSocket, or
// "" when no token is stored (server is fail-open when auth is unconfigured).
export function wsTokenParam(): string {
  const token = getAuthToken();
  return token ? `?token=${encodeURIComponent(token)}` : "";
}

export function getAuthToken(): string | null {
  const token = localStorage.getItem(AUTH_TOKEN_KEY);
  if (!token) return null;
  const expiry = localStorage.getItem(AUTH_EXPIRY_KEY);
  if (expiry && Date.now() > new Date(expiry).getTime()) {
    clearAuthToken();
    return null;
  }
  return token;
}

export function setAuthToken(token: string, expiresAt?: string) {
  if (token) {
    localStorage.setItem(AUTH_TOKEN_KEY, token);
    if (expiresAt) localStorage.setItem(AUTH_EXPIRY_KEY, expiresAt);
  } else {
    clearAuthToken();
  }
}

export function clearAuthToken() {
  localStorage.removeItem(AUTH_TOKEN_KEY);
  localStorage.removeItem(AUTH_EXPIRY_KEY);
}

export interface LoginResponse {
  token: string;
  expires_at: string;
}

export async function login(
  username: string,
  password: string,
): Promise<LoginResponse> {
  const res = await fetch("/api/login", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ username, password }),
  });
  if (!res.ok) {
    if (res.status === 401) {
      throw new Error("Invalid username or password");
    }
    throw new Error(`Login failed: ${res.statusText}`);
  }
  return (await res.json()) as LoginResponse;
}

const headerAuthToken = "X-Auth-Token";

function getAuthHeaders(): Record<string, string> {
  const token = getAuthToken();
  if (token) {
    return { [headerAuthToken]: token };
  }
  return {};
}

export async function fetchAgents(): Promise<Record<string, AgentSummary>> {
  const res = await fetch("/api/agents", { headers: { ...getAuthHeaders() } });
  if (!res.ok) {
    throw new Error(`Failed to load agents: ${res.statusText}`);
  }
  const data = (await res.json()) as AgentSummary[];
  const map: Record<string, AgentSummary> = {};
  for (const agent of data) {
    map[agent.agent.id] = { ...agent, connected: true };
  }
  return map;
}

export async function sendCommand(
  agentId: string,
  command: string,
  target: string,
  payload?: unknown,
): Promise<void> {
  const body: Record<string, unknown> = {
    command,
    target,
  };
  if (payload !== undefined) {
    body.payload = payload;
  }
  const res = await fetch(`/api/agents/${encodeURIComponent(agentId)}`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      ...getAuthHeaders(),
    },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    if (res.status === 401) {
      throw new Error("Login required to send commands.");
    }
    const text = await res.text();
    throw new Error(text || "Command failed");
  }
}

export async function fetchAgentConfig(
  agentId: string,
): Promise<AgentConfig | null> {
  const res = await fetch(
    `/api/agents/${encodeURIComponent(agentId)}/config`,
    {
      headers: { "Accept": "application/json", ...getAuthHeaders() },
    },
  );
  if (res.status === 404) {
    return null;
  }
  if (!res.ok) {
    throw new Error(`Failed to load config: ${res.statusText}`);
  }
  return (await res.json()) as AgentConfig;
}

export interface AgentConfigPayload {
  device_name?: string;
  env?: string;
  timezone?: string;
  revision: number;
  batteries: AgentConfig["batteries"];
  schedules: AgentConfig["schedules"];
  email_reports?: AgentConfig["email_reports"];
}

export async function updateAgentConfig(
  agentId: string,
  payload: AgentConfigPayload,
): Promise<AgentConfig> {
  const res = await fetch(
    `/api/agents/${encodeURIComponent(agentId)}/config`,
    {
      method: "PUT",
      headers: {
        "Content-Type": "application/json",
        ...getAuthHeaders(),
      },
      body: JSON.stringify(payload),
    },
  );
  if (!res.ok) {
    if (res.status === 401) {
      throw new Error("Login required to update configuration.");
    }
    const text = await res.text();
    throw new Error(text || "Failed to update config");
  }
  return (await res.json()) as AgentConfig;
}

export interface LogRequest {
  lines?: number;
  stream?: string; // "agent" or "updater"
}

export async function fetchAgentLogs(
  agentId: string,
  options?: LogRequest,
): Promise<string> {
  const params = new URLSearchParams();
  if (options?.lines !== undefined) {
    params.set("lines", options.lines.toString());
  }
  if (options?.stream) {
    params.set("stream", options.stream);
  }

  const url = `/api/agents/${encodeURIComponent(agentId)}/logs${params.toString() ? `?${params.toString()}` : ""}`;
  const res = await fetch(url, { headers: { ...getAuthHeaders() } });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || "Failed to fetch logs");
  }
  return await res.text();
}

export async function fetchPrices(): Promise<PricesState> {
  const res = await fetch("/api/prices");
  if (!res.ok) {
    throw new Error(`Failed to load prices: ${res.statusText}`);
  }
  return (await res.json()) as PricesState;
}

export async function fetchSessions(opts?: {
  agentId?: string;
  hours?: number;
  since?: string;
  until?: string;
}): Promise<SessionsResponse> {
  const params = new URLSearchParams();
  if (opts?.agentId) params.set("agent_id", opts.agentId);
  if (opts?.since) params.set("since", opts.since);
  if (opts?.until) params.set("until", opts.until);
  if (opts?.hours) params.set("hours", opts.hours.toString());
  const qs = params.toString();
  const res = await fetch(`/api/sessions${qs ? `?${qs}` : ""}`);
  if (!res.ok) {
    throw new Error(`Failed to load sessions: ${res.statusText}`);
  }
  return (await res.json()) as SessionsResponse;
}

export async function fetchHistory(opts: {
  agentId?: string;
  battery?: string;
  hours?: number;
  since?: string;
  until?: string;
}): Promise<HistoryResponse> {
  const params = new URLSearchParams();
  if (opts.agentId) params.set("agent_id", opts.agentId);
  if (opts.battery) params.set("battery", opts.battery);
  if (opts.since) params.set("since", opts.since);
  if (opts.until) params.set("until", opts.until);
  if (opts.hours) params.set("hours", opts.hours.toString());
  const qs = params.toString();
  const res = await fetch(`/api/history${qs ? `?${qs}` : ""}`);
  if (!res.ok) {
    throw new Error(`Failed to load history: ${res.statusText}`);
  }
  return (await res.json()) as HistoryResponse;
}

export async function savePriceLimits(limits: PriceLimits): Promise<PriceLimits> {
  const token = getAuthToken();
  const res = await fetch("/api/price-limits", {
    method: "PUT",
    headers: {
      "Content-Type": "application/json",
      ...(token ? { "X-Auth-Token": token } : {}),
    },
    body: JSON.stringify(limits),
  });
  if (!res.ok) {
    throw new Error(`Failed to save price limits: ${res.statusText}`);
  }
  return (await res.json()) as PriceLimits;
}

export function exportPricesURL(start: string, end: string): string {
  return `/api/prices/export?start=${encodeURIComponent(start)}&end=${encodeURIComponent(end)}`;
}

export async function fetchEmailStatus(): Promise<EmailProviderStatus> {
  const res = await fetch("/api/email/status");
  if (!res.ok) {
    throw new Error(`Failed to load email status: ${res.statusText}`);
  }
  return (await res.json()) as EmailProviderStatus;
}

export interface EmailTestResponse {
  sent: boolean;
  recipients: string[];
}

export async function sendEmailTest(
  agentId: string,
  recipients: string[],
): Promise<EmailTestResponse> {
  const res = await fetch(
    `/api/agents/${encodeURIComponent(agentId)}/email-test`,
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        ...getAuthHeaders(),
      },
      body: JSON.stringify({ recipients }),
    },
  );
  if (!res.ok) {
    if (res.status === 401) {
      throw new Error("Login required to send test emails.");
    }
    const text = await res.text();
    throw new Error(text || `Failed to send test email: ${res.statusText}`);
  }
  return (await res.json()) as EmailTestResponse;
}

export async function fetchChargerLinks(): Promise<ChargerLink[]> {
  const res = await fetch("/api/charger-links", { headers: { ...getAuthHeaders() } });
  if (!res.ok) {
    throw new Error(`Failed to load charger links: ${res.statusText}`);
  }
  return (await res.json()) as ChargerLink[];
}

/**
 * saveChargerLinks replaces the whole link set. Callers editing one agent's
 * links must merge them back into the full list first — the server stores a
 * single global set.
 */
export async function saveChargerLinks(links: ChargerLink[]): Promise<ChargerLink[]> {
  const res = await fetch("/api/charger-links", {
    method: "PUT",
    headers: {
      "Content-Type": "application/json",
      ...getAuthHeaders(),
    },
    body: JSON.stringify(links),
  });
  if (!res.ok) {
    if (res.status === 401) {
      throw new Error("Login required to edit charger links.");
    }
    const text = await res.text();
    throw new Error(text || "Failed to save charger links");
  }
  return (await res.json()) as ChargerLink[];
}

export async function fetchChargerSessions(): Promise<ChargerSession[]> {
  const res = await fetch("/api/charger-sessions");
  if (!res.ok) {
    throw new Error(`Failed to load charger sessions: ${res.statusText}`);
  }
  return (await res.json()) as ChargerSession[];
}

export async function fetchDBStats(): Promise<DBStatsResponse> {
  const res = await fetch("/api/db-stats");
  if (!res.ok) {
    throw new Error(`Failed to load DB stats: ${res.statusText}`);
  }
  return (await res.json()) as DBStatsResponse;
}

export async function fetchDBRecords(query: DBRecordsQuery): Promise<DBRecordsResponse> {
  const params = new URLSearchParams();
  if (query.agent_id) params.set("agent_id", query.agent_id);
  if (query.type) params.set("type", query.type);
  if (query.date_from) params.set("date_from", query.date_from);
  if (query.date_to) params.set("date_to", query.date_to);
  if (query.status) params.set("status", query.status);
  if (query.limit) params.set("limit", query.limit.toString());
  if (query.offset) params.set("offset", query.offset.toString());
  const qs = params.toString();
  const res = await fetch(`/api/db/records${qs ? `?${qs}` : ""}`, {
    headers: { ...getAuthHeaders() },
  });
  if (!res.ok) {
    if (res.status === 401) {
      throw new Error("Login required to access database inspector.");
    }
    throw new Error(`Failed to load records: ${res.statusText}`);
  }
  return (await res.json()) as DBRecordsResponse;
}


/**
 * restartAgent asks the agent to shut down and let systemd start it again. The
 * server answers as soon as the command is queued — there is no reply to wait
 * for, since a successful restart looks exactly like the socket dropping.
 */
export async function restartAgent(agentId: string): Promise<void> {
  const res = await fetch(`/api/agents/${encodeURIComponent(agentId)}/restart`, {
    method: "POST",
    headers: { ...getAuthHeaders() },
  });
  if (!res.ok) {
    if (res.status === 401) {
      throw new Error("Login required to restart the agent.");
    }
    if (res.status === 404) {
      throw new Error("Agent is not connected.");
    }
    const text = await res.text();
    throw new Error(text || `Restart failed: ${res.statusText}`);
  }
}

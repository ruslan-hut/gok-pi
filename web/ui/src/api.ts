import type { AgentConfig, AgentSummary, ChargingSession, PricesState } from "./types";

const AUTH_TOKEN_KEY = "gok-pi-auth-token";

export function getAuthToken(): string | null {
  return localStorage.getItem(AUTH_TOKEN_KEY);
}

export function setAuthToken(token: string) {
  if (token) {
    localStorage.setItem(AUTH_TOKEN_KEY, token);
  } else {
    localStorage.removeItem(AUTH_TOKEN_KEY);
  }
}

export function clearAuthToken() {
  localStorage.removeItem(AUTH_TOKEN_KEY);
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
  const res = await fetch("/api/agents", {
    headers: getAuthHeaders(),
  });
  if (!res.ok) {
    if (res.status === 401) {
      clearAuthToken();
      throw new Error("Unauthorized. Please log in again.");
    }
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
      clearAuthToken();
      throw new Error("Unauthorized. Please log in again.");
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
      headers: {
        "Accept": "application/json",
        ...getAuthHeaders(),
      },
    },
  );
  if (res.status === 404) {
    return null;
  }
  if (!res.ok) {
    if (res.status === 401) {
      clearAuthToken();
      throw new Error("Unauthorized. Please log in again.");
    }
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
      clearAuthToken();
      throw new Error("Unauthorized. Please log in again.");
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
  const res = await fetch(url, {
    headers: getAuthHeaders(),
  });
  if (!res.ok) {
    if (res.status === 401) {
      clearAuthToken();
      throw new Error("Unauthorized. Please log in again.");
    }
    const text = await res.text();
    throw new Error(text || "Failed to fetch logs");
  }
  return await res.text();
}

export async function fetchPrices(): Promise<PricesState> {
  const res = await fetch("/api/prices", {
    headers: getAuthHeaders(),
  });
  if (!res.ok) {
    if (res.status === 401) {
      clearAuthToken();
      throw new Error("Unauthorized. Please log in again.");
    }
    throw new Error(`Failed to load prices: ${res.statusText}`);
  }
  return (await res.json()) as PricesState;
}

export async function fetchSessions(agentId?: string): Promise<ChargingSession[]> {
  const params = agentId ? `?agent_id=${encodeURIComponent(agentId)}` : "";
  const res = await fetch(`/api/sessions${params}`, {
    headers: getAuthHeaders(),
  });
  if (!res.ok) {
    if (res.status === 401) {
      clearAuthToken();
      throw new Error("Unauthorized. Please log in again.");
    }
    throw new Error(`Failed to load sessions: ${res.statusText}`);
  }
  return (await res.json()) as ChargingSession[];
}


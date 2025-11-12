import type { AgentConfig, AgentSummary } from "./types";

export async function fetchAgents(): Promise<Record<string, AgentSummary>> {
  const res = await fetch("/api/agents");
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
    },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
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
      },
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
      },
      body: JSON.stringify(payload),
    },
  );
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || "Failed to update config");
  }
  return (await res.json()) as AgentConfig;
}


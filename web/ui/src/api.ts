import type { AgentSummary } from "./types";

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
  const res = await fetch(`/api/agents/${encodeURIComponent(agentId)}/command`, {
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


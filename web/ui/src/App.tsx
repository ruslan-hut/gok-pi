import { useEffect, useMemo, useState } from "react";
import { fetchAgents, sendCommand } from "./api";
import type {
  AgentSummary,
  DashboardMessage,
  TelemetrySnapshot,
} from "./types";

type AgentsMap = Record<string, AgentSummary>;

interface CommandState {
  power: number;
  powerLimit: number;
  socLimit: number;
}

const defaultCommandState: CommandState = {
  power: 500,
  powerLimit: 500,
  socLimit: 50,
};

const OFFLINE_GRACE_MS = 2 * 60 * 1000;

function computeConnectionStatus(agent: AgentSummary): boolean {
  const lastSeen = new Date(agent.last_seen).getTime();
  if (Number.isNaN(lastSeen)) {
    return false;
  }
  return Date.now() - lastSeen <= OFFLINE_GRACE_MS;
}

function getWsUrl(): string {
  const protocol = window.location.protocol === "https:" ? "wss" : "ws";
  const host = window.location.host;
  return `${protocol}://${host}/api/ui`;
}

export default function App() {
  const [agents, setAgents] = useState<AgentsMap>({});
  const [selectedAgentId, setSelectedAgentId] = useState<string>();
  const [connectionActive, setConnectionActive] = useState(false);
  const [commandState, setCommandState] =
    useState<CommandState>(defaultCommandState);
  const [message, setMessage] = useState<string>();

  useEffect(() => {
    fetchAgents()
      .then((data) => {
        setAgents((prev) => {
          const next: AgentsMap = { ...prev };
          Object.entries(data).forEach(([id, agent]) => {
            next[id] = {
              ...(prev[id] ?? agent),
              ...agent,
              connected: computeConnectionStatus(agent),
            };
          });
          return next;
        });
        if (!selectedAgentId) {
          const firstAgent = Object.values(data)[0];
          if (firstAgent) {
            setSelectedAgentId(firstAgent.agent.id);
          }
        }
      })
      .catch((err) => setMessage(err.message));
  }, [selectedAgentId]);

  useEffect(() => {
    let isActive = true;
    let retryMs = 1000;
    let socket: WebSocket | null = null;

    const connect = () => {
      if (!isActive) return;
      socket = new WebSocket(getWsUrl());
      socket.onopen = () => {
        setConnectionActive(true);
        setMessage(undefined);
        retryMs = 1000;
      };
      socket.onmessage = (event) => {
        const data = JSON.parse(event.data) as DashboardMessage;
        handleMessage(data);
      };
      socket.onclose = () => {
        setConnectionActive(false);
        if (!isActive) {
          return;
        }
        setTimeout(() => {
          retryMs = Math.min(retryMs * 2, 8000);
          connect();
        }, retryMs);
      };
      socket.onerror = () => {
        setConnectionActive(false);
      };
    };

    connect();

    return () => {
      isActive = false;
      if (socket) {
        socket.close();
      }
    };
  }, []);

  const selectedAgent = selectedAgentId ? agents[selectedAgentId] : undefined;
  const selectedAgentOnline = selectedAgent
    ? selectedAgent.connected !== false
    : false;

  const batteries = useMemo(() => {
    if (!selectedAgent) {
      return [];
    }
    return Object.values(selectedAgent.telemetry).sort((a, b) =>
      a.name.localeCompare(b.name),
    );
  }, [selectedAgent]);

  function updateAgent(agent: AgentSummary) {
    setAgents((prev) => ({
      ...prev,
      [agent.agent.id]: {
        ...agent,
        connected: computeConnectionStatus(agent),
      },
    }));
  }

  function updateTelemetry(agentId: string, snapshot: TelemetrySnapshot) {
    setAgents((prev) => {
      const current = prev[agentId];
      if (!current) {
        return prev;
      }
      return {
        ...prev,
        [agentId]: {
          ...current,
          telemetry: {
            ...current.telemetry,
            [snapshot.name]: snapshot,
          },
        },
      };
    });
  }

  function removeAgent(agentId: string) {
    let wasConnected = false;
    setAgents((prev) => {
      const next = { ...prev };
      const current = next[agentId];
      if (!current) {
        return prev;
      }
      wasConnected = current.connected !== false;
      next[agentId] = { ...current, connected: false };
      return next;
    });
    if (wasConnected) {
      setMessage(`Agent ${agentId} disconnected`);
    }
  }

  function handleMessage(message: DashboardMessage) {
    switch (message.type) {
      case "agents.snapshot": {
        setAgents((prev) => {
          const next: AgentsMap = { ...prev };
          const seen = new Set<string>();
          message.agents.forEach((agent) => {
            seen.add(agent.agent.id);
            const existing = prev[agent.agent.id];
            next[agent.agent.id] = {
              ...(existing ?? agent),
              ...agent,
              connected: computeConnectionStatus(agent),
            };
          });
          Object.keys(next).forEach((id) => {
            if (!seen.has(id)) {
              next[id] = { ...next[id], connected: false };
            }
          });
          return next;
        });
        if (!selectedAgentId && message.agents.length > 0) {
          setSelectedAgentId(message.agents[0].agent.id);
        }
        break;
      }
      case "agent.summary":
        updateAgent(message.agent);
        break;
      case "agent.telemetry":
        updateTelemetry(message.agent_id, message.snapshot);
        break;
      case "agent.removed":
        removeAgent(message.agent_id);
        break;
      default:
        break;
    }
  }

  useEffect(() => {
    const interval = window.setInterval(() => {
      setAgents((prev) => {
        let changed = false;
        const next: AgentsMap = {};
        for (const [id, agent] of Object.entries(prev)) {
          const connected = agent.connected !== false && computeConnectionStatus(agent);
          if (connected !== agent.connected) {
            changed = true;
          }
          next[id] = { ...agent, connected };
        }
        return changed ? next : prev;
      });
    }, 30 * 1000);

    return () => {
      window.clearInterval(interval);
    };
  }, []);

  async function handleCommand(
    command: string,
    target: string,
    payload?: unknown,
  ) {
    if (!selectedAgent) return;
    try {
      await sendCommand(selectedAgent.agent.id, command, target, payload);
      setMessage(`Command ${command} sent to ${target}`);
    } catch (err) {
      if (err instanceof Error) {
        setMessage(err.message);
      } else {
        setMessage("Failed to send command");
      }
    }
  }

  return (
    <div className="app">
      <aside className="sidebar">
        <h1>
          GOK-Pi Dashboard
          <span
            className={`connection-dot ${connectionActive ? "online" : ""}`}
            role="status"
            aria-label={connectionActive ? "Live updates active" : "Reconnecting"}
            title={connectionActive ? "Live updates active" : "Reconnecting"}
          />
        </h1>
        <div className="agent-list">
          {Object.values(agents).map((agent) => (
            <button
              key={agent.agent.id}
              className={`agent-card ${
                agent.agent.id === selectedAgentId ? "selected" : ""
              }`}
              onClick={() => setSelectedAgentId(agent.agent.id)}
            >
              <div className="agent-card-header">
                <strong>{agent.agent.id}</strong>
                <span
                  className={`badge ${
                    agent.connected === false ? "offline" : "online"
                  }`}
                >
                  {agent.connected === false ? "Offline" : "Online"}
                </span>
              </div>
              <div className="agent-card-meta">
                <span>{agent.agent.env}</span>
                {agent.connected === false ? (
                  <span className="agent-last-seen">
                    Last seen{" "}
                    {new Date(agent.last_seen).toLocaleTimeString([], {
                      hour: "2-digit",
                      minute: "2-digit",
                    })}
                  </span>
                ) : null}
              </div>
              <small>{agent.agent.hostname}</small>
            </button>
          ))}
        </div>
      </aside>

      <main className="content">
        {message && <div className="badge">{message}</div>}
        {selectedAgent ? (
          <>
            <header>
              <div className="agent-header">
                <h2>{selectedAgent.agent.hostname}</h2>
                <span
                  className={`badge ${
                    selectedAgentOnline ? "online" : "offline"
                  }`}
                >
                  {selectedAgentOnline ? "Connected" : "Offline"}
                </span>
              </div>
              <div className="status-bar">
                <span>
                  Last contact:{" "}
                  {new Date(selectedAgent.last_seen).toLocaleTimeString()}
                </span>
                <span className="badge">
                  Version {selectedAgent.agent.version}
                </span>
              </div>
            </header>
            {!selectedAgentOnline && (
              <div className="offline-warning">
                Agent is offline. Commands are disabled until it reconnects.
              </div>
            )}
            <section className="metrics-grid">
              {batteries.map((battery) => (
                <BatteryCard
                  key={battery.name}
                  snapshot={battery}
                  commandState={commandState}
                  onCommandStateChange={setCommandState}
                  onCommand={handleCommand}
                  isOnline={selectedAgentOnline}
                />
              ))}
            </section>
          </>
        ) : (
          <div className="empty-state">
            <h2>No agents connected</h2>
            <p>
              Once a gok-pi agent connects to the control server you will see it
              listed here. Ensure the server URL and shared secret are set in
              the agent configuration.
            </p>
          </div>
        )}
      </main>
    </div>
  );
}

interface BatteryCardProps {
  snapshot: TelemetrySnapshot;
  commandState: CommandState;
  onCommandStateChange: (state: CommandState) => void;
  onCommand: (command: string, target: string, payload?: unknown) => void;
  isOnline: boolean;
}

function BatteryCard({
  snapshot,
  commandState,
  onCommandStateChange,
  onCommand,
  isOnline,
}: BatteryCardProps) {
  const { name } = snapshot;
  const controlsDisabled = !isOnline;

  return (
    <div className="card">
      <h2>
        {name}
        <span
          className={`badge ${
            snapshot.battery_discharging ? "online" : "offline"
          }`}
        >
          {snapshot.battery_discharging ? "Discharging" : "Idle"}
        </span>
      </h2>
      <div className="metrics">
        <Metric label="RSoC" value={`${snapshot.rsoc.toFixed(1)} %`} />
        <Metric label="USoC" value={`${snapshot.usoc.toFixed(1)} %`} />
        <Metric
          label="Capacity"
          value={`${snapshot.remaining_capacity_wh.toFixed(0)} Wh`}
        />
        <Metric label="Consumption" value={`${snapshot.consumption_w} W`} />
        <Metric label="Pac" value={`${snapshot.pac_total_w} W`} />
        <Metric label="Op Mode" value={snapshot.operating_mode || "n/a"} />
      </div>
      <div className="controls">
        <div className="control-row">
          <input
            type="number"
            value={commandState.power}
            disabled={controlsDisabled}
            onChange={(event) =>
              onCommandStateChange({
                ...commandState,
                power: Number(event.target.value),
              })
            }
            placeholder="Power (W)"
          />
          <button
            className="primary"
            disabled={controlsDisabled}
            onClick={() =>
              onCommand("start_discharge", snapshot.name, {
                power: commandState.power,
              })
            }
          >
            Start
          </button>
          <button
            disabled={controlsDisabled}
            onClick={() => onCommand("stop_discharge", snapshot.name)}
          >
            Stop
          </button>
        </div>
        <div className="control-row">
          <input
            type="number"
            value={commandState.powerLimit}
            disabled={controlsDisabled}
            onChange={(event) =>
              onCommandStateChange({
                ...commandState,
                powerLimit: Number(event.target.value),
              })
            }
            placeholder="Power limit"
          />
          <input
            type="number"
            value={commandState.socLimit}
            disabled={controlsDisabled}
            onChange={(event) =>
              onCommandStateChange({
                ...commandState,
                socLimit: Number(event.target.value),
              })
            }
            placeholder="SoC limit"
          />
          <button
            disabled={controlsDisabled}
            onClick={() =>
              onCommand("set_limits", snapshot.name, {
                power_limit: commandState.powerLimit,
                soc_limit: commandState.socLimit,
              })
            }
          >
            Update Limits
          </button>
        </div>
        <div className="control-row">
          <button
            disabled={controlsDisabled}
            onClick={() =>
              onCommand("force_mode", snapshot.name, { mode: "manual" })
            }
          >
            Force Manual
          </button>
          <button
            disabled={controlsDisabled}
            onClick={() =>
              onCommand("force_mode", snapshot.name, { mode: "auto" })
            }
          >
            Force Auto
          </button>
        </div>
      </div>
    </div>
  );
}

interface MetricProps {
  label: string;
  value: string;
}

function Metric({ label, value }: MetricProps) {
  return (
    <div className="metric">
      <span>{label}</span>
      <span>{value}</span>
    </div>
  );
}


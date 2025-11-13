import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { MouseEvent } from "react";
import {
  fetchAgentConfig,
  fetchAgents,
  sendCommand,
  updateAgentConfig,
  getAuthToken,
  setAuthToken,
  clearAuthToken,
} from "./api";
import Login from "./Login";
import type {
  AgentConfig,
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

function formatConfigDraft(config: AgentConfig | null): string {
  const payload = {
    revision: config?.revision ?? 0,
    batteries: config?.batteries ?? [],
    schedules: config?.schedules ?? [],
  };
  return JSON.stringify(payload, null, 2);
}

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
  const token = getAuthToken();
  const url = `${protocol}://${host}/api/ui`;
  if (token) {
    return `${url}?token=${encodeURIComponent(token)}`;
  }
  return url;
}

export default function App() {
  const [authenticated, setAuthenticated] = useState<boolean | null>(null);
  const [agents, setAgents] = useState<AgentsMap>({});
  const [selectedAgentId, setSelectedAgentId] = useState<string>();
  const [connectionActive, setConnectionActive] = useState(false);
  const [commandState, setCommandState] =
    useState<CommandState>(defaultCommandState);
  const [message, setMessage] = useState<string>();
  const [agentConfig, setAgentConfig] = useState<AgentConfig | null>(null);
  const [configDraft, setConfigDraft] = useState<string>("");
  const [configDirty, setConfigDirty] = useState(false);
  const [configLoading, setConfigLoading] = useState(false);
  const [configSaving, setConfigSaving] = useState(false);
  const [configError, setConfigError] = useState<string>();
  const [expandedBatteries, setExpandedBatteries] = useState<Set<string>>(new Set());
  const isLoggingOutRef = useRef(false);
  const handleLogoutRef = useRef<() => void>();
  const selectedAgentIdRef = useRef<string | undefined>();
  const configDirtyRef = useRef(false);

  // Keep refs in sync with state
  useEffect(() => {
    selectedAgentIdRef.current = selectedAgentId;
  }, [selectedAgentId]);

  useEffect(() => {
    configDirtyRef.current = configDirty;
  }, [configDirty]);

  // Check authentication on mount
  useEffect(() => {
    const token = getAuthToken();
    if (token) {
      // We have a token, assume authenticated
      // If token is invalid, API calls will handle it
      setAuthenticated(true);
    } else {
      // No token, show login form
      // Login endpoint will handle case where auth is not configured
      setAuthenticated(false);
    }
  }, []);

  const handleLogin = useCallback((token: string) => {
    setAuthToken(token);
    setAuthenticated(true);
    isLoggingOutRef.current = false;
  }, []);

  const handleLogout = useCallback(() => {
    if (isLoggingOutRef.current) return;
    isLoggingOutRef.current = true;
    clearAuthToken();
    setAuthenticated(false);
    setAgents({});
    setSelectedAgentId(undefined);
    setConnectionActive(false);
  }, []);

  // Store handleLogout in ref so we can use it in effects without adding to deps
  useEffect(() => {
    handleLogoutRef.current = handleLogout;
  }, [handleLogout]);

  // Helper functions for message handling
  const updateAgent = useCallback((agent: AgentSummary) => {
    setAgents((prev: AgentsMap) => ({
      ...prev,
      [agent.agent.id]: {
        ...agent,
        connected: computeConnectionStatus(agent),
      },
    }));
  }, []);

  const updateTelemetry = useCallback((agentId: string, snapshot: TelemetrySnapshot) => {
    setAgents((prev: AgentsMap) => {
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
  }, []);

  const removeAgent = useCallback((agentId: string) => {
    let wasConnected = false;
    setAgents((prev: AgentsMap) => {
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
  }, []);

  const handleMessage = useCallback((message: DashboardMessage) => {
    switch (message.type) {
      case "agents.snapshot": {
        setAgents((prev: AgentsMap) => {
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
        setSelectedAgentId((current: string | undefined) => {
          if (!current && message.agents.length > 0) {
            return message.agents[0].agent.id;
          }
          return current;
        });
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
      case "config.updated":
        if (message.agent_id !== selectedAgentIdRef.current) {
          break;
        }
        setAgentConfig(message.config);
        setConfigError(undefined);
        if (configDirtyRef.current) {
          setMessage("Remote configuration changed while editing; draft unchanged.");
        } else {
          setConfigDraft(formatConfigDraft(message.config));
          setConfigDirty(false);
        }
        break;
      default:
        break;
    }
  }, [updateAgent, updateTelemetry, removeAgent]);

  // Fetch agents when authenticated
  useEffect(() => {
    if (!authenticated || isLoggingOutRef.current) {
      return;
    }

    let cancelled = false;

    fetchAgents()
      .then((data) => {
        if (cancelled || isLoggingOutRef.current) return;
        setAgents((prev: AgentsMap) => {
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
        setSelectedAgentId((current: string | undefined) => {
          if (!current) {
            const firstAgent = Object.values(data)[0];
            if (firstAgent) {
              return firstAgent.agent.id;
            }
          }
          return current;
        });
      })
      .catch((err) => {
        if (cancelled || isLoggingOutRef.current) return;
        if (err.message.includes("Unauthorized")) {
          handleLogoutRef.current?.();
        } else {
          setMessage(err.message);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [authenticated]);

  // Fetch agent config when selected agent changes
  useEffect(() => {
    if (!selectedAgentId || !authenticated || isLoggingOutRef.current) {
      setAgentConfig(null);
      setConfigDraft("");
      setConfigDirty(false);
      setConfigError(undefined);
      return;
    }

    let cancelled = false;
    setConfigLoading(true);

    fetchAgentConfig(selectedAgentId)
      .then((cfg) => {
        if (cancelled || isLoggingOutRef.current) return;
        setAgentConfig(cfg);
        setConfigDraft(formatConfigDraft(cfg));
        setConfigDirty(false);
        setConfigError(undefined);
      })
      .catch((err) => {
        if (cancelled || isLoggingOutRef.current) return;
        if (err instanceof Error && err.message.includes("Unauthorized")) {
          handleLogoutRef.current?.();
        } else {
          setAgentConfig(null);
          setConfigDraft(formatConfigDraft(null));
          setConfigDirty(false);
          setConfigError(err instanceof Error ? err.message : "Failed to load configuration");
        }
      })
      .finally(() => {
        if (!cancelled) {
          setConfigLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [selectedAgentId, authenticated]);

  // WebSocket connection for real-time updates
  useEffect(() => {
    if (!authenticated) {
      return;
    }

    let isActive = true;
    let retryMs = 1000;
    let socket: WebSocket | null = null;

    const connect = () => {
      if (!isActive || !authenticated) return;
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
        if (!isActive || !authenticated) {
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
  }, [authenticated, handleMessage]);

  // Update connection status periodically
  useEffect(() => {
    const interval = window.setInterval(() => {
      setAgents((prev: AgentsMap) => {
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

  const selectedAgent = selectedAgentId ? agents[selectedAgentId] : undefined;
  const selectedAgentOnline = selectedAgent
    ? selectedAgent.connected !== false
    : false;

  const batteries = useMemo(() => {
    if (!selectedAgent) {
      return [];
    }
    return Object.values(selectedAgent.telemetry).sort((a: TelemetrySnapshot, b: TelemetrySnapshot) =>
      a.name.localeCompare(b.name),
    );
  }, [selectedAgent]);

  if (authenticated === null) {
    return (
      <div className="app">
        <div className="empty-state">Loading...</div>
      </div>
    );
  }

  if (!authenticated) {
    return <Login onLogin={handleLogin} />;
  }

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

  async function handleConfigSave() {
    if (!selectedAgentId) {
      return;
    }
    try {
      setConfigSaving(true);
      setConfigError(undefined);
      const parsed = JSON.parse(configDraft) as Partial<AgentConfig>;
      const revision =
        typeof parsed.revision === "number"
          ? parsed.revision
          : agentConfig?.revision ?? 0;
      const batteries = Array.isArray(parsed.batteries)
        ? parsed.batteries
        : [];
      const schedules = Array.isArray(parsed.schedules)
        ? parsed.schedules
        : [];

      const updated = await updateAgentConfig(selectedAgentId, {
        revision,
        batteries,
        schedules,
      });
      setAgentConfig(updated);
      setConfigDraft(formatConfigDraft(updated));
      setConfigDirty(false);
      setMessage("Configuration saved");
    } catch (err) {
      if (err instanceof SyntaxError) {
        setConfigError("Configuration JSON is invalid");
      } else if (err instanceof Error) {
        setConfigError(err.message);
      } else {
        setConfigError("Failed to update configuration");
      }
    } finally {
      setConfigSaving(false);
    }
  }

  function handleConfigReset() {
    setConfigDraft(formatConfigDraft(agentConfig));
    setConfigDirty(false);
    setConfigError(undefined);
  }

  return (
    <div className="app">
      <aside className="sidebar">
        <div className="sidebar-header">
          <h1>
            GOK-Pi Dashboard
            <span
              className={`connection-dot ${connectionActive ? "online" : ""}`}
              role="status"
              aria-label={connectionActive ? "Live updates active" : "Reconnecting"}
              title={connectionActive ? "Live updates active" : "Reconnecting"}
            />
          </h1>
          <button className="logout-button" onClick={handleLogout} title="Logout">
            Logout
          </button>
        </div>
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
                  expanded={expandedBatteries.has(battery.name)}
                  onToggle={() => {
                    setExpandedBatteries((prev) => {
                      const next = new Set(prev);
                      if (next.has(battery.name)) {
                        next.delete(battery.name);
                      } else {
                        next.add(battery.name);
                      }
                      return next;
                    });
                  }}
                />
              ))}
            </section>
            <ConfigEditor
              config={agentConfig}
              draft={configDraft}
              loading={configLoading}
              saving={configSaving}
              dirty={configDirty}
              error={configError}
              onDraftChange={(value) => {
                setConfigDraft(value);
                setConfigDirty(true);
              }}
              onSave={handleConfigSave}
              onReset={handleConfigReset}
            />
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
  expanded: boolean;
  onToggle: () => void;
}

function BatteryCard({
  snapshot,
  commandState,
  onCommandStateChange,
  onCommand,
  isOnline,
  expanded,
  onToggle,
}: BatteryCardProps) {
  const { name } = snapshot;
  const controlsDisabled = !isOnline;

  const getStatusBadgeClass = (status: string) => {
    switch (status) {
      case "Connected":
        return "online";
      case "Disconnected":
        return "offline";
      case "Disabled":
        return "offline";
      default:
        return "offline";
    }
  };

  const handleCardClick = (e: MouseEvent) => {
    // Only toggle on mobile, and only if clicking on the card itself, not on interactive elements
    const target = e.target as HTMLElement;
    if (target.closest('.controls') || target.closest('button') || target.closest('input')) {
      return;
    }
    onToggle();
  };

  const handleControlClick = (e: MouseEvent) => {
    e.stopPropagation();
  };

  return (
    <div 
      className={`card battery-card ${expanded ? "expanded" : ""}`}
      onClick={handleCardClick}
    >
      <h2>
        {name}
        <span
          className={`badge ${getStatusBadgeClass(snapshot.status || "Disconnected")}`}
        >
          {snapshot.status || "Disconnected"}
        </span>
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
      <div className="controls" onClick={handleControlClick}>
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

interface ConfigEditorProps {
  config: AgentConfig | null;
  draft: string;
  loading: boolean;
  saving: boolean;
  dirty: boolean;
  error?: string;
  onDraftChange: (value: string) => void;
  onSave: () => void;
  onReset: () => void;
}

function ConfigEditor({
  config,
  draft,
  loading,
  saving,
  dirty,
  error,
  onDraftChange,
  onSave,
  onReset,
}: ConfigEditorProps) {
  const [collapsed, setCollapsed] = useState(true);

  return (
    <section className="config-panel">
      <div 
        className="config-panel-header"
        onClick={() => setCollapsed(!collapsed)}
        style={{ cursor: "pointer" }}
      >
        <h3>Remote configuration</h3>
        <div style={{ display: "flex", alignItems: "center", gap: "0.5rem" }}>
          {config ? <span className="badge">Revision {config.revision}</span> : null}
          <span className="config-toggle">{collapsed ? "▶" : "▼"}</span>
        </div>
      </div>
      {!collapsed && (
        <>
          {loading ? (
            <p>Loading configuration…</p>
          ) : (
            <>
              <p className="config-meta">
                {config
                  ? `Last updated ${new Date(config.updated_at).toLocaleString()}`
                  : "No remote configuration stored yet. Edit the JSON below and save to push new settings."}
              </p>
              <textarea
                className="config-editor"
                value={draft}
                onChange={(event) => onDraftChange(event.target.value)}
                disabled={saving}
                spellCheck={false}
              />
              {error ? <div className="config-error">{error}</div> : null}
              <div className="config-actions">
                <button onClick={onReset} disabled={!dirty || saving}>
                  Reset
                </button>
                <button
                  className="primary"
                  onClick={onSave}
                  disabled={saving || !dirty}
                >
                  {saving ? "Saving..." : "Save"}
                </button>
              </div>
            </>
          )}
        </>
      )}
    </section>
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


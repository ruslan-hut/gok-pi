import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { MouseEvent } from "react";
import {
  fetchAgentConfig,
  fetchAgentLogs,
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
  BatteryConfig,
  DashboardMessage,
  ScheduleConfig,
  TelemetrySnapshot,
} from "./types";

type AgentSummaryWithDeviceName = AgentSummary & {
  device_name?: string;
};

type AgentsMap = Record<string, AgentSummaryWithDeviceName>;

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
    device_name: config?.device_name ?? "",
    env: config?.env ?? "",
    timezone: config?.timezone ?? "",
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
  const [logsOpen, setLogsOpen] = useState(false);
  const [logs, setLogs] = useState<string>("");
  const [logsLoading, setLogsLoading] = useState(false);
  const [logsError, setLogsError] = useState<string>();
  const [logStream, setLogStream] = useState<string>("agent");
  const [logLines, setLogLines] = useState<number>(500);
  const [lastStatusMessage, setLastStatusMessage] = useState<string>("");
  const [showStatusMessage, setShowStatusMessage] = useState(false);
  const [statusMessageFrozen, setStatusMessageFrozen] = useState(false);
  const isLoggingOutRef = useRef(false);
  const handleLogoutRef = useRef<() => void>();
  const prefetchedConfigAgentsRef = useRef<Set<string>>(new Set());
  const selectedAgentIdRef = useRef<string | undefined>();
  const configDirtyRef = useRef(false);
  const statusMessageFrozenRef = useRef(false);

  // Keep refs in sync with state
  useEffect(() => {
    selectedAgentIdRef.current = selectedAgentId;
    // Clear status message when agent changes (unless frozen)
    if (!statusMessageFrozenRef.current) {
      setLastStatusMessage("");
    }
  }, [selectedAgentId]);

  useEffect(() => {
    configDirtyRef.current = configDirty;
  }, [configDirty]);

  useEffect(() => {
    statusMessageFrozenRef.current = statusMessageFrozen;
  }, [statusMessageFrozen]);

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
  const prefetchAgentConfig = useCallback((agentId: string) => {
    // Only prefetch once per agent and only when authenticated
    if (!authenticated) return;
    if (prefetchedConfigAgentsRef.current.has(agentId)) return;
    prefetchedConfigAgentsRef.current.add(agentId);

    fetchAgentConfig(agentId)
      .then((cfg) => {
        if (!cfg || isLoggingOutRef.current) return;
        // Only use config to improve display name, avoid touching config editor state here
        setAgents((prev: AgentsMap) => {
          const agent = prev[agentId];
          if (!agent) return prev;
          return {
            ...prev,
            [agentId]: {
              ...agent,
              device_name: cfg.device_name,
            },
          };
        });
      })
      .catch(() => {
        // Best-effort; ignore errors from background prefetch
      });
  }, [authenticated]);

  const updateAgent = useCallback((agent: AgentSummary) => {
    setAgents((prev: AgentsMap) => {
      const existing = prev[agent.agent.id];
      return {
        ...prev,
        [agent.agent.id]: {
          ...agent,
          connected: computeConnectionStatus(agent),
          device_name: existing?.device_name, // Preserve device_name
        },
      };
    });
    // Background fetch to populate device_name for new/unknown agents
    prefetchAgentConfig(agent.agent.id);
  }, [prefetchAgentConfig]);

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
              device_name: existing?.device_name, // Preserve device_name
            };
          });
          Object.keys(next).forEach((id) => {
            if (!seen.has(id)) {
              next[id] = { ...next[id], connected: false };
            }
          });
          return next;
        });
        // Prefetch configs so device names appear in selectors as soon as agents come online
        message.agents.forEach((agent) => {
          prefetchAgentConfig(agent.agent.id);
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
        // Update device_name in agents map
        setAgents((prev: AgentsMap) => {
          const agent = prev[message.agent_id];
          if (agent) {
            return {
              ...prev,
              [message.agent_id]: {
                ...agent,
                device_name: message.config.device_name,
              },
            };
          }
          return prev;
        });
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
        // Update device_name in agents map
        if (cfg) {
          setAgents((prev: AgentsMap) => {
            const agent = prev[selectedAgentId];
            if (agent) {
              return {
                ...prev,
                [selectedAgentId]: {
                  ...agent,
                  device_name: cfg.device_name,
                },
              };
            }
            return prev;
          });
        }
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
        // Store raw JSON for debug preview (only for selected agent)
        const data = JSON.parse(event.data) as DashboardMessage;
        const currentAgentId = selectedAgentIdRef.current;
        
        // Check if message is for the selected agent
        let isForSelectedAgent = false;
        switch (data.type) {
          case "agent.telemetry":
          case "agent.removed":
          case "config.updated":
            isForSelectedAgent = data.agent_id === currentAgentId;
            break;
          case "agent.summary":
            isForSelectedAgent = data.agent.agent.id === currentAgentId;
            break;
          case "agents.snapshot":
            // Skip snapshot messages as they contain all agents
            isForSelectedAgent = false;
            break;
          default:
            isForSelectedAgent = false;
        }
        
        if (isForSelectedAgent && !statusMessageFrozenRef.current) {
          setLastStatusMessage(event.data);
        }
        
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
    // Get configured battery names from agent config
    const configuredBatteryNames = new Set(
      (agentConfig?.batteries || []).map((b) => b.name)
    );
    // Filter telemetry to only show configured batteries
    return Object.values(selectedAgent.telemetry)
      .filter((snapshot: TelemetrySnapshot) => configuredBatteryNames.has(snapshot.name))
      .sort((a: TelemetrySnapshot, b: TelemetrySnapshot) =>
        a.name.localeCompare(b.name),
      );
  }, [selectedAgent, agentConfig]);

  // Create a map of battery configs by name for quick lookup
  const batteryConfigMap = useMemo(() => {
    const map = new Map<string, BatteryConfig>();
    (agentConfig?.batteries || []).forEach((battery) => {
      map.set(battery.name, battery);
    });
    return map;
  }, [agentConfig]);

  const handleLogRefresh = useCallback(async () => {
    if (!selectedAgentId) return;
    setLogsLoading(true);
    setLogsError(undefined);
    try {
      const logContent = await fetchAgentLogs(selectedAgentId, {
        stream: logStream,
        lines: logLines,
      });
      setLogs(logContent);
    } catch (err) {
      setLogsError(err instanceof Error ? err.message : "Failed to fetch logs");
    } finally {
      setLogsLoading(false);
    }
  }, [selectedAgentId, logStream, logLines]);

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
      const device_name = typeof parsed.device_name === "string"
        ? parsed.device_name
        : agentConfig?.device_name ?? "";
      const env = typeof parsed.env === "string"
        ? parsed.env
        : agentConfig?.env ?? "";
      const timezone = typeof parsed.timezone === "string"
        ? parsed.timezone
        : agentConfig?.timezone ?? "";
      const batteries = Array.isArray(parsed.batteries)
        ? parsed.batteries
        : [];
      const schedules = Array.isArray(parsed.schedules)
        ? parsed.schedules
        : [];

      const updated = await updateAgentConfig(selectedAgentId, {
        device_name,
        env,
        timezone,
        revision,
        batteries,
        schedules,
      });
      setAgentConfig(updated);
      setConfigDraft(formatConfigDraft(updated));
      setConfigDirty(false);
      // Update device_name in agents map
      setAgents((prev: AgentsMap) => {
        const agent = prev[selectedAgentId];
        if (agent) {
          return {
            ...prev,
            [selectedAgentId]: {
              ...agent,
              device_name: updated.device_name,
            },
          };
        }
        return prev;
      });
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
        <div className="agent-select-mobile">
          <select
            value={selectedAgentId || ""}
            onChange={(e) => setSelectedAgentId(e.target.value || undefined)}
            className="agent-select"
          >
            <option value="">Select agent...</option>
            {Object.values(agents).map((agent) => (
              <option key={agent.agent.id} value={agent.agent.id}>
                {agent.device_name || agent.agent.hostname || agent.agent.id} ({agent.connected === false ? "Offline" : "Online"})
              </option>
            ))}
          </select>
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
                <strong>{agent.device_name || agent.agent.hostname || agent.agent.id}</strong>
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
              <small>ID: {agent.agent.id}</small>
            </button>
          ))}
        </div>
      </aside>

      <main className="content">
        {message && (
          <div className="message-banner">
            {message}
            <button
              className="message-close"
              onClick={() => setMessage(undefined)}
              aria-label="Dismiss message"
            >
              ×
            </button>
          </div>
        )}
        {selectedAgent ? (
          <>
            <header>
              <div className="agent-header">
                <h2>{agentConfig?.device_name || selectedAgent.agent.hostname}</h2>
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
                  Device ID: {selectedAgent.agent.id}
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
              {batteries.map((battery) => {
                // Check if any schedule for this battery has goal reached today
                const hasGoalReachedToday = (() => {
                  if (!selectedAgent?.schedule_goal_reached || !agentConfig?.schedules) {
                    return false;
                  }
                  const today = new Date();
                  today.setHours(0, 0, 0, 0);
                  const batterySchedules = agentConfig.schedules.filter(
                    (s) => s.battery_name === battery.name && s.run_once
                  );
                  return batterySchedules.some((schedule) => {
                    const goalTime = selectedAgent.schedule_goal_reached?.[schedule.name ?? ""];
                    if (!goalTime) return false;
                    const goalDate = new Date(goalTime);
                    goalDate.setHours(0, 0, 0, 0);
                    return goalDate.getTime() === today.getTime();
                  });
                })();

                return (
                  <BatteryCard
                    key={battery.name}
                    snapshot={battery}
                    batteryConfig={batteryConfigMap.get(battery.name)}
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
                    hasGoalReachedToday={hasGoalReachedToday}
                  />
                );
              })}
            </section>
            <ConfigEditor
              config={agentConfig}
              agentEnv={selectedAgent?.agent.env}
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
              scheduleGoalReached={selectedAgent?.schedule_goal_reached}
              onResetGoal={(scheduleName) => {
                // Find the schedule to get its battery_name for the command target
                const schedule = agentConfig?.schedules.find(s => s.name === scheduleName);
                const batteryName = schedule?.battery_name || (selectedAgent?.telemetry ? Object.keys(selectedAgent.telemetry)[0] : "");
                handleCommand("reset_goal", batteryName, { schedule_name: scheduleName });
              }}
              isOnline={selectedAgentOnline}
            />
            <StatusMessagePreview
              lastStatusMessage={lastStatusMessage}
              showStatusMessage={showStatusMessage}
              onToggleStatusMessage={() => setShowStatusMessage(!showStatusMessage)}
              statusMessageFrozen={statusMessageFrozen}
              onToggleStatusMessageFrozen={() => setStatusMessageFrozen(!statusMessageFrozen)}
            />
            <LogViewer
              agentId={selectedAgentId}
              isOpen={logsOpen}
              logs={logs}
              loading={logsLoading}
              error={logsError}
              stream={logStream}
              lines={logLines}
              onToggle={() => setLogsOpen(!logsOpen)}
              onRefresh={handleLogRefresh}
              onStreamChange={setLogStream}
              onLinesChange={setLogLines}
              disabled={!selectedAgentOnline}
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
  batteryConfig?: BatteryConfig;
  commandState: CommandState;
  onCommandStateChange: (state: CommandState) => void;
  onCommand: (command: string, target: string, payload?: unknown) => void;
  isOnline: boolean;
  expanded: boolean;
  onToggle: () => void;
  hasGoalReachedToday?: boolean;
}

function BatteryCard({
  snapshot,
  batteryConfig,
  commandState,
  onCommandStateChange,
  onCommand,
  isOnline,
  expanded,
  onToggle,
  hasGoalReachedToday,
}: BatteryCardProps) {
  const { name } = snapshot;
  const controlsDisabled = !isOnline;
  // Check if limits are defined in config (non-zero values)
  const hasConfigLimits = batteryConfig && (
    (batteryConfig.power_limit !== undefined && batteryConfig.power_limit > 0) ||
    (batteryConfig.soc_limit !== undefined && batteryConfig.soc_limit > 0)
  );

  const getStatusBadgeClass = (status: string) => {
    switch (status) {
      case "Connected":
        return "online";
      case "Disconnected":
        return "offline";
      case "Disabled":
        return "disabled";
      default:
        return "offline";
    }
  };

  const handleHeaderClick = (e: MouseEvent) => {
    // Toggle when clicking on the header, but not on badges or other interactive elements
    const target = e.target as HTMLElement;
    if (target.closest('.badge') || target.closest('button')) {
      return;
    }
    onToggle();
  };

  const handleControlClick = (e: MouseEvent) => {
    e.stopPropagation();
  };

  const renderOperatingMode = (mode: string | undefined | null): string => {
    if (mode === undefined || mode === null || mode === "") {
      return "n/a";
    }

    switch (mode) {
      case "1":
        return "MANUAL";
      case "2":
        return "AUTO";
      case "10":
        return "SERVICE";
      default:
        return mode;
    }
  };

  const isManualMode = snapshot.operating_mode === "1";
  const isServiceMode = snapshot.operating_mode === "10";

  return (
    <div 
      className={`card battery-card ${expanded ? "expanded" : ""} ${isManualMode ? "manual-mode" : ""} ${isServiceMode ? "service-mode" : ""}`}
    >
      <h2 
        className="battery-card-header"
        onClick={handleHeaderClick}
      >
        <span className="battery-card-title">{name}</span>
        <span className="battery-card-badges">
          <span
            className={`badge ${getStatusBadgeClass(snapshot.status || "Disconnected")}`}
          >
            {snapshot.status || "Disconnected"}
          </span>
          {hasGoalReachedToday && (
            <span
              className="goal-reached-icon"
              title="Schedule goal reached today"
              style={{
                color: "#22c55e",
                fontSize: "1rem",
                marginLeft: "0.25rem",
              }}
            >
              ✓
            </span>
          )}
          <span
            className={`badge ${
              snapshot.battery_discharging ? "online" : "offline"
            }`}
          >
            {snapshot.battery_discharging ? "Discharging" : snapshot.battery_charging ? "Charging" : "Idle"}
          </span>
          <span className="battery-card-toggle" title={expanded ? "Collapse controls" : "Expand controls"}>
            {expanded ? "▼" : "▶"}
          </span>
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
        <Metric label="Op Mode" value={renderOperatingMode(snapshot.operating_mode)} />
      </div>
      <div className="controls" onClick={handleControlClick}>
        <div className="control-group">
          <label className="control-label">Power (W)</label>
          <input
            type="number"
            className="control-input"
            value={commandState.power}
            disabled={controlsDisabled}
            onChange={(event) =>
              onCommandStateChange({
                ...commandState,
                power: Number(event.target.value),
              })
            }
            placeholder="Power"
          />
        </div>
        
        <div className="control-group">
          <label className="control-label">Discharge</label>
          <div className="control-actions">
            <button
              className="control-button control-button-primary"
              disabled={controlsDisabled}
              onClick={() =>
                onCommand("start_discharge", snapshot.name, {
                  power: commandState.power,
                })
              }
              title="Start Discharge"
            >
              <span className="control-button-icon">▶</span>
              Start
            </button>
            <button
              className="control-button"
              disabled={controlsDisabled}
              onClick={() => onCommand("stop_discharge", snapshot.name)}
              title="Stop Discharge"
            >
              <span className="control-button-icon">■</span>
              Stop
            </button>
          </div>
        </div>

        <div className="control-group">
          <label className="control-label">Charge</label>
          <div className="control-actions">
            <button
              className="control-button control-button-primary"
              disabled={controlsDisabled}
              onClick={() =>
                onCommand("start_charge", snapshot.name, {
                  power: commandState.power,
                })
              }
              title="Start Charge"
            >
              <span className="control-button-icon">▶</span>
              Start
            </button>
            <button
              className="control-button"
              disabled={controlsDisabled}
              onClick={() => onCommand("stop_charge", snapshot.name)}
              title="Stop Charge"
            >
              <span className="control-button-icon">■</span>
              Stop
            </button>
          </div>
        </div>

        {!hasConfigLimits && (
          <div className="control-group">
            <label className="control-label">Limits</label>
            <div className="control-inputs-row">
              <div className="control-input-wrapper">
                <label className="control-input-label" htmlFor={`power-limit-${snapshot.name}`}>
                  Power limit (W)
                </label>
                <input
                  id={`power-limit-${snapshot.name}`}
                  type="number"
                  className="control-input"
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
              </div>
              <div className="control-input-wrapper">
                <label className="control-input-label" htmlFor={`soc-limit-${snapshot.name}`}>
                  SoC limit (%)
                </label>
                <input
                  id={`soc-limit-${snapshot.name}`}
                  type="number"
                  className="control-input"
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
              </div>
            </div>
            <button
              className="control-button control-button-secondary"
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
        )}

        <div className="control-group">
          <label className="control-label">Mode</label>
          <div className="control-actions">
            <button
              className="control-button"
              disabled={controlsDisabled}
              onClick={() =>
                onCommand("force_mode", snapshot.name, { mode: "manual" })
              }
            >
              Force Manual
            </button>
            <button
              className="control-button"
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
    </div>
  );
}

interface ConfigEditorProps {
  config: AgentConfig | null;
  agentEnv?: string;
  draft: string;
  loading: boolean;
  saving: boolean;
  dirty: boolean;
  error?: string;
  onDraftChange: (value: string) => void;
  onSave: () => void;
  onReset: () => void;
  scheduleGoalReached?: Record<string, string>;
  onResetGoal?: (scheduleName: string) => void;
  isOnline?: boolean;
}

function ConfigEditor({
  config,
  agentEnv,
  draft,
  loading,
  saving,
  dirty,
  error,
  onDraftChange,
  onSave,
  onReset,
  scheduleGoalReached,
  onResetGoal,
  isOnline,
}: ConfigEditorProps) {
  const [collapsed, setCollapsed] = useState(true);
  const [showJson, setShowJson] = useState(false);
  const [localConfig, setLocalConfig] = useState<AgentConfig | null>(null);

  // Parse draft into local config state
  useEffect(() => {
    if (draft) {
      try {
        const parsed = JSON.parse(draft) as Partial<AgentConfig>;
        setLocalConfig({
          device_name: parsed.device_name ?? config?.device_name ?? "",
          env: parsed.env ?? config?.env ?? agentEnv ?? "",
          timezone: parsed.timezone ?? config?.timezone ?? "",
          revision: parsed.revision ?? config?.revision ?? 0,
          updated_at: config?.updated_at ?? new Date().toISOString(),
          batteries: Array.isArray(parsed.batteries) ? parsed.batteries : [],
          schedules: Array.isArray(parsed.schedules) ? parsed.schedules : [],
        });
      } catch {
        // Invalid JSON, keep current state
      }
    } else {
      setLocalConfig(config);
    }
  }, [draft, config]);

  const handleConfigChange = (newConfig: AgentConfig) => {
    setLocalConfig(newConfig);
    onDraftChange(formatConfigDraft(newConfig));
  };

  const handleBatteryChange = (index: number, battery: BatteryConfig) => {
    if (!localConfig) return;
    const newBatteries = [...localConfig.batteries];
    newBatteries[index] = battery;
    handleConfigChange({ ...localConfig, batteries: newBatteries });
  };

  const handleBatteryAdd = () => {
    if (!localConfig) return;
    const newBattery: BatteryConfig = {
      name: "",
      url: "",
      token: "",
      enabled: true,
      capacity_limit: 0,
    };
    handleConfigChange({
      ...localConfig,
      batteries: [...localConfig.batteries, newBattery],
    });
  };

  const handleBatteryRemove = (index: number) => {
    if (!localConfig) return;
    const newBatteries = localConfig.batteries.filter((_, i) => i !== index);
    handleConfigChange({ ...localConfig, batteries: newBatteries });
  };

  const handleScheduleChange = (index: number, schedule: ScheduleConfig) => {
    if (!localConfig) return;
    const newSchedules = [...localConfig.schedules];
    newSchedules[index] = schedule;
    handleConfigChange({ ...localConfig, schedules: newSchedules });
  };

  const handleScheduleAdd = () => {
    if (!localConfig) return;
    const newSchedule: ScheduleConfig = {
      name: "",
      type: "discharge",
      start_time: "00:00",
      stop_time: "23:59",
      battery_name: "",
      enabled: true,
      power_limit: 0,
      soc_limit: 0,
      run_once: false,
    };
    handleConfigChange({
      ...localConfig,
      schedules: [...localConfig.schedules, newSchedule],
    });
  };

  const handleScheduleRemove = (index: number) => {
    if (!localConfig) return;
    const newSchedules = localConfig.schedules.filter((_, i) => i !== index);
    handleConfigChange({ ...localConfig, schedules: newSchedules });
  };

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
          {dirty && <span className="badge" style={{ borderColor: "#fbbf24", color: "#fbbf24" }}>Unsaved</span>}
          <span className="config-toggle">{collapsed ? "▶" : "▼"}</span>
        </div>
      </div>
      {!collapsed && (
        <>
          {loading ? (
            <div className="config-loading">
              <div className="spinner"></div>
              <p>Loading configuration…</p>
            </div>
          ) : (
            <>
              <p className="config-meta">
                {config
                  ? `Last updated ${new Date(config.updated_at).toLocaleString()}`
                  : "No remote configuration stored yet. Configure batteries and schedules below."}
              </p>
              
              {!showJson ? (
                <div className="config-forms">
                  <div className="config-section">
                    <h4>Device Settings</h4>
                    <div className="config-form-grid">
                      <div className="form-field">
                        <label htmlFor="device-name">Device Name</label>
                        <input
                          id="device-name"
                          type="text"
                          value={localConfig?.device_name ?? ""}
                          onChange={(e) => {
                            if (!localConfig) return;
                            handleConfigChange({ ...localConfig, device_name: e.target.value });
                          }}
                          disabled={saving}
                          placeholder="My Battery Controller"
                        />
                      </div>
                      <div className="form-field">
                        <label htmlFor="env">Logging Level (env)</label>
                        <select
                          id="env"
                          value={localConfig?.env ?? agentEnv ?? ""}
                          onChange={(e) => {
                            if (!localConfig) return;
                            handleConfigChange({ ...localConfig, env: e.target.value });
                          }}
                          disabled={saving}
                        >
                          <option value="">Select...</option>
                          <option value="local">local</option>
                          <option value="dev">dev</option>
                          <option value="prod">prod</option>
                        </select>
                        <small className="form-help-text">
                          Controls logging level: local (debug to stdout), dev (debug to file), prod (info to file)
                        </small>
                      </div>
                      <div className="form-field">
                        <label htmlFor="timezone">Timezone</label>
                        <input
                          id="timezone"
                          type="text"
                          value={localConfig?.timezone ?? ""}
                          onChange={(e) => {
                            if (!localConfig) return;
                            handleConfigChange({ ...localConfig, timezone: e.target.value });
                          }}
                          disabled={saving}
                          placeholder="UTC"
                        />
                        <small className="form-help-text">
                          IANA timezone name (e.g., "America/New_York", "Europe/London", "UTC"). Used for schedule time parsing.
                        </small>
                      </div>
                    </div>
                  </div>

                  <div className="config-section">
                    <div className="config-section-header">
                      <h4>Batteries</h4>
                      <button 
                        type="button" 
                        className="button-small"
                        onClick={handleBatteryAdd}
                        disabled={saving || !localConfig}
                      >
                        + Add Battery
                      </button>
                    </div>
                    {localConfig?.batteries.length === 0 ? (
                      <p className="config-empty">No batteries configured. Click "Add Battery" to create one.</p>
                    ) : (
                      <div className="config-items">
                        {localConfig?.batteries.map((battery, index) => (
                          <BatteryConfigForm
                            key={`battery-${index}-${battery.name || 'new'}`}
                            battery={battery}
                            onChange={(b) => handleBatteryChange(index, b)}
                            onRemove={() => handleBatteryRemove(index)}
                            disabled={saving}
                          />
                        ))}
                      </div>
                    )}
                  </div>

                  <div className="config-section">
                    <div className="config-section-header">
                      <h4>Schedules</h4>
                      <button 
                        type="button" 
                        className="button-small"
                        onClick={handleScheduleAdd}
                        disabled={saving || !localConfig}
                      >
                        + Add Schedule
                      </button>
                    </div>
                    {localConfig?.schedules.length === 0 ? (
                      <p className="config-empty">No schedules configured. Click "Add Schedule" to create one.</p>
                    ) : (
                      <div className="config-items">
                        {localConfig?.schedules.map((schedule, index) => (
                          <ScheduleConfigForm
                            key={`schedule-${index}-${schedule.battery_name || 'new'}`}
                            schedule={schedule}
                            batteryNames={localConfig?.batteries.map(b => b.name) || []}
                            onChange={(s) => handleScheduleChange(index, s)}
                            onRemove={() => handleScheduleRemove(index)}
                            disabled={saving}
                            goalReachedAt={schedule.name ? scheduleGoalReached?.[schedule.name] : undefined}
                            onResetGoal={schedule.name && onResetGoal ? () => onResetGoal(schedule.name!) : undefined}
                            isOnline={isOnline}
                          />
                        ))}
                      </div>
                    )}
                  </div>
                </div>
              ) : (
                <textarea
                  className="config-editor"
                  value={draft}
                  onChange={(event) => onDraftChange(event.target.value)}
                  disabled={saving}
                  spellCheck={false}
                />
              )}

              <div className="config-view-toggle">
                <button
                  type="button"
                  className="button-link"
                  onClick={() => setShowJson(!showJson)}
                  disabled={saving}
                >
                  {showJson ? "← Back to Forms" : "Advanced: Edit JSON"}
                </button>
              </div>

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
                  {saving ? (
                    <>
                      <span className="spinner-small"></span>
                      Saving...
                    </>
                  ) : (
                    "Save"
                  )}
                </button>
              </div>
            </>
          )}
        </>
      )}
    </section>
  );
}

interface BatteryConfigFormProps {
  battery: BatteryConfig;
  onChange: (battery: BatteryConfig) => void;
  onRemove: () => void;
  disabled?: boolean;
}

function BatteryConfigForm({ battery, onChange, onRemove, disabled }: BatteryConfigFormProps) {
  const [collapsed, setCollapsed] = useState(true);

  return (
    <div className="config-item config-item-battery">
      <div
        className={`config-item-header ${collapsed ? "config-item-header-collapsed" : ""}`}
        onClick={() => setCollapsed(!collapsed)}
        style={{ cursor: "pointer" }}
      >
        <h5>
          {battery.name || "Unnamed Battery"}
          <span className="config-item-toggle">{collapsed ? "▶" : "▼"}</span>
        </h5>
        <div className="config-item-header-actions" onClick={(e) => e.stopPropagation()}>
          <label className="switch">
            <input
              type="checkbox"
              checked={battery.enabled}
              onChange={(e) => onChange({ ...battery, enabled: e.target.checked })}
              disabled={disabled}
            />
            <span className="switch-slider"></span>
            <span className="switch-label">Enabled</span>
          </label>
          <button
            type="button"
            className="button-icon"
            onClick={onRemove}
            disabled={disabled}
            title="Remove battery"
          >
            ×
          </button>
        </div>
      </div>
      {collapsed && (
        <div className="config-item-summary">
          {battery.url?.replace(/^https?:\/\//, '') || 'No URL'}
          {' · '}{battery.capacity_limit > 0 ? `${battery.capacity_limit} Wh` : 'No limit'}
        </div>
      )}
      {!collapsed && <div className="config-form-grid">
        <div className="form-field">
          <label htmlFor={`battery-name-${battery.name || 'new'}`}>Name *</label>
          <input
            id={`battery-name-${battery.name || 'new'}`}
            type="text"
            value={battery.name}
            onChange={(e) => onChange({ ...battery, name: e.target.value })}
            disabled={disabled}
            required
            placeholder="battery-1"
          />
        </div>
        <div className="form-field">
          <label htmlFor={`battery-url-${battery.name || 'new'}`}>URL *</label>
          <input
            id={`battery-url-${battery.name || 'new'}`}
            type="url"
            value={battery.url}
            onChange={(e) => onChange({ ...battery, url: e.target.value })}
            disabled={disabled}
            required
            placeholder="http://192.168.1.100"
          />
        </div>
        <div className="form-field">
          <label htmlFor={`battery-token-${battery.name || 'new'}`}>Token *</label>
          <input
            id={`battery-token-${battery.name || 'new'}`}
            type="password"
            value={battery.token}
            onChange={(e) => onChange({ ...battery, token: e.target.value })}
            disabled={disabled}
            required
            placeholder="API token"
          />
        </div>
        <div className="form-field">
          <label htmlFor={`battery-capacity-${battery.name || 'new'}`}>Capacity Limit (Wh)</label>
          <input
            id={`battery-capacity-${battery.name || 'new'}`}
            type="number"
            value={battery.capacity_limit}
            onChange={(e) => onChange({ ...battery, capacity_limit: Number(e.target.value) })}
            disabled={disabled}
            min="0"
            step="1"
          />
        </div>
        <div className="form-field-pair">
          <div className="form-field">
            <label htmlFor={`battery-power-${battery.name || 'new'}`}>Power Limit (W)</label>
            <input
              id={`battery-power-${battery.name || 'new'}`}
              type="number"
              value={battery.power_limit ?? 0}
              onChange={(e) => onChange({ ...battery, power_limit: Number(e.target.value) || undefined })}
              disabled={disabled}
              min="0"
              step="1"
              placeholder="Default power limit"
            />
          </div>
          <div className="form-field">
            <label htmlFor={`battery-soc-${battery.name || 'new'}`}>SoC Limit (%)</label>
            <input
              id={`battery-soc-${battery.name || 'new'}`}
              type="number"
              value={battery.soc_limit ?? 0}
              onChange={(e) => onChange({ ...battery, soc_limit: Number(e.target.value) || undefined })}
              disabled={disabled}
              min="0"
              max="100"
              step="0.1"
              placeholder="Default SoC limit"
            />
          </div>
        </div>
        <small className="form-help-text">
          Used when no schedule is active
        </small>
      </div>}
    </div>
  );
}

interface ScheduleConfigFormProps {
  schedule: ScheduleConfig;
  batteryNames: string[];
  onChange: (schedule: ScheduleConfig) => void;
  onRemove: () => void;
  disabled?: boolean;
  goalReachedAt?: string;
  onResetGoal?: () => void;
  isOnline?: boolean;
}

function ScheduleConfigForm({ schedule, batteryNames, onChange, onRemove, disabled, goalReachedAt, onResetGoal, isOnline }: ScheduleConfigFormProps) {
  const [collapsed, setCollapsed] = useState(true);

  const formatGoalReachedTime = (isoTime: string): string => {
    try {
      const date = new Date(isoTime);
      return date.toLocaleString([], {
        month: 'short',
        day: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
      });
    } catch {
      return isoTime;
    }
  };

  return (
    <div className="config-item config-item-schedule">
      <div
        className={`config-item-header ${collapsed ? "config-item-header-collapsed" : ""}`}
        onClick={() => setCollapsed(!collapsed)}
        style={{ cursor: "pointer" }}
      >
        <h5>
          {schedule.name || schedule.battery_name || "Unnamed Schedule"}
          {schedule.type && (
            <span className="badge" style={{ marginLeft: "8px", fontSize: "0.8em" }}>
              {schedule.type === "charge" ? "Charge" : "Discharge"}
            </span>
          )}
          <span className="config-item-toggle">{collapsed ? "▶" : "▼"}</span>
        </h5>
        <div className="config-item-header-actions" onClick={(e) => e.stopPropagation()}>
          <label className="switch">
            <input
              type="checkbox"
              checked={schedule.enabled}
              onChange={(e) => onChange({ ...schedule, enabled: e.target.checked })}
              disabled={disabled}
            />
            <span className="switch-slider"></span>
            <span className="switch-label">Enabled</span>
          </label>
          <label className="switch">
            <input
              type="checkbox"
              checked={schedule.run_once ?? false}
              onChange={(e) => onChange({ ...schedule, run_once: e.target.checked })}
              disabled={disabled}
            />
            <span className="switch-slider"></span>
            <span className="switch-label">Run Once</span>
          </label>
          <button
            type="button"
            className="button-icon"
            onClick={onRemove}
            disabled={disabled}
            title="Remove schedule"
          >
            ×
          </button>
        </div>
      </div>
      {schedule.run_once && goalReachedAt && (
        <div
          className="config-item-goal-badge"
          title={`Goal reached at ${goalReachedAt}`}
        >
          <span className="goal-badge-text">✓ Goal reached {formatGoalReachedTime(goalReachedAt)}</span>
          {onResetGoal && (
            <button
              type="button"
              className="goal-reset-button"
              onClick={onResetGoal}
              disabled={!isOnline}
              title={isOnline ? "Reset goal to allow schedule to run again today" : "Agent offline - cannot reset goal"}
            >
              ↺ Reset
            </button>
          )}
        </div>
      )}
      {collapsed && (
        <div className="config-item-summary">
          {schedule.start_time}–{schedule.stop_time} · {schedule.battery_name || 'No battery'} · {schedule.power_limit}W / {schedule.soc_limit}%
        </div>
      )}
      {!collapsed && <div className="config-form-grid">
        <div className="form-field">
          <label htmlFor={`schedule-name-${schedule.battery_name || 'new'}`}>Schedule Name</label>
          <input
            id={`schedule-name-${schedule.battery_name || 'new'}`}
            type="text"
            value={schedule.name ?? ""}
            onChange={(e) => onChange({ ...schedule, name: e.target.value })}
            disabled={disabled}
            placeholder="Evening Discharge"
          />
        </div>
        <div className="form-field">
          <label htmlFor={`schedule-type-${schedule.battery_name || 'new'}`}>Type *</label>
          <select
            id={`schedule-type-${schedule.battery_name || 'new'}`}
            value={schedule.type || "discharge"}
            onChange={(e) => onChange({ ...schedule, type: e.target.value })}
            disabled={disabled}
            required
          >
            <option value="discharge">Discharge</option>
            <option value="charge">Charge</option>
          </select>
        </div>
        <div className="form-field">
          <label htmlFor={`schedule-battery-${schedule.battery_name || 'new'}`}>Battery Name *</label>
          <select
            id={`schedule-battery-${schedule.battery_name || 'new'}`}
            value={schedule.battery_name}
            onChange={(e) => onChange({ ...schedule, battery_name: e.target.value })}
            disabled={disabled}
            required
          >
            <option value="">Select battery...</option>
            {batteryNames.map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
          </select>
        </div>
        <div className="form-field-pair">
          <div className="form-field">
            <label htmlFor={`schedule-start-${schedule.battery_name || 'new'}`}>Start Time *</label>
            <input
              id={`schedule-start-${schedule.battery_name || 'new'}`}
              type="time"
              value={schedule.start_time}
              onChange={(e) => onChange({ ...schedule, start_time: e.target.value })}
              disabled={disabled}
              required
            />
          </div>
          <div className="form-field">
            <label htmlFor={`schedule-stop-${schedule.battery_name || 'new'}`}>Stop Time *</label>
            <input
              id={`schedule-stop-${schedule.battery_name || 'new'}`}
              type="time"
              value={schedule.stop_time}
              onChange={(e) => onChange({ ...schedule, stop_time: e.target.value })}
              disabled={disabled}
              required
            />
          </div>
        </div>
        <div className="form-field-pair">
          <div className="form-field">
            <label htmlFor={`schedule-power-${schedule.battery_name || 'new'}`}>Power Limit (W) *</label>
            <input
              id={`schedule-power-${schedule.battery_name || 'new'}`}
              type="number"
              value={schedule.power_limit}
              onChange={(e) => onChange({ ...schedule, power_limit: Number(e.target.value) })}
              disabled={disabled}
              required
              min="0"
              step="1"
            />
          </div>
          <div className="form-field">
            <label htmlFor={`schedule-soc-${schedule.battery_name || 'new'}`}>SoC Limit (%) *</label>
            <input
              id={`schedule-soc-${schedule.battery_name || 'new'}`}
              type="number"
              value={schedule.soc_limit}
              onChange={(e) => onChange({ ...schedule, soc_limit: Number(e.target.value) })}
              disabled={disabled}
              required
              min="0"
              max="100"
              step="0.1"
            />
          </div>
        </div>
      </div>}
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

interface StatusMessagePreviewProps {
  lastStatusMessage: string;
  showStatusMessage: boolean;
  onToggleStatusMessage: () => void;
  statusMessageFrozen: boolean;
  onToggleStatusMessageFrozen: () => void;
}

interface LogViewerProps {
  agentId?: string;
  isOpen: boolean;
  logs: string;
  loading: boolean;
  error?: string;
  stream: string;
  lines: number;
  onToggle: () => void;
  onRefresh: () => Promise<void>;
  onStreamChange: (stream: string) => void;
  onLinesChange: (lines: number) => void;
  disabled: boolean;
}

function StatusMessagePreview({
  lastStatusMessage,
  showStatusMessage,
  onToggleStatusMessage,
  statusMessageFrozen,
  onToggleStatusMessageFrozen,
}: StatusMessagePreviewProps) {
  return (
    <section className="config-panel">
      <div 
        className="config-panel-header"
        style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}
      >
        <div 
          onClick={onToggleStatusMessage}
          style={{ cursor: "pointer", display: "flex", alignItems: "center", gap: "0.5rem", flex: 1 }}
        >
          <h3 style={{ margin: 0 }}>Last Status Message</h3>
          {statusMessageFrozen && (
            <span className="badge" style={{ borderColor: "#fbbf24", color: "#fbbf24", fontSize: "0.75rem" }}>
              Frozen
            </span>
          )}
        </div>
        <div style={{ display: "flex", alignItems: "center", gap: "0.5rem" }}>
          <button
            type="button"
            className="button-small"
            onClick={(e) => {
              e.stopPropagation();
              onToggleStatusMessageFrozen();
            }}
            title={statusMessageFrozen ? "Unfreeze updates" : "Freeze updates"}
            style={{ fontSize: "0.75rem", padding: "0.25rem 0.5rem" }}
          >
            {statusMessageFrozen ? "▶ Resume" : "⏸ Freeze"}
          </button>
          <span 
            className="config-toggle"
            onClick={onToggleStatusMessage}
            style={{ cursor: "pointer" }}
          >
            {showStatusMessage ? "▼" : "▶"}
          </span>
        </div>
      </div>
      {showStatusMessage && (
        <div className="status-message-content">
          {lastStatusMessage ? (
            (() => {
              try {
                const parsed = JSON.parse(lastStatusMessage);
                return JSON.stringify(parsed, null, 2);
              } catch {
                return lastStatusMessage;
              }
            })()
          ) : (
            "No status messages received yet"
          )}
        </div>
      )}
    </section>
  );
}

function LogViewer({
  agentId,
  isOpen,
  logs,
  loading,
  error,
  stream,
  lines,
  onToggle,
  onRefresh,
  onStreamChange,
  onLinesChange,
  disabled,
}: LogViewerProps) {
  const logContainerRef = useRef<HTMLDivElement>(null);
  const onRefreshRef = useRef(onRefresh);

  // Keep ref in sync
  useEffect(() => {
    onRefreshRef.current = onRefresh;
  }, [onRefresh]);

  // Auto-scroll to bottom when logs update
  useEffect(() => {
    if (isOpen && logContainerRef.current) {
      logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight;
    }
  }, [logs, isOpen]);

  // Fetch logs when opened or settings change
  useEffect(() => {
    if (isOpen && agentId && !disabled) {
      onRefreshRef.current();
    }
  }, [isOpen, agentId, stream, lines, disabled]);

  return (
    <section className="config-panel">
      <div
        className="config-panel-header"
        style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: "1rem" }}
      >
        <h3 
          onClick={onToggle}
          style={{ cursor: "pointer", margin: 0, flex: "0 0 auto" }}
        >
          Agent Logs
        </h3>
        {isOpen && (
          <div className="log-viewer-controls">
            <select
              id="log-stream"
              value={stream}
              onChange={(e) => {
                e.stopPropagation();
                onStreamChange(e.target.value);
              }}
              disabled={disabled || loading}
            >
              <option value="agent">Agent Logs</option>
              <option value="updater">Autoupdater Logs</option>
            </select>
            <div style={{ display: "flex", alignItems: "center", gap: "0.5rem" }}>
              <button
                onClick={(e) => {
                  e.stopPropagation();
                  onLinesChange(500);
                }}
                disabled={disabled || loading}
                className={lines === 500 ? "primary" : ""}
              >
                500
              </button>
              <button
                onClick={(e) => {
                  e.stopPropagation();
                  onLinesChange(1000);
                }}
                disabled={disabled || loading}
                className={lines === 1000 ? "primary" : ""}
              >
                1000
              </button>
            </div>
            <button
              onClick={(e) => {
                e.stopPropagation();
                onRefresh();
              }}
              disabled={disabled || loading}
              className="primary"
            >
              {loading ? "Loading..." : "Refresh"}
            </button>
          </div>
        )}
        <span 
          className="config-toggle"
          onClick={onToggle}
          style={{ cursor: "pointer", flex: "0 0 auto" }}
        >
          {isOpen ? "▼" : "▶"}
        </span>
      </div>
      {isOpen && (
        <>
          {disabled && (
            <div className="offline-warning" style={{ marginTop: "1rem", marginBottom: "1rem" }}>
              Agent is offline. Logs cannot be fetched.
            </div>
          )}
          {error && <div className="config-error">{error}</div>}
          {loading && logs === "" ? (
            <div className="config-loading">
              <div className="spinner"></div>
              <p>Loading logs…</p>
            </div>
          ) : (
            <div
              ref={logContainerRef}
              className="log-viewer-content"
            >
              {logs || "No logs available"}
            </div>
          )}
        </>
      )}
    </section>
  );
}


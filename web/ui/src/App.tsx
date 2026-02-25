import { useCallback, useEffect, useRef, useState } from "react";
import {
  getAuthToken,
  setAuthToken,
  clearAuthToken,
  sendCommand,
} from "./api";
import Login from "./Login";
import { useAgents } from "./hooks/useAgents";
import { useConfig } from "./hooks/useConfig";
import { useLogs } from "./hooks/useLogs";
import { usePersistedTab } from "./hooks/usePersistedTab";
import { TabBar } from "./components/layout/TabBar";
import { BottomNav } from "./components/layout/BottomNav";
import { MonitorTab } from "./components/monitor/MonitorTab";
import { ConfigTab } from "./components/config/ConfigTab";
import { ToolsTab } from "./components/tools/ToolsTab";
import PricesDashboard from "./components/prices/PricesDashboard";
import { MessageBanner } from "./components/shared/MessageBanner";
import type { TabId } from "./types";

export default function App() {
  const [authenticated, setAuthenticated] = useState<boolean | null>(null);

  useEffect(() => {
    const token = getAuthToken();
    setAuthenticated(token ? true : false);
  }, []);

  const handleLogin = useCallback((token: string) => {
    setAuthToken(token);
    setAuthenticated(true);
  }, []);

  const handleLogout = useCallback(() => {
    clearAuthToken();
    setAuthenticated(false);
  }, []);

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

  return <Dashboard onLogout={handleLogout} />;
}

interface DashboardProps {
  onLogout: () => void;
}

function Dashboard({ onLogout }: DashboardProps) {
  const [tab, setTab] = usePersistedTab("monitor");
  const [showStatusMessage, setShowStatusMessage] = useState(false);

  // Wire up hooks
  const handleAuthError = useCallback(() => {
    onLogout();
  }, [onLogout]);

  const configMessageRef = useRef<(msg: string) => void>();

  const agents = useAgents({
    authenticated: true,
    onAuthError: handleAuthError,
    onConfigUpdate: (agentId, config) => {
      configHook.handleRemoteConfigUpdate(agentId, config);
    },
  });

  const configHook = useConfig({
    agentId: agents.selectedAgentId,
    authenticated: true,
    onDeviceNameUpdate: agents.updateDeviceName,
    onAuthError: handleAuthError,
    onMessage: useCallback((msg: string) => {
      agents.setMessage(msg);
    }, []),
  });

  const logs = useLogs({
    agentId: agents.selectedAgentId,
    disabled: !agents.selectedAgentOnline,
  });

  // Stabilize onConfigUpdate ref after configHook is created
  useEffect(() => {
    configMessageRef.current = configHook.handleRemoteConfigUpdate as never;
  }, [configHook.handleRemoteConfigUpdate]);

  const handleCommand = useCallback(
    async (command: string, target: string, payload?: unknown) => {
      if (!agents.selectedAgent) return;
      try {
        await sendCommand(agents.selectedAgent.agent.id, command, target, payload);
        agents.setMessage(`Command ${command} sent to ${target}`);
      } catch (err) {
        if (err instanceof Error) {
          agents.setMessage(err.message);
        } else {
          agents.setMessage("Failed to send command");
        }
      }
    },
    [agents.selectedAgent],
  );

  const handleTabChange = useCallback(
    (newTab: TabId) => {
      if (
        tab === "configure" &&
        newTab !== "configure" &&
        configHook.configDirty
      ) {
        const confirmed = window.confirm(
          "You have unsaved configuration changes. Switch tab anyway?",
        );
        if (!confirmed) return;
      }
      setTab(newTab);
    },
    [tab, configHook.configDirty, setTab],
  );

  return (
    <div className="app">
      <aside className="sidebar">
        <div className="sidebar-header">
          <h1>
            GOK-Pi Dashboard
            <span
              className={`connection-dot ${agents.connectionActive ? "online" : ""}`}
              role="status"
              aria-label={
                agents.connectionActive
                  ? "Live updates active"
                  : "Reconnecting"
              }
              title={
                agents.connectionActive
                  ? "Live updates active"
                  : "Reconnecting"
              }
            />
          </h1>
          <button
            className="logout-button"
            onClick={onLogout}
            title="Logout"
          >
            Logout
          </button>
        </div>
        <div className="agent-select-mobile">
          <select
            value={agents.selectedAgentId || ""}
            onChange={(e) =>
              agents.setSelectedAgentId(e.target.value || undefined)
            }
            className="agent-select"
          >
            <option value="">Select agent...</option>
            {Object.values(agents.agents).map((agent) => (
              <option key={agent.agent.id} value={agent.agent.id}>
                {agent.device_name ||
                  agent.agent.hostname ||
                  agent.agent.id}{" "}
                ({agent.connected === false ? "Offline" : "Online"})
              </option>
            ))}
          </select>
        </div>
        <div className="agent-list">
          {Object.values(agents.agents).map((agent) => (
            <button
              key={agent.agent.id}
              className={`agent-card ${
                agent.agent.id === agents.selectedAgentId ? "selected" : ""
              }`}
              onClick={() => agents.setSelectedAgentId(agent.agent.id)}
            >
              <div className="agent-card-header">
                <strong>
                  {agent.device_name ||
                    agent.agent.hostname ||
                    agent.agent.id}
                </strong>
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
        <MessageBanner
          message={agents.message}
          onDismiss={() => agents.setMessage(undefined)}
        />

        {agents.selectedAgent ? (
          <>
            <header>
              <div className="agent-header">
                <h2>
                  {configHook.agentConfig?.device_name ||
                    agents.selectedAgent.agent.hostname}
                </h2>
                <span
                  className={`badge ${
                    agents.selectedAgentOnline ? "online" : "offline"
                  }`}
                >
                  {agents.selectedAgentOnline ? "Connected" : "Offline"}
                </span>
              </div>
              <div className="status-bar">
                <span>
                  Last contact:{" "}
                  {new Date(
                    agents.selectedAgent.last_seen,
                  ).toLocaleTimeString()}
                </span>
                <span className="badge">
                  Device ID: {agents.selectedAgent.agent.id}
                </span>
                <span className="badge">
                  Version {agents.selectedAgent.agent.version}
                </span>
              </div>
            </header>

            <TabBar
              activeTab={tab}
              onTabChange={handleTabChange}
              configDirty={configHook.configDirty}
            />

            <div
              id={`tabpanel-${tab}`}
              role="tabpanel"
              aria-labelledby={`tab-${tab}`}
              className="tab-panel"
            >
              {tab === "monitor" && (
                <MonitorTab
                  selectedAgent={agents.selectedAgent}
                  selectedAgentOnline={agents.selectedAgentOnline}
                  agentConfig={configHook.agentConfig}
                  onCommand={handleCommand}
                />
              )}

              {tab === "prices" && <PricesDashboard />}

              {tab === "configure" && (
                <ConfigTab
                  config={configHook.agentConfig}
                  agentEnv={agents.selectedAgent?.agent.env}
                  draft={configHook.configDraft}
                  loading={configHook.configLoading}
                  saving={configHook.configSaving}
                  dirty={configHook.configDirty}
                  error={configHook.configError}
                  onDraftChange={configHook.handleDraftChange}
                  onSave={configHook.handleConfigSave}
                  onReset={configHook.handleConfigReset}
                  scheduleGoalReached={
                    agents.selectedAgent?.schedule_goal_reached
                  }
                  onResetGoal={(scheduleName) => {
                    const schedule =
                      configHook.agentConfig?.schedules.find(
                        (s) => s.name === scheduleName,
                      );
                    const batteryName =
                      schedule?.battery_name ||
                      (agents.selectedAgent?.telemetry
                        ? Object.keys(agents.selectedAgent.telemetry)[0]
                        : "");
                    handleCommand("reset_goal", batteryName, {
                      schedule_name: scheduleName,
                    });
                  }}
                  isOnline={agents.selectedAgentOnline}
                />
              )}

              {tab === "tools" && (
                <ToolsTab
                  lastStatusMessage={agents.lastStatusMessage}
                  showStatusMessage={showStatusMessage}
                  onToggleStatusMessage={() =>
                    setShowStatusMessage(!showStatusMessage)
                  }
                  statusMessageFrozen={agents.statusMessageFrozen}
                  onToggleStatusMessageFrozen={() =>
                    agents.setStatusMessageFrozen(!agents.statusMessageFrozen)
                  }
                  agentId={agents.selectedAgentId}
                  logsOpen={logs.logsOpen}
                  logs={logs.logs}
                  logsLoading={logs.logsLoading}
                  logsError={logs.logsError}
                  logStream={logs.logStream}
                  logLines={logs.logLines}
                  onToggleLogs={() => logs.setLogsOpen(!logs.logsOpen)}
                  onRefreshLogs={logs.handleLogRefresh}
                  onStreamChange={logs.setLogStream}
                  onLinesChange={logs.setLogLines}
                  logsDisabled={!agents.selectedAgentOnline}
                />
              )}
            </div>
          </>
        ) : (
          <div className="empty-state">
            <h2>No agents connected</h2>
            <p>
              Once a gok-pi agent connects to the control server you will see
              it listed here. Ensure the server URL and shared secret are set
              in the agent configuration.
            </p>
          </div>
        )}
      </main>

      <BottomNav
        activeTab={tab}
        onTabChange={handleTabChange}
        configDirty={configHook.configDirty}
      />
    </div>
  );
}

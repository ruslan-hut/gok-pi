import { useCallback, useEffect, useState } from "react";
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
import { useNavigation } from "./hooks/useNavigation";
import { useOnlineStatus } from "./hooks/useOnlineStatus";
import { useServiceWorker } from "./hooks/useServiceWorker";
import { useTheme } from "./hooks/useTheme";
import type { AppPage } from "./types";
import { TopNav } from "./components/layout/TopNav";
import { TabBar } from "./components/layout/TabBar";
import { BottomNav } from "./components/layout/BottomNav";
import { OverviewPage } from "./components/pages/OverviewPage";
import { MonitorTab } from "./components/monitor/MonitorTab";
import { ConfigTab } from "./components/config/ConfigTab";
import { ToolsTab } from "./components/tools/ToolsTab";
import PricesDashboard from "./components/prices/PricesDashboard";
import { DatabaseInspector } from "./components/pages/DatabaseInspector";
import { MessageBanner } from "./components/shared/MessageBanner";
import { Icon } from "./components/shared/Icon";

export default function App() {
  const [loggedIn, setLoggedIn] = useState(() => !!getAuthToken());
  const [showLogin, setShowLogin] = useState(false);
  const [theme, toggleTheme] = useTheme();
  const browserOnline = useOnlineStatus();
  const { updateAvailable, applyUpdate } = useServiceWorker();

  const handleLogin = useCallback((token: string, expiresAt?: string) => {
    if (token) setAuthToken(token, expiresAt);
    setLoggedIn(true);
    setShowLogin(false);
  }, []);

  const handleLogout = useCallback(() => {
    clearAuthToken();
    setLoggedIn(false);
  }, []);

  if (showLogin) {
    return <Login onLogin={handleLogin} onBack={() => setShowLogin(false)} />;
  }

  return (
    <Dashboard
      readonly={!loggedIn}
      onLoginRequest={() => setShowLogin(true)}
      onLogout={handleLogout}
      theme={theme}
      onToggleTheme={toggleTheme}
      browserOnline={browserOnline}
      updateAvailable={updateAvailable}
      onApplyUpdate={applyUpdate}
    />
  );
}

interface DashboardProps {
  readonly: boolean;
  onLoginRequest: () => void;
  onLogout: () => void;
  theme: "light" | "dark";
  onToggleTheme: () => void;
  browserOnline: boolean;
  updateAvailable: boolean;
  onApplyUpdate: () => void;
}

function Dashboard({ readonly, onLoginRequest, onLogout, theme, onToggleTheme, browserOnline, updateAvailable, onApplyUpdate }: DashboardProps) {
  const [nav, setNav] = useNavigation();
  const [showStatusMessage, setShowStatusMessage] = useState(false);

  const agents = useAgents({
    onConfigUpdate: (agentId, config) => {
      configHook.handleRemoteConfigUpdate(agentId, config);
    },
  });

  // Current agent from navigation
  const currentAgentId = nav.page === "device" ? nav.agentId : undefined;

  // Sync agent selection with navigation
  useEffect(() => {
    if (currentAgentId) {
      agents.setSelectedAgentId(currentAgentId);
    }
  }, [currentAgentId]);

  const configHook = useConfig({
    agentId: currentAgentId,
    onDeviceNameUpdate: agents.updateDeviceName,
    onMessage: useCallback((msg: string) => {
      agents.setMessage(msg);
    }, []),
  });

  const logs = useLogs({
    agentId: currentAgentId,
    disabled: !agents.selectedAgentOnline,
  });

  const handleCommand = useCallback(
    async (command: string, target: string, payload?: unknown) => {
      if (readonly) {
        agents.setMessage("Log in to send commands.");
        return;
      }
      if (!agents.selectedAgent) return;
      try {
        await sendCommand(
          agents.selectedAgent.agent.id,
          command,
          target,
          payload,
        );
        agents.setMessage(`Command ${command} sent to ${target}`);
      } catch (err) {
        agents.setMessage(
          err instanceof Error ? err.message : "Failed to send command",
        );
      }
    },
    [agents.selectedAgent, readonly],
  );

  const handleNavigate = useCallback(
    (newPage: AppPage) => {
      // Warn about unsaved config changes when leaving configure tab
      if (
        nav.page === "device" &&
        nav.tab === "configure" &&
        configHook.configDirty
      ) {
        const leavingConfigure =
          newPage.page !== "device" ||
          (newPage.page === "device" && newPage.tab !== "configure");
        if (leavingConfigure) {
          const confirmed = window.confirm(
            "You have unsaved configuration changes. Navigate away?",
          );
          if (!confirmed) return;
        }
      }
      setNav(newPage);
    },
    [nav, configHook.configDirty, setNav],
  );

  const selectedAgent = agents.selectedAgent;
  const selectedAgentOnline = agents.selectedAgentOnline;

  const deviceName =
    configHook.agentConfig?.device_name ||
    selectedAgent?.device_name ||
    selectedAgent?.agent.hostname ||
    currentAgentId;

  return (
    <div className="app">
      <TopNav
        currentPage={nav}
        onNavigate={handleNavigate}
        connectionActive={agents.connectionActive}
        deviceName={deviceName}
        readonly={readonly}
        onLoginRequest={onLoginRequest}
        onLogout={onLogout}
        theme={theme}
        onToggleTheme={onToggleTheme}
      />

      <main className="content">
        {!browserOnline && (
          <div className="status-banner status-banner-offline">
            <Icon name="cloud_off" size={18} />
            <span>You are offline. Some features may be unavailable.</span>
          </div>
        )}
        {updateAvailable && (
          <div className="status-banner status-banner-update">
            <Icon name="system_update" size={18} />
            <span>A new version is available.</span>
            <button className="status-banner-action" onClick={onApplyUpdate}>
              Update now
            </button>
          </div>
        )}
        <MessageBanner
          message={agents.message}
          onDismiss={() => agents.setMessage(undefined)}
        />

        {nav.page === "overview" && (
          <OverviewPage
            agents={agents.agents}
            onNavigate={handleNavigate}
          />
        )}

        {nav.page === "electricity" && <PricesDashboard readonly={readonly} />}

        {nav.page === "database" && !readonly && <DatabaseInspector />}

        {nav.page === "device" && (
          <>
            {selectedAgent && (
              <header>
                <div className="agent-header">
                  <h2>{deviceName}</h2>
                  <span
                    className={`badge ${selectedAgentOnline ? "online" : "offline"}`}
                  >
                    {selectedAgentOnline ? "Connected" : "Offline"}
                  </span>
                </div>
                <div className="status-bar">
                  <span>
                    Last contact:{" "}
                    {new Date(
                      selectedAgent.last_seen,
                    ).toLocaleTimeString()}
                  </span>
                </div>
              </header>
            )}

            <TabBar
              activeTab={nav.tab}
              onTabChange={(tab) => handleNavigate({ ...nav, tab })}
              configDirty={configHook.configDirty}
            />

            <div className="tab-panel">
              {selectedAgent ? (
                <>
                  {nav.tab === "monitor" && (
                    <MonitorTab
                      selectedAgent={selectedAgent}
                      selectedAgentOnline={selectedAgentOnline}
                      agentConfig={configHook.agentConfig}
                      onCommand={handleCommand}
                      readonly={readonly}
                    />
                  )}

                  {nav.tab === "configure" && (
                    <ConfigTab
                      config={configHook.agentConfig}
                      agentEnv={selectedAgent?.agent.env}
                      agentId={selectedAgent?.agent.id}
                      agentVersion={selectedAgent?.agent.version}
                      draft={configHook.configDraft}
                      loading={configHook.configLoading}
                      saving={configHook.configSaving}
                      dirty={configHook.configDirty}
                      error={configHook.configError}
                      onDraftChange={configHook.handleDraftChange}
                      onSave={configHook.handleConfigSave}
                      onReset={configHook.handleConfigReset}
                      readonly={readonly}
                      scheduleGoalReached={
                        selectedAgent?.schedule_goal_reached
                      }
                      onResetGoal={(scheduleName) => {
                        const schedule =
                          configHook.agentConfig?.schedules.find(
                            (s) => s.name === scheduleName,
                          );
                        const batteryName =
                          schedule?.battery_name ||
                          (selectedAgent?.telemetry
                            ? Object.keys(selectedAgent.telemetry)[0]
                            : "");
                        handleCommand("reset_goal", batteryName, {
                          schedule_name: scheduleName,
                        });
                      }}
                      isOnline={selectedAgentOnline}
                    />
                  )}

                  {nav.tab === "tools" && (
                    <ToolsTab
                      lastStatusMessage={agents.lastStatusMessage}
                      showStatusMessage={showStatusMessage}
                      onToggleStatusMessage={() =>
                        setShowStatusMessage(!showStatusMessage)
                      }
                      statusMessageFrozen={agents.statusMessageFrozen}
                      onToggleStatusMessageFrozen={() =>
                        agents.setStatusMessageFrozen(
                          !agents.statusMessageFrozen,
                        )
                      }
                      agentId={currentAgentId}
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
                      logsDisabled={!selectedAgentOnline}
                    />
                  )}
                </>
              ) : (
                <div className="empty-state">
                  <h2>Agent not found</h2>
                  <p>
                    The selected agent is no longer available.{" "}
                    <button
                      className="button-link"
                      onClick={() => handleNavigate({ page: "overview" })}
                    >
                      Go to overview
                    </button>
                  </p>
                </div>
              )}
            </div>
          </>
        )}
      </main>

      <BottomNav
        currentPage={nav}
        onNavigate={handleNavigate}
        configDirty={configHook.configDirty}
        readonly={readonly}
      />
    </div>
  );
}

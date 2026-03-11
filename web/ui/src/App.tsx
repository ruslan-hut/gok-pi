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
import type { AppPage } from "./types";
import { TopNav } from "./components/layout/TopNav";
import { TabBar } from "./components/layout/TabBar";
import { BottomNav } from "./components/layout/BottomNav";
import { OverviewPage } from "./components/pages/OverviewPage";
import { MonitorTab } from "./components/monitor/MonitorTab";
import { ConfigTab } from "./components/config/ConfigTab";
import { ToolsTab } from "./components/tools/ToolsTab";
import PricesDashboard from "./components/prices/PricesDashboard";
import { MessageBanner } from "./components/shared/MessageBanner";

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
  const [nav, setNav] = useNavigation();
  const [showStatusMessage, setShowStatusMessage] = useState(false);

  const handleAuthError = useCallback(() => {
    onLogout();
  }, [onLogout]);

  const agents = useAgents({
    authenticated: true,
    onAuthError: handleAuthError,
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
    authenticated: true,
    onDeviceNameUpdate: agents.updateDeviceName,
    onAuthError: handleAuthError,
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
    [agents.selectedAgent],
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

  const selectedAgent = currentAgentId
    ? agents.agents[currentAgentId]
    : undefined;
  const selectedAgentOnline = selectedAgent
    ? selectedAgent.connected !== false
    : false;

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
        onLogout={onLogout}
      />

      <main className="content">
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

        {nav.page === "electricity" && <PricesDashboard />}

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
      />
    </div>
  );
}

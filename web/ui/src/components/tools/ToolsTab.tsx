import { StatusMessagePreview } from "./StatusMessagePreview";
import { LogViewer } from "./LogViewer";
import { AgentMaintenance } from "./AgentMaintenance";
import { BatteryScan } from "./BatteryScan";

interface ToolsTabProps {
  // Status message
  lastStatusMessage: string;
  showStatusMessage: boolean;
  onToggleStatusMessage: () => void;
  statusMessageFrozen: boolean;
  onToggleStatusMessageFrozen: () => void;
  // Logs
  agentId?: string;
  logsOpen: boolean;
  logs: string;
  logsLoading: boolean;
  logsError?: string;
  logStream: string;
  logLines: number;
  onToggleLogs: () => void;
  onRefreshLogs: () => Promise<void>;
  onStreamChange: (stream: string) => void;
  onLinesChange: (lines: number) => void;
  logsDisabled: boolean;
  // Agent process
  agentOnline: boolean;
  readonly: boolean;
  // Battery scan
  batteryNames: string[];
}

export function ToolsTab({
  lastStatusMessage,
  showStatusMessage,
  onToggleStatusMessage,
  statusMessageFrozen,
  onToggleStatusMessageFrozen,
  agentId,
  logsOpen,
  logs,
  logsLoading,
  logsError,
  logStream,
  logLines,
  onToggleLogs,
  onRefreshLogs,
  onStreamChange,
  onLinesChange,
  logsDisabled,
  agentOnline,
  readonly,
  batteryNames,
}: ToolsTabProps) {
  return (
    <>
      <StatusMessagePreview
        lastStatusMessage={lastStatusMessage}
        showStatusMessage={showStatusMessage}
        onToggleStatusMessage={onToggleStatusMessage}
        statusMessageFrozen={statusMessageFrozen}
        onToggleStatusMessageFrozen={onToggleStatusMessageFrozen}
      />
      <LogViewer
        isOpen={logsOpen}
        logs={logs}
        loading={logsLoading}
        error={logsError}
        stream={logStream}
        lines={logLines}
        onToggle={onToggleLogs}
        onRefresh={onRefreshLogs}
        onStreamChange={onStreamChange}
        onLinesChange={onLinesChange}
        disabled={logsDisabled}
      />
      {/* Both of these act on the device rather than just reading it, so they
          are shown to logged-in users only — the same line the database
          inspector and the config forms draw. */}
      {!readonly && (
        <BatteryScan agentId={agentId} online={agentOnline} batteryNames={batteryNames} />
      )}
      {!readonly && <AgentMaintenance agentId={agentId} online={agentOnline} />}
    </>
  );
}

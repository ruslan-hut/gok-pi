import { StatusMessagePreview } from "./StatusMessagePreview";
import { LogViewer } from "./LogViewer";

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
        agentId={agentId}
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
    </>
  );
}

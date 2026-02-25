import { useCallback, useEffect, useRef, useState } from "react";
import { fetchAgentLogs } from "../api";

interface UseLogsOptions {
  agentId: string | undefined;
  disabled: boolean;
}

export function useLogs({ agentId, disabled }: UseLogsOptions) {
  const [logsOpen, setLogsOpen] = useState(false);
  const [logs, setLogs] = useState("");
  const [logsLoading, setLogsLoading] = useState(false);
  const [logsError, setLogsError] = useState<string>();
  const [logStream, setLogStream] = useState("agent");
  const [logLines, setLogLines] = useState(500);
  const onRefreshRef = useRef<() => Promise<void>>();

  const handleLogRefresh = useCallback(async () => {
    if (!agentId) return;
    setLogsLoading(true);
    setLogsError(undefined);
    try {
      const logContent = await fetchAgentLogs(agentId, {
        stream: logStream,
        lines: logLines,
      });
      setLogs(logContent);
    } catch (err) {
      setLogsError(err instanceof Error ? err.message : "Failed to fetch logs");
    } finally {
      setLogsLoading(false);
    }
  }, [agentId, logStream, logLines]);

  useEffect(() => {
    onRefreshRef.current = handleLogRefresh;
  }, [handleLogRefresh]);

  // Auto-fetch when opened or settings change
  useEffect(() => {
    if (logsOpen && agentId && !disabled) {
      onRefreshRef.current?.();
    }
  }, [logsOpen, agentId, logStream, logLines, disabled]);

  return {
    logsOpen,
    setLogsOpen,
    logs,
    logsLoading,
    logsError,
    logStream,
    setLogStream,
    logLines,
    setLogLines,
    handleLogRefresh,
  };
}

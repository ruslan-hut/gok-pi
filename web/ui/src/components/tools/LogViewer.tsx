import { useCallback, useEffect, useRef } from "react";
import { CopyButton } from "../shared/CopyButton";
import { Icon } from "../shared/Icon";

interface LogViewerProps {
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

export function LogViewer({
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
  const getLogText = useCallback(() => logs, [logs]);

  // Auto-scroll to bottom when logs update
  useEffect(() => {
    if (isOpen && logContainerRef.current) {
      logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight;
    }
  }, [logs, isOpen]);

  return (
    <section className="config-panel">
      <div
        className="config-panel-header"
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          gap: "1rem",
        }}
      >
        <button
          type="button"
          className="button-link"
          onClick={onToggle}
          aria-expanded={isOpen}
          style={{ cursor: "pointer", margin: 0, flex: "0 0 auto", textDecoration: "none", fontSize: "1.25rem", fontWeight: 600 }}
        >
          Agent Logs
        </button>
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
            <select
              id="log-lines"
              value={lines}
              onChange={(e) => {
                e.stopPropagation();
                onLinesChange(Number(e.target.value));
              }}
              disabled={disabled || loading}
            >
              <option value={500}>500</option>
              <option value={1000}>1000</option>
            </select>
            <button
              onClick={(e) => {
                e.stopPropagation();
                onRefresh();
              }}
              disabled={disabled || loading}
              className="log-viewer-refresh"
              title="Refresh logs"
            >
              <Icon name={loading ? "hourglass_empty" : "refresh"} size={18} />
            </button>
          </div>
        )}
        <span
          className="config-toggle"
          onClick={onToggle}
          style={{ cursor: "pointer", flex: "0 0 auto" }}
        >
          <Icon name={isOpen ? "expand_more" : "chevron_right"} size={20} />
        </span>
      </div>
      {isOpen && (
        <>
          {disabled && (
            <div
              className="offline-warning"
              style={{ marginTop: "1rem", marginBottom: "1rem" }}
            >
              Agent is offline. Logs cannot be fetched.
            </div>
          )}
          {error && <div className="config-error">{error}</div>}
          {loading && logs === "" ? (
            <div className="config-loading">
              <div className="spinner"></div>
              <p>Loading logs...</p>
            </div>
          ) : (
            <div className="text-content-wrapper">
              <CopyButton getText={getLogText} />
              <div ref={logContainerRef} className="log-viewer-content">
                {logs || "No logs available"}
              </div>
            </div>
          )}
        </>
      )}
    </section>
  );
}

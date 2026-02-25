import { useEffect, useRef } from "react";

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
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: "0.5rem",
              }}
            >
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
            <div ref={logContainerRef} className="log-viewer-content">
              {logs || "No logs available"}
            </div>
          )}
        </>
      )}
    </section>
  );
}

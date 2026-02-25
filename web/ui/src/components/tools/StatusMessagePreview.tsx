interface StatusMessagePreviewProps {
  lastStatusMessage: string;
  showStatusMessage: boolean;
  onToggleStatusMessage: () => void;
  statusMessageFrozen: boolean;
  onToggleStatusMessageFrozen: () => void;
}

export function StatusMessagePreview({
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
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
        }}
      >
        <div
          onClick={onToggleStatusMessage}
          style={{
            cursor: "pointer",
            display: "flex",
            alignItems: "center",
            gap: "0.5rem",
            flex: 1,
          }}
        >
          <h3 style={{ margin: 0 }}>Last Status Message</h3>
          {statusMessageFrozen && (
            <span
              className="badge"
              style={{
                borderColor: "#fbbf24",
                color: "#fbbf24",
                fontSize: "0.75rem",
              }}
            >
              Frozen
            </span>
          )}
        </div>
        <div
          style={{ display: "flex", alignItems: "center", gap: "0.5rem" }}
        >
          <button
            type="button"
            className="button-small"
            onClick={(e) => {
              e.stopPropagation();
              onToggleStatusMessageFrozen();
            }}
            title={
              statusMessageFrozen ? "Unfreeze updates" : "Freeze updates"
            }
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
          {lastStatusMessage
            ? (() => {
                try {
                  const parsed = JSON.parse(lastStatusMessage);
                  return JSON.stringify(parsed, null, 2);
                } catch {
                  return lastStatusMessage;
                }
              })()
            : "No status messages received yet"}
        </div>
      )}
    </section>
  );
}

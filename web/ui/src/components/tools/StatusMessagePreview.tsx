import { Icon } from "../shared/Icon";

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
        <button
          type="button"
          className="button-link"
          onClick={onToggleStatusMessage}
          aria-expanded={showStatusMessage}
          style={{
            cursor: "pointer",
            display: "flex",
            alignItems: "center",
            gap: "0.5rem",
            flex: 1,
            textDecoration: "none",
            fontSize: "1.25rem",
            fontWeight: 600,
          }}
        >
          Last Status Message
          {statusMessageFrozen && (
            <span
              className="badge"
              style={{
                borderColor: "var(--color-warning)",
                color: "var(--color-warning)",
                fontSize: "0.75rem",
              }}
            >
              Frozen
            </span>
          )}
        </button>
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
            style={{ fontSize: "0.75rem", padding: "0.25rem 0.5rem", display: "inline-flex", alignItems: "center", gap: "0.25rem" }}
          >
            <Icon name={statusMessageFrozen ? "play_arrow" : "pause"} size={16} />
            {statusMessageFrozen ? "Resume" : "Freeze"}
          </button>
          <span
            className="config-toggle"
            onClick={onToggleStatusMessage}
            style={{ cursor: "pointer" }}
          >
            <Icon name={showStatusMessage ? "expand_more" : "chevron_right"} size={20} />
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

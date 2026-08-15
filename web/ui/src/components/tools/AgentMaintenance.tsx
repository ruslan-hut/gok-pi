import { useState } from "react";
import { restartAgent } from "../../api";
import { Icon } from "../shared/Icon";

interface AgentMaintenanceProps {
  agentId?: string;
  online: boolean;
}

/**
 * AgentMaintenance holds actions on the agent process itself rather than on a
 * battery. Restarting is confirmed in place rather than through a browser
 * dialog: it takes the device off the air for a few seconds, so it should cost
 * a deliberate second click, but it is routine enough not to warrant a modal.
 */
export function AgentMaintenance({ agentId, online }: AgentMaintenanceProps) {
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string>("");
  const [error, setError] = useState<string>("");

  const disabled = !agentId || !online || busy;

  const onRestart = async () => {
    if (!agentId) return;
    if (!confirming) {
      setConfirming(true);
      setMessage("");
      setError("");
      return;
    }
    setConfirming(false);
    setBusy(true);
    setError("");
    try {
      await restartAgent(agentId);
      setMessage("Restart requested. The agent drops off and reconnects within a few seconds.");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Restart failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="config-panel">
      <div className="config-panel-header">
        <h3>Agent process</h3>
      </div>

      <div className="agent-maintenance">
        <p className="config-section-intro">
          Restarts the agent service on the device. Schedules and overrides are re-applied
          from the control server on reconnect; buffered telemetry is replayed, so no history
          is lost. Use it when the agent is reachable but misbehaving — new versions arrive on
          their own through the updater.
        </p>

        <div className="agent-maintenance-actions">
          <button
            type="button"
            className={confirming ? "button-small agent-restart-confirm" : "button-small"}
            onClick={onRestart}
            onBlur={() => setConfirming(false)}
            disabled={disabled}
            title={online ? "Restart the agent service" : "Agent is offline"}
          >
            <Icon name={busy ? "hourglass_empty" : "restart_alt"} size={16} />
            {busy ? "Sending…" : confirming ? "Confirm restart" : "Restart agent"}
          </button>
          {confirming && (
            <small className="form-help-text">Click again to restart, or click away to cancel.</small>
          )}
        </div>

        {!online && (
          <div className="offline-warning">Agent is offline. It cannot be restarted from here.</div>
        )}
        <div className="form-status" aria-live="polite">
          {message && <small className="form-ok">{message}</small>}
          {error && <small className="form-error">{error}</small>}
        </div>
      </div>
    </section>
  );
}

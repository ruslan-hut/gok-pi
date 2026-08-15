import { useEffect, useState } from "react";
import type { EmailProviderStatus, EmailRecipient, EmailReportsConfig } from "../../types";
import { fetchEmailStatus, sendEmailTest } from "../../api";

const DEFAULT: EmailReportsConfig = {
  enabled: false,
  recipients: [],
  send_hour: 7,
};

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

type SubscriptionKey = "daily" | "weekly" | "monthly" | "alerts";

/**
 * One column per thing an address can be sent. There is no agent-wide switch for
 * these any more: a subscription is the whole answer to "does this person get
 * it", which is one rule instead of two that could disagree.
 */
const COLUMNS: { key: SubscriptionKey; label: string; title: string }[] = [
  { key: "daily", label: "Daily", title: "Each morning, covering the previous day: sessions and the price chart." },
  { key: "weekly", label: "Weekly", title: "Monday, covering the previous Mon–Sun, totalled per battery." },
  { key: "monthly", label: "Monthly", title: "Day 1, covering the previous month, totalled per battery." },
  {
    key: "alerts",
    label: "Alerts",
    title: "Sent when the agent stops reporting for 10 minutes, and again when it comes back. Not tied to the send hour.",
  },
];

interface Props {
  value: EmailReportsConfig | null | undefined;
  agentId?: string;
  disabled: boolean;
  readonly?: boolean;
  onChange: (next: EmailReportsConfig | null) => void;
}

export function EmailReportsForm({ value, agentId, disabled, readonly, onChange }: Props) {
  const cfg = value ?? DEFAULT;
  const [status, setStatus] = useState<EmailProviderStatus | null>(null);
  const [testState, setTestState] = useState<{
    busy: boolean;
    message?: string;
    error?: string;
  }>({ busy: false });

  useEffect(() => {
    fetchEmailStatus().then(setStatus).catch(() => setStatus(null));
  }, []);

  const update = (patch: Partial<EmailReportsConfig>) => {
    onChange({ ...cfg, ...patch });
  };

  const updateRecipient = (index: number, patch: Partial<EmailRecipient>) => {
    update({ recipients: cfg.recipients.map((r, i) => (i === index ? { ...r, ...patch } : r)) });
  };

  const addRecipient = () => {
    update({
      recipients: [
        ...cfg.recipients,
        { address: "", daily: false, weekly: false, monthly: false, alerts: false },
      ],
    });
  };

  const removeRecipient = (index: number) => {
    update({ recipients: cfg.recipients.filter((_, i) => i !== index) });
  };

  const locked = disabled || readonly;
  const addresses = cfg.recipients.map((r) => r.address.trim());
  const dailySubscribers = cfg.recipients
    .filter((r) => r.daily)
    .map((r) => r.address.trim())
    .filter((address) => address !== "" && EMAIL_RE.test(address));
  const invalidAddresses = addresses.filter(
    (address) => address !== "" && !EMAIL_RE.test(address),
  );
  const blankCount = addresses.filter((address) => address === "").length;
  const duplicateAddresses = addresses.filter(
    (address, index) =>
      address !== "" &&
      addresses.findIndex((other) => other.toLowerCase() === address.toLowerCase()) !== index,
  );
  const canSendTest = dailySubscribers.length > 0 && invalidAddresses.length === 0;

  const onSendTest = async () => {
    if (!agentId) return;
    setTestState({ busy: true });
    try {
      const res = await sendEmailTest(agentId, dailySubscribers);
      setTestState({
        busy: false,
        message: `Sent test report to ${res.recipients.join(", ")}. Check your inbox.`,
      });
    } catch (err) {
      setTestState({
        busy: false,
        error: err instanceof Error ? err.message : "Failed to send test email",
      });
    }
  };

  return (
    <div className="config-section">
      {status && (
        <div className="config-section-header config-section-header-actions">
          <span
            className={`badge ${status.enabled ? "ok" : "muted"}`}
            title={status.sender ? `Sender: ${status.sender}` : undefined}
          >
            Email provider: {status.enabled ? "active" : status.configured ? "configured" : "not configured"}
          </span>
        </div>
      )}

      <div className="config-form-grid">
        <div className="form-field">
          <label className="switch">
            <input
              type="checkbox"
              checked={cfg.enabled}
              disabled={locked}
              onChange={(e) => update({ enabled: e.target.checked })}
            />
            <span className="switch-slider"></span>
            <span className="switch-label">Send email for this agent</span>
          </label>
          <small className="form-help-text">
            Master switch: while it is off, nothing is sent to anyone below.
          </small>
        </div>

        <div className="form-field">
          <label htmlFor="email-send-hour">Send hour (0–23, agent timezone)</label>
          <input
            id="email-send-hour"
            type="number"
            min={0}
            max={23}
            value={cfg.send_hour}
            disabled={locked}
            onChange={(e) => update({ send_hour: Math.max(0, Math.min(23, Number(e.target.value) || 0)) })}
          />
          <small className="form-help-text">
            When reports go out. Alerts ignore it — they are sent when the outage happens.
          </small>
        </div>
      </div>

      <div className="form-field email-recipients">
        <label>Recipients</label>
        <small className="form-help-text">
          Each address gets exactly what it is ticked for, and nothing else.
        </small>

        {cfg.recipients.length === 0 ? (
          <p className="recipients-empty">No recipients yet — nothing is being sent.</p>
        ) : (
          <div className="recipients-table">
            <table>
              <thead>
                <tr>
                  <th>Address</th>
                  {COLUMNS.map((col) => (
                    <th key={col.key} className="sub" title={col.title}>
                      {col.label}
                    </th>
                  ))}
                  <th className="sub" aria-label="Remove"></th>
                </tr>
              </thead>
              <tbody>
                {cfg.recipients.map((recipient, index) => {
                  const address = recipient.address.trim();
                  const invalid = address !== "" && !EMAIL_RE.test(address);
                  return (
                    <tr key={index}>
                      <td>
                        <input
                          type="email"
                          className={invalid ? "input-invalid" : undefined}
                          value={recipient.address}
                          disabled={locked}
                          placeholder="alice@example.com"
                          aria-label="Recipient email address"
                          onChange={(e) => updateRecipient(index, { address: e.target.value })}
                        />
                      </td>
                      {COLUMNS.map((col) => (
                        <td key={col.key} className="sub" data-label={col.label}>
                          <input
                            type="checkbox"
                            checked={recipient[col.key]}
                            disabled={locked}
                            aria-label={`${col.label} for ${address || "this recipient"}`}
                            title={col.title}
                            onChange={(e) => updateRecipient(index, { [col.key]: e.target.checked })}
                          />
                        </td>
                      ))}
                      <td className="sub">
                        <button
                          type="button"
                          className="button-icon"
                          disabled={locked}
                          aria-label={`Remove ${address || "recipient"}`}
                          title="Remove this recipient"
                          onClick={() => removeRecipient(index)}
                        >
                          ×
                        </button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}

        {invalidAddresses.length > 0 && (
          <small className="form-error">
            Invalid email{invalidAddresses.length > 1 ? "s" : ""}: {invalidAddresses.join(", ")}
          </small>
        )}
        {duplicateAddresses.length > 0 && (
          <small className="form-error">
            Listed twice: {duplicateAddresses.join(", ")}. Each copy is sent separately.
          </small>
        )}
        {blankCount > 0 && (
          <small className="form-help-text">
            {blankCount === 1 ? "The empty row is" : `${blankCount} empty rows are`} dropped on save.
          </small>
        )}

        <div className="recipients-actions">
          <button type="button" className="button-small" disabled={locked} onClick={addRecipient}>
            Add recipient
          </button>

          {agentId && status?.enabled && (
            <button
              type="button"
              className="button-small"
              onClick={onSendTest}
              disabled={locked || testState.busy || !canSendTest}
              title={
                canSendTest
                  ? "Send a [TEST] daily report for yesterday to the daily subscribers above."
                  : "Subscribe at least one valid address to the daily report first."
              }
            >
              {testState.busy ? "Sending…" : "Send test email"}
            </button>
          )}
        </div>

        <div className="form-status" aria-live="polite">
          {testState.message && (
            <small className="form-ok">{testState.message}</small>
          )}
          {testState.error && <small className="form-error">{testState.error}</small>}
        </div>
        {agentId && status && !status.enabled && (
          <small className="form-help-text">
            Test sending is unavailable: server email provider is{" "}
            {status.configured ? "configured but inactive" : "not configured"}.
          </small>
        )}
      </div>
    </div>
  );
}

import { useEffect, useState } from "react";
import type { EmailProviderStatus, EmailRecipient, EmailReportsConfig } from "../../types";
import { fetchEmailStatus, sendEmailTest } from "../../api";

const DEFAULT: EmailReportsConfig = {
  enabled: false,
  recipients: [],
  daily: true,
  weekly: false,
  monthly: false,
  send_hour: 7,
};

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

type SubscriptionKey = "daily" | "weekly" | "monthly" | "alerts";

const COLUMNS: { key: SubscriptionKey; label: string; title: string }[] = [
  { key: "daily", label: "Daily", title: "Yesterday's sessions, each morning." },
  { key: "weekly", label: "Weekly", title: "Previous Mon–Sun, sent Monday." },
  { key: "monthly", label: "Monthly", title: "Previous month, sent on day 1." },
  {
    key: "alerts",
    label: "Alerts",
    title: "Sent when the agent stops reporting for 10 minutes, and again when it returns.",
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
    const next = cfg.recipients.map((r, i) => (i === index ? { ...r, ...patch } : r));
    update({ recipients: next });
  };

  const addRecipient = () => {
    // A new address starts subscribed to whatever reports the agent sends, and
    // not to alerts: being added to a mailing list should not page anyone.
    update({
      recipients: [
        ...cfg.recipients,
        { address: "", daily: cfg.daily, weekly: cfg.weekly, monthly: cfg.monthly, alerts: false },
      ],
    });
  };

  const removeRecipient = (index: number) => {
    update({ recipients: cfg.recipients.filter((_, i) => i !== index) });
  };

  const locked = disabled || readonly;
  const dailySubscribers = cfg.recipients
    .map((r) => r.address.trim())
    .filter((address, i) => address !== "" && cfg.recipients[i].daily);
  const invalidAddresses = cfg.recipients
    .map((r) => r.address.trim())
    .filter((address) => address !== "" && !EMAIL_RE.test(address));

  const onSendTest = async () => {
    if (!agentId) return;
    if (dailySubscribers.length === 0) {
      setTestState({ busy: false, error: "Subscribe at least one recipient to the daily report first." });
      return;
    }
    if (invalidAddresses.length > 0) {
      setTestState({
        busy: false,
        error: `Invalid email${invalidAddresses.length > 1 ? "s" : ""}: ${invalidAddresses.join(", ")}`,
      });
      return;
    }
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
      <div className="config-section-header config-section-header-actions">
        {status && (
          <span
            className={`badge ${status.enabled ? "ok" : "muted"}`}
            title={status.sender ? `Sender: ${status.sender}` : undefined}
          >
            Email provider: {status.enabled ? "active" : status.configured ? "configured" : "not configured"}
          </span>
        )}
      </div>

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
            Master switch: no reports and no alerts are sent while this is off.
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
            disabled={locked || !cfg.enabled}
            onChange={(e) => update({ send_hour: Math.max(0, Math.min(23, Number(e.target.value) || 0)) })}
          />
          <small className="form-help-text">Alerts ignore this — they are sent when the outage happens.</small>
        </div>

        <div className="form-field">
          <label>Reports this agent produces</label>
          <ReportToggle
            label="Daily"
            help="Each morning, covering the previous day, with session detail and the price chart."
            checked={cfg.daily}
            disabled={locked || !cfg.enabled}
            onChange={(daily) => update({ daily })}
          />
          <ReportToggle
            label="Weekly"
            help="Monday, covering the previous Mon–Sun, totalled per battery."
            checked={cfg.weekly}
            disabled={locked || !cfg.enabled}
            onChange={(weekly) => update({ weekly })}
          />
          <ReportToggle
            label="Monthly"
            help="Day 1, covering the previous month, totalled per battery."
            checked={cfg.monthly}
            disabled={locked || !cfg.enabled}
            onChange={(monthly) => update({ monthly })}
          />
        </div>

        <div className="form-field">
          <label>Recipients</label>
          <small className="form-help-text">
            Each address receives only what it is subscribed to. A report also needs its
            switch above; alerts do not.
          </small>

          {cfg.recipients.length === 0 ? (
            <p className="recipients-empty">No recipients yet.</p>
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
                    <th className="sub"></th>
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
                          <td key={col.key} className="sub">
                            <input
                              type="checkbox"
                              checked={recipient[col.key]}
                              disabled={locked || !cfg.enabled || (col.key !== "alerts" && !cfg[col.key])}
                              aria-label={`${col.label} for ${address || "this recipient"}`}
                              title={
                                col.key !== "alerts" && !cfg[col.key]
                                  ? `${col.label} reports are switched off for this agent.`
                                  : col.title
                              }
                              onChange={(e) => updateRecipient(index, { [col.key]: e.target.checked })}
                            />
                          </td>
                        ))}
                        <td className="sub">
                          <button
                            type="button"
                            className="recipient-remove"
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

          <div className="recipients-actions">
            <button type="button" className="button-small" disabled={locked} onClick={addRecipient}>
              Add recipient
            </button>

            {agentId && status?.enabled && (
              <button
                type="button"
                className="button-small"
                onClick={onSendTest}
                disabled={locked || testState.busy}
                title="Send a [TEST] daily report for yesterday to the daily subscribers above."
              >
                {testState.busy ? "Sending…" : "Send test email"}
              </button>
            )}
          </div>

          {testState.message && (
            <small className="form-help-text" style={{ color: "var(--color-success)" }}>
              {testState.message}
            </small>
          )}
          {testState.error && <small className="form-error">{testState.error}</small>}
          {agentId && status && !status.enabled && (
            <small className="form-help-text">
              Test sending is unavailable: server email provider is{" "}
              {status.configured ? "configured but inactive" : "not configured"}.
            </small>
          )}
        </div>
      </div>
    </div>
  );
}

function ReportToggle({
  label,
  help,
  checked,
  disabled,
  onChange,
}: {
  label: string;
  help: string;
  checked: boolean;
  disabled: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <div className="report-toggle">
      <label className="switch">
        <input
          type="checkbox"
          checked={checked}
          disabled={disabled}
          onChange={(e) => onChange(e.target.checked)}
        />
        <span className="switch-slider"></span>
        <span className="switch-label">{label}</span>
      </label>
      <small className="form-help-text">{help}</small>
    </div>
  );
}

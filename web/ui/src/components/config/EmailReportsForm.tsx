import { useEffect, useState } from "react";
import type { EmailProviderStatus, EmailReportsConfig } from "../../types";
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

interface Props {
  value: EmailReportsConfig | null | undefined;
  agentId?: string;
  disabled: boolean;
  readonly?: boolean;
  onChange: (next: EmailReportsConfig | null) => void;
}

export function EmailReportsForm({ value, agentId, disabled, readonly, onChange }: Props) {
  const cfg = value ?? DEFAULT;
  const [recipientsText, setRecipientsText] = useState<string>(cfg.recipients.join(", "));
  const [recipientError, setRecipientError] = useState<string>("");
  const [status, setStatus] = useState<EmailProviderStatus | null>(null);
  const [testState, setTestState] = useState<{
    busy: boolean;
    message?: string;
    error?: string;
  }>({ busy: false });

  useEffect(() => {
    fetchEmailStatus().then(setStatus).catch(() => setStatus(null));
  }, []);

  // Re-sync the local recipients string when the parent replaces the value.
  useEffect(() => {
    setRecipientsText((value?.recipients ?? []).join(", "));
  }, [value?.recipients]);

  const update = (patch: Partial<EmailReportsConfig>) => {
    onChange({ ...cfg, ...patch });
  };

  const onRecipientsBlur = () => {
    const parts = recipientsText
      .split(/[,;\n]/)
      .map((s) => s.trim())
      .filter(Boolean);
    const invalid = parts.filter((p) => !EMAIL_RE.test(p));
    if (invalid.length > 0) {
      setRecipientError(`Invalid email${invalid.length > 1 ? "s" : ""}: ${invalid.join(", ")}`);
    } else {
      setRecipientError("");
    }
    update({ recipients: parts });
  };

  const parseRecipients = (): string[] =>
    recipientsText
      .split(/[,;\n]/)
      .map((s) => s.trim())
      .filter(Boolean);

  const onSendTest = async () => {
    if (!agentId) return;
    const parts = parseRecipients();
    const invalid = parts.filter((p) => !EMAIL_RE.test(p));
    if (parts.length === 0) {
      setTestState({ busy: false, error: "Add at least one recipient before testing." });
      return;
    }
    if (invalid.length > 0) {
      setTestState({ busy: false, error: `Invalid email${invalid.length > 1 ? "s" : ""}: ${invalid.join(", ")}` });
      return;
    }
    setTestState({ busy: true });
    try {
      const res = await sendEmailTest(agentId, parts);
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
      <div className="config-section-header">
        <h4>Email reports</h4>
        {status && (
          <span
            className={`badge ${status.enabled ? "badge-success" : "badge-warning"}`}
            title={status.sender ? `Sender: ${status.sender}` : undefined}
          >
            Email provider: {status.enabled ? "active" : status.configured ? "configured" : "not configured"}
          </span>
        )}
      </div>

      <div className="config-form-grid">
        <div className="form-field">
          <label>
            <input
              type="checkbox"
              checked={cfg.enabled}
              disabled={disabled || readonly}
              onChange={(e) => update({ enabled: e.target.checked })}
            />
            {" "}Enable email reports
          </label>
          <small className="form-help-text">
            When enabled and at least one report is selected, the control server will email the listed recipients.
          </small>
        </div>

        <div className="form-field">
          <label htmlFor="email-recipients">Recipients</label>
          <input
            id="email-recipients"
            type="text"
            value={recipientsText}
            disabled={disabled || readonly}
            placeholder="alice@example.com, bob@example.com"
            onChange={(e) => setRecipientsText(e.target.value)}
            onBlur={onRecipientsBlur}
          />
          <small className="form-help-text">Comma-separated list of email addresses.</small>
          {recipientError && <small className="form-error">{recipientError}</small>}

          {agentId && status?.enabled && (
            <div style={{ marginTop: 8, display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
              <button
                type="button"
                className="button-small"
                onClick={onSendTest}
                disabled={disabled || readonly || testState.busy}
                title="Send a [TEST] daily report for yesterday to the recipients above."
              >
                {testState.busy ? "Sending…" : "Send test email"}
              </button>
              {testState.message && <small className="form-help-text" style={{ color: "var(--color-success)" }}>{testState.message}</small>}
              {testState.error && <small className="form-error">{testState.error}</small>}
            </div>
          )}
          {agentId && status && !status.enabled && (
            <small className="form-help-text" style={{ marginTop: 8 }}>
              Test sending is unavailable: server email provider is {status.configured ? "configured but inactive" : "not configured"}.
            </small>
          )}
        </div>

        <div className="form-field">
          <label htmlFor="email-send-hour">Send hour (0–23, agent timezone)</label>
          <input
            id="email-send-hour"
            type="number"
            min={0}
            max={23}
            value={cfg.send_hour}
            disabled={disabled || readonly || !cfg.enabled}
            onChange={(e) => update({ send_hour: Math.max(0, Math.min(23, Number(e.target.value) || 0)) })}
          />
        </div>

        <div className="form-field">
          <label>Reports</label>
          <label>
            <input
              type="checkbox"
              checked={cfg.daily}
              disabled={disabled || readonly || !cfg.enabled}
              onChange={(e) => update({ daily: e.target.checked })}
            />
            {" "}Daily — sent each morning for the previous day, with session details and price chart.
          </label>
          <label>
            <input
              type="checkbox"
              checked={cfg.weekly}
              disabled={disabled || readonly || !cfg.enabled}
              onChange={(e) => update({ weekly: e.target.checked })}
            />
            {" "}Weekly — sent Monday for the previous Mon–Sun, aggregated per battery.
          </label>
          <label>
            <input
              type="checkbox"
              checked={cfg.monthly}
              disabled={disabled || readonly || !cfg.enabled}
              onChange={(e) => update({ monthly: e.target.checked })}
            />
            {" "}Monthly — sent on day 1 for the previous month, aggregated per battery.
          </label>
        </div>
      </div>
    </div>
  );
}

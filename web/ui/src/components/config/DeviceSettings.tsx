import type { AgentConfig } from "../../types";

interface DeviceSettingsProps {
  config: AgentConfig;
  agentEnv?: string;
  disabled: boolean;
  onChange: (config: AgentConfig) => void;
}

export function DeviceSettings({
  config,
  agentEnv,
  disabled,
  onChange,
}: DeviceSettingsProps) {
  return (
    <div className="config-section">
      <h4>Device Settings</h4>
      <div className="config-form-grid">
        <div className="form-field">
          <label htmlFor="device-name">Device Name</label>
          <input
            id="device-name"
            type="text"
            value={config.device_name ?? ""}
            onChange={(e) =>
              onChange({ ...config, device_name: e.target.value })
            }
            disabled={disabled}
            placeholder="My Battery Controller"
          />
        </div>
        <div className="form-field">
          <label htmlFor="env">Logging Level (env)</label>
          <select
            id="env"
            value={config.env ?? agentEnv ?? ""}
            onChange={(e) => onChange({ ...config, env: e.target.value })}
            disabled={disabled}
          >
            <option value="">Select...</option>
            <option value="local">local</option>
            <option value="dev">dev</option>
            <option value="prod">prod</option>
          </select>
          <small className="form-help-text">
            Controls logging level: local (debug to stdout), dev (debug to
            file), prod (info to file)
          </small>
        </div>
        <div className="form-field">
          <label htmlFor="timezone">Timezone</label>
          <input
            id="timezone"
            type="text"
            value={config.timezone ?? ""}
            onChange={(e) =>
              onChange({ ...config, timezone: e.target.value })
            }
            disabled={disabled}
            placeholder="UTC"
          />
          <small className="form-help-text">
            IANA timezone name (e.g., "America/New_York", "Europe/London",
            "UTC"). Used for schedule time parsing.
          </small>
        </div>
      </div>
    </div>
  );
}

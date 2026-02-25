import { useState } from "react";
import { Icon } from "../shared/Icon";
import type { BatteryConfig } from "../../types";

interface BatteryConfigFormProps {
  battery: BatteryConfig;
  onChange: (battery: BatteryConfig) => void;
  onRemove: () => void;
  disabled?: boolean;
}

export function BatteryConfigForm({
  battery,
  onChange,
  onRemove,
  disabled,
}: BatteryConfigFormProps) {
  const [collapsed, setCollapsed] = useState(true);

  return (
    <div className="config-item config-item-battery">
      <div
        className={`config-item-header ${collapsed ? "config-item-header-collapsed" : ""}`}
        onClick={() => setCollapsed(!collapsed)}
        style={{ cursor: "pointer" }}
      >
        <h5>
          {battery.name || "Unnamed Battery"}
          <span className="config-item-toggle">
            <Icon name={collapsed ? "chevron_right" : "expand_more"} size={18} />
          </span>
        </h5>
        <div
          className="config-item-header-actions"
          onClick={(e) => e.stopPropagation()}
        >
          <label className="switch">
            <input
              type="checkbox"
              checked={battery.enabled}
              onChange={(e) =>
                onChange({ ...battery, enabled: e.target.checked })
              }
              disabled={disabled}
            />
            <span className="switch-slider"></span>
            <span className="switch-label">Enabled</span>
          </label>
        </div>
      </div>
      {collapsed && (
        <div className="config-item-summary">
          {battery.url?.replace(/^https?:\/\//, "") || "No URL"}
          {" · "}
          {battery.capacity_limit > 0
            ? `${battery.capacity_limit} Wh`
            : "No limit"}
        </div>
      )}
      {!collapsed && (
        <div className="config-form-grid">
          <div className="form-field">
            <label htmlFor={`battery-name-${battery.name || "new"}`}>
              Name *
            </label>
            <input
              id={`battery-name-${battery.name || "new"}`}
              type="text"
              value={battery.name}
              onChange={(e) => onChange({ ...battery, name: e.target.value })}
              disabled={disabled}
              required
              placeholder="battery-1"
            />
          </div>
          <div className="form-field">
            <label htmlFor={`battery-url-${battery.name || "new"}`}>
              URL *
            </label>
            <input
              id={`battery-url-${battery.name || "new"}`}
              type="url"
              value={battery.url}
              onChange={(e) => onChange({ ...battery, url: e.target.value })}
              disabled={disabled}
              required
              placeholder="http://192.168.1.100"
            />
          </div>
          <div className="form-field">
            <label htmlFor={`battery-token-${battery.name || "new"}`}>
              Token *
            </label>
            <input
              id={`battery-token-${battery.name || "new"}`}
              type="password"
              value={battery.token}
              onChange={(e) =>
                onChange({ ...battery, token: e.target.value })
              }
              disabled={disabled}
              required
              placeholder="API token"
            />
          </div>
          <div className="form-field">
            <label htmlFor={`battery-capacity-${battery.name || "new"}`}>
              Capacity Limit (Wh)
            </label>
            <input
              id={`battery-capacity-${battery.name || "new"}`}
              type="number"
              value={battery.capacity_limit}
              onChange={(e) =>
                onChange({
                  ...battery,
                  capacity_limit: Number(e.target.value),
                })
              }
              disabled={disabled}
              min="0"
              step="1"
            />
          </div>
          <div className="form-field-pair">
            <div className="form-field">
              <label htmlFor={`battery-power-${battery.name || "new"}`}>
                Power Limit (W)
              </label>
              <input
                id={`battery-power-${battery.name || "new"}`}
                type="number"
                value={battery.power_limit ?? 0}
                onChange={(e) =>
                  onChange({
                    ...battery,
                    power_limit: Number(e.target.value) || undefined,
                  })
                }
                disabled={disabled}
                min="0"
                step="1"
                placeholder="Default power limit"
              />
            </div>
            <div className="form-field">
              <label htmlFor={`battery-soc-${battery.name || "new"}`}>
                SoC Limit (%)
              </label>
              <input
                id={`battery-soc-${battery.name || "new"}`}
                type="number"
                value={battery.soc_limit ?? 0}
                onChange={(e) =>
                  onChange({
                    ...battery,
                    soc_limit: Number(e.target.value) || undefined,
                  })
                }
                disabled={disabled}
                min="0"
                max="100"
                step="0.1"
                placeholder="Default SoC limit"
              />
            </div>
          </div>
          <small className="form-help-text">
            Used when no schedule is active
          </small>
          <button
            type="button"
            className="config-item-remove"
            onClick={onRemove}
            disabled={disabled}
          >
            Remove Battery
          </button>
        </div>
      )}
    </div>
  );
}

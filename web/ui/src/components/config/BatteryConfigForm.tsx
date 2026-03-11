import { useState } from "react";
import { Icon } from "../shared/Icon";
import type { BatteryConfig } from "../../types";

interface BatteryConfigFormProps {
  index: number;
  battery: BatteryConfig;
  onChange: (battery: BatteryConfig) => void;
  onRemove: () => void;
  disabled?: boolean;
  readonly?: boolean;
}

export function BatteryConfigForm({
  index,
  battery,
  onChange,
  onRemove,
  disabled,
  readonly,
}: BatteryConfigFormProps) {
  const [collapsed, setCollapsed] = useState(true);

  return (
    <div className="config-item config-item-battery">
      <div
        className={`config-item-header ${collapsed ? "config-item-header-collapsed" : ""}`}
        onClick={() => setCollapsed(!collapsed)}
        onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); setCollapsed(!collapsed); } }}
        tabIndex={0}
        role="button"
        aria-expanded={!collapsed}
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
          <label className="switch">
            <input
              type="checkbox"
              checked={battery.auto_schedule ?? false}
              onChange={(e) =>
                onChange({ ...battery, auto_schedule: e.target.checked })
              }
              disabled={disabled}
            />
            <span className="switch-slider"></span>
            <span className="switch-label">Auto Schedule</span>
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
            <label htmlFor={`battery-name-${index}`}>
              Name *
            </label>
            <input
              id={`battery-name-${index}`}
              type="text"
              value={battery.name}
              onChange={(e) => onChange({ ...battery, name: e.target.value })}
              disabled={disabled}
              required
              placeholder="battery-1"
            />
          </div>
          <div className="form-field">
            <label htmlFor={`battery-url-${index}`}>
              URL *
            </label>
            <input
              id={`battery-url-${index}`}
              type="url"
              value={battery.url}
              onChange={(e) => onChange({ ...battery, url: e.target.value })}
              disabled={disabled}
              required
              placeholder="http://192.168.1.100"
            />
          </div>
          <div className="form-field">
            <label htmlFor={`battery-token-${index}`}>
              Token *
            </label>
            <input
              id={`battery-token-${index}`}
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
            <label htmlFor={`battery-capacity-${index}`}>
              Capacity Limit (Wh)
            </label>
            <input
              id={`battery-capacity-${index}`}
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
              <label htmlFor={`battery-power-${index}`}>
                Power Limit (W)
              </label>
              <input
                id={`battery-power-${index}`}
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
              <label htmlFor={`battery-soc-${index}`}>
                SoC Limit (%)
              </label>
              <input
                id={`battery-soc-${index}`}
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
          {!readonly && (
            <button
              type="button"
              className="config-item-remove"
              onClick={onRemove}
              disabled={disabled}
            >
              Remove Battery
            </button>
          )}
        </div>
      )}
    </div>
  );
}

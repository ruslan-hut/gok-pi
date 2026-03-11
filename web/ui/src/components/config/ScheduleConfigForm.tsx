import { useState } from "react";
import { Icon } from "../shared/Icon";
import type { ScheduleConfig } from "../../types";

interface ScheduleConfigFormProps {
  index: number;
  schedule: ScheduleConfig;
  batteryNames: string[];
  onChange: (schedule: ScheduleConfig) => void;
  onRemove: () => void;
  disabled?: boolean;
  readonly?: boolean;
  goalReachedAt?: string;
  onResetGoal?: () => void;
  isOnline?: boolean;
}

function formatGoalReachedTime(isoTime: string): string {
  try {
    const date = new Date(isoTime);
    return date.toLocaleString([], {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    });
  } catch {
    return isoTime;
  }
}

export function ScheduleConfigForm({
  index,
  schedule,
  batteryNames,
  onChange,
  onRemove,
  disabled,
  readonly,
  goalReachedAt,
  onResetGoal,
  isOnline,
}: ScheduleConfigFormProps) {
  const [collapsed, setCollapsed] = useState(true);

  return (
    <div className="config-item config-item-schedule">
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
          {schedule.name || schedule.battery_name || "Unnamed Schedule"}
          {schedule.type && (
            <span
              className="badge"
              style={{ marginLeft: "8px", fontSize: "0.8em" }}
            >
              {schedule.type === "charge" ? "Charge" : "Discharge"}
            </span>
          )}
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
              checked={schedule.enabled}
              onChange={(e) =>
                onChange({ ...schedule, enabled: e.target.checked })
              }
              disabled={disabled}
            />
            <span className="switch-slider"></span>
            <span className="switch-label">Enabled</span>
          </label>
          <label className="switch">
            <input
              type="checkbox"
              checked={schedule.run_once ?? false}
              onChange={(e) =>
                onChange({ ...schedule, run_once: e.target.checked })
              }
              disabled={disabled}
            />
            <span className="switch-slider"></span>
            <span className="switch-label">Run Once</span>
          </label>
        </div>
      </div>
      {schedule.run_once && goalReachedAt && (
        <div
          className="config-item-goal-badge"
          title={`Goal reached at ${goalReachedAt}`}
        >
          <span className="goal-badge-text">
            <Icon name="check_circle" size={16} /> Goal reached {formatGoalReachedTime(goalReachedAt)}
          </span>
          {onResetGoal && !readonly && (
            <button
              type="button"
              className="goal-reset-button"
              onClick={onResetGoal}
              disabled={!isOnline}
              title={
                isOnline
                  ? "Reset goal to allow schedule to run again today"
                  : "Agent offline - cannot reset goal"
              }
            >
              <Icon name="restart_alt" size={16} /> Reset
            </button>
          )}
        </div>
      )}
      {collapsed && (
        <div className="config-item-summary">
          {schedule.start_time}–{schedule.stop_time} ·{" "}
          {schedule.battery_name || "No battery"} · {schedule.power_limit}W /{" "}
          {schedule.soc_limit}%
        </div>
      )}
      {!collapsed && (
        <div className="config-form-grid">
          <div className="form-field">
            <label
              htmlFor={`schedule-name-${index}`}
            >
              Schedule Name
            </label>
            <input
              id={`schedule-name-${index}`}
              type="text"
              value={schedule.name ?? ""}
              onChange={(e) =>
                onChange({ ...schedule, name: e.target.value })
              }
              disabled={disabled}
              placeholder="Evening Discharge"
            />
          </div>
          <div className="form-field">
            <label
              htmlFor={`schedule-type-${index}`}
            >
              Type *
            </label>
            <select
              id={`schedule-type-${index}`}
              value={schedule.type || "discharge"}
              onChange={(e) =>
                onChange({ ...schedule, type: e.target.value })
              }
              disabled={disabled}
              required
            >
              <option value="discharge">Discharge</option>
              <option value="charge">Charge</option>
            </select>
          </div>
          <div className="form-field">
            <label
              htmlFor={`schedule-battery-${index}`}
            >
              Battery Name *
            </label>
            <select
              id={`schedule-battery-${index}`}
              value={schedule.battery_name}
              onChange={(e) =>
                onChange({ ...schedule, battery_name: e.target.value })
              }
              disabled={disabled}
              required
            >
              <option value="">Select battery...</option>
              {batteryNames.map((name) => (
                <option key={name} value={name}>
                  {name}
                </option>
              ))}
            </select>
          </div>
          <div className="form-field-pair">
            <div className="form-field">
              <label
                htmlFor={`schedule-start-${index}`}
              >
                Start Time *
              </label>
              <input
                id={`schedule-start-${index}`}
                type="time"
                value={schedule.start_time}
                onChange={(e) =>
                  onChange({ ...schedule, start_time: e.target.value })
                }
                disabled={disabled}
                required
              />
            </div>
            <div className="form-field">
              <label
                htmlFor={`schedule-stop-${index}`}
              >
                Stop Time *
              </label>
              <input
                id={`schedule-stop-${index}`}
                type="time"
                value={schedule.stop_time}
                onChange={(e) =>
                  onChange({ ...schedule, stop_time: e.target.value })
                }
                disabled={disabled}
                required
              />
            </div>
          </div>
          <div className="form-field-pair">
            <div className="form-field">
              <label
                htmlFor={`schedule-power-${index}`}
              >
                Power Limit (W) *
              </label>
              <input
                id={`schedule-power-${index}`}
                type="number"
                value={schedule.power_limit}
                onChange={(e) =>
                  onChange({
                    ...schedule,
                    power_limit: Number(e.target.value),
                  })
                }
                disabled={disabled}
                required
                min="0"
                step="1"
              />
            </div>
            <div className="form-field">
              <label
                htmlFor={`schedule-soc-${index}`}
              >
                SoC Limit (%) *
              </label>
              <input
                id={`schedule-soc-${index}`}
                type="number"
                value={schedule.soc_limit}
                onChange={(e) =>
                  onChange({
                    ...schedule,
                    soc_limit: Number(e.target.value),
                  })
                }
                disabled={disabled}
                required
                min="0"
                max="100"
                step="0.1"
              />
            </div>
          </div>
          {!readonly && (
            <button
              type="button"
              className="config-item-remove"
              onClick={onRemove}
              disabled={disabled}
            >
              Remove Schedule
            </button>
          )}
        </div>
      )}
    </div>
  );
}

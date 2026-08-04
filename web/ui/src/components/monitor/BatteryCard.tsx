import { useState } from "react";
import type { MouseEvent } from "react";
import { Metric } from "./Metric";
import { Icon } from "../shared/Icon";
import type { BatteryConfig, CommandState, TelemetrySnapshot } from "../../types";

const defaultCommandState: CommandState = {
  power: 500,
  powerLimit: 500,
  socLimit: 50,
};

interface BatteryCardProps {
  snapshot: TelemetrySnapshot;
  batteryConfig?: BatteryConfig;
  onCommand: (command: string, target: string, payload?: unknown) => void;
  isOnline: boolean;
  hasGoalReachedToday?: boolean;
  readonly?: boolean;
}

export function BatteryCard({
  snapshot,
  batteryConfig,
  onCommand,
  isOnline,
  hasGoalReachedToday,
  readonly,
}: BatteryCardProps) {
  const [expanded, setExpanded] = useState(false);
  const [commandState, setCommandState] =
    useState<CommandState>(defaultCommandState);

  const { name } = snapshot;
  const controlsDisabled = !isOnline;

  const hasConfigLimits =
    batteryConfig &&
    ((batteryConfig.power_limit !== undefined &&
      batteryConfig.power_limit > 0) ||
      (batteryConfig.soc_limit !== undefined && batteryConfig.soc_limit > 0));

  const getStatusBadgeClass = (status: string) => {
    switch (status) {
      case "Connected":
        return "online";
      case "Disconnected":
        return "offline";
      case "Disabled":
        return "disabled";
      default:
        return "offline";
    }
  };

  const renderOperatingMode = (mode: string | undefined | null): string => {
    if (!mode) return "n/a";
    switch (mode) {
      case "1":
        return "MANUAL";
      case "2":
        return "AUTO";
      case "10":
        return "SERVICE";
      default:
        return mode;
    }
  };

  const isManualMode = snapshot.operating_mode === "1";
  const isServiceMode = snapshot.operating_mode === "10";

  const handleHeaderClick = (e: MouseEvent) => {
    const target = e.target as HTMLElement;
    if (
      target.closest(".badge") ||
      target.closest("button") ||
      target.closest("input")
    )
      return;
    setExpanded(!expanded);
  };

  return (
    <div
      className={`card battery-card ${expanded ? "expanded" : ""} ${isManualMode ? "manual-mode" : ""} ${isServiceMode ? "service-mode" : ""}`}
    >
      <h2
        className="battery-card-header"
        onClick={handleHeaderClick}
        onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); setExpanded(!expanded); } }}
        tabIndex={0}
        role="button"
        aria-expanded={expanded}
      >
        <span className="battery-card-title">{name}</span>
        <span className="battery-card-badges">
          <span
            className={`badge ${getStatusBadgeClass(snapshot.status || "Disconnected")}`}
          >
            {snapshot.status || "Disconnected"}
          </span>
          {hasGoalReachedToday && (
            <span
              className="goal-reached-icon"
              title="Schedule goal reached today"
              style={{
                color: "var(--color-success)",
                marginLeft: "0.25rem",
              }}
            >
              <Icon name="check_circle" size={18} />
            </span>
          )}
          <span
            className={`badge ${snapshot.battery_discharging ? "online" : "offline"}`}
          >
            {snapshot.battery_discharging
              ? "Discharging"
              : snapshot.battery_charging
                ? "Charging"
                : "Idle"}
          </span>
          {snapshot.override_source === "charger" && (
            <span
              className="badge"
              title="An EV charging session is driving this battery; it overrides the schedule until the session ends."
            >
              <Icon name="ev_station" size={14} /> EV session
            </span>
          )}
          {!readonly && (
            <span
              className="battery-card-toggle"
              title={expanded ? "Collapse controls" : "Expand controls"}
            >
              <Icon name={expanded ? "expand_more" : "chevron_right"} size={18} />
            </span>
          )}
        </span>
      </h2>

      <div className="metrics">
        <Metric label="RSoC" value={`${snapshot.rsoc.toFixed(1)} %`} />
        <Metric label="USoC" value={`${snapshot.usoc.toFixed(1)} %`} />
        <Metric
          label="Capacity"
          value={`${snapshot.remaining_capacity_wh.toFixed(0)} Wh`}
        />
        <Metric label="Consumption" value={`${snapshot.consumption_w} W`} />
        <Metric label="Pac" value={`${snapshot.pac_total_w} W`} />
        <Metric
          label="Op Mode"
          value={renderOperatingMode(snapshot.operating_mode)}
        />
      </div>

      {/* All controls — expandable */}
      {!readonly && <div className="controls" onClick={(e) => e.stopPropagation()}>
        <div className="control-group">
          <label className="control-label" htmlFor={`power-${name}`}>Power (W)</label>
          <input
            id={`power-${name}`}
            type="number"
            className="control-input"
            value={commandState.power}
            disabled={controlsDisabled}
            onChange={(e) =>
              setCommandState({
                ...commandState,
                power: Number(e.target.value),
              })
            }
            placeholder="Power"
          />
        </div>

        <div className="control-group">
          <span className="control-label">Discharge</span>
          <div className="control-actions">
            <button
              className="control-button control-button-primary"
              disabled={controlsDisabled}
              onClick={() =>
                onCommand("start_discharge", name, {
                  power: commandState.power,
                })
              }
              title="Start Discharge"
            >
              <span className="control-button-icon"><Icon name="play_arrow" size={16} /></span>
              Start
            </button>
            <button
              className="control-button"
              disabled={controlsDisabled}
              onClick={() => onCommand("stop_discharge", name)}
              title="Stop Discharge"
            >
              <span className="control-button-icon"><Icon name="stop" size={16} /></span>
              Stop
            </button>
          </div>
        </div>

        <div className="control-group">
          <span className="control-label">Charge</span>
          <div className="control-actions">
            <button
              className="control-button control-button-primary"
              disabled={controlsDisabled}
              onClick={() =>
                onCommand("start_charge", name, {
                  power: commandState.power,
                })
              }
              title="Start Charge"
            >
              <span className="control-button-icon"><Icon name="play_arrow" size={16} /></span>
              Start
            </button>
            <button
              className="control-button"
              disabled={controlsDisabled}
              onClick={() => onCommand("stop_charge", name)}
              title="Stop Charge"
            >
              <span className="control-button-icon"><Icon name="stop" size={16} /></span>
              Stop
            </button>
          </div>
        </div>

        {!hasConfigLimits && (
          <div className="control-group">
            <span className="control-label">Limits</span>
            <div className="control-inputs-row">
              <div className="control-input-wrapper">
                <label
                  className="control-input-label"
                  htmlFor={`power-limit-${name}`}
                >
                  Power limit (W)
                </label>
                <input
                  id={`power-limit-${name}`}
                  type="number"
                  className="control-input"
                  value={commandState.powerLimit}
                  disabled={controlsDisabled}
                  onChange={(e) =>
                    setCommandState({
                      ...commandState,
                      powerLimit: Number(e.target.value),
                    })
                  }
                  placeholder="Power limit"
                />
              </div>
              <div className="control-input-wrapper">
                <label
                  className="control-input-label"
                  htmlFor={`soc-limit-${name}`}
                >
                  SoC limit (%)
                </label>
                <input
                  id={`soc-limit-${name}`}
                  type="number"
                  className="control-input"
                  value={commandState.socLimit}
                  disabled={controlsDisabled}
                  onChange={(e) =>
                    setCommandState({
                      ...commandState,
                      socLimit: Number(e.target.value),
                    })
                  }
                  placeholder="SoC limit"
                />
              </div>
            </div>
            <button
              className="control-button control-button-secondary"
              disabled={controlsDisabled}
              onClick={() =>
                onCommand("set_limits", name, {
                  power_limit: commandState.powerLimit,
                  soc_limit: commandState.socLimit,
                })
              }
            >
              Update Limits
            </button>
          </div>
        )}

        <div className="control-group">
          <span className="control-label">Mode</span>
          <div className="control-actions">
            <button
              className="control-button"
              disabled={controlsDisabled}
              onClick={() =>
                onCommand("force_mode", name, { mode: "manual" })
              }
            >
              Force Manual
            </button>
            <button
              className="control-button"
              disabled={controlsDisabled}
              onClick={() =>
                onCommand("force_mode", name, { mode: "auto" })
              }
            >
              Force Auto
            </button>
          </div>
        </div>
      </div>}
    </div>
  );
}

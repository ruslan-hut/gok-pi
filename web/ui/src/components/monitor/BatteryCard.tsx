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
  const [sent, setSent] = useState<string | null>(null);

  const { name } = snapshot;
  const controlsDisabled = !isOnline;

  // Confirm dispatch at the button that caused it. The command is only sent
  // here — the real resulting state arrives back on the flow badge above.
  const send = (command: string, payload?: unknown) => {
    onCommand(command, name, payload);
    setSent(command);
    window.setTimeout(
      () => setSent((current) => (current === command ? null : current)),
      2000,
    );
  };

  const hasConfigLimits =
    batteryConfig &&
    ((batteryConfig.power_limit !== undefined &&
      batteryConfig.power_limit > 0) ||
      (batteryConfig.soc_limit !== undefined && batteryConfig.soc_limit > 0));

  const getStatusBadgeClass = (status: string) => {
    switch (status) {
      case "Connected":
        return "ok";
      case "Disabled":
        return "muted";
      default:
        return "fault";
    }
  };

  const flow = snapshot.battery_discharging
    ? { className: "discharge", label: "Discharging" }
    : snapshot.battery_charging
      ? { className: "charge", label: "Charging" }
      : { className: "idle", label: "Idle" };

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
          <span className={`badge ${flow.className}`}>{flow.label}</span>
          {snapshot.override_source === "charger" && (
            <span
              className="badge badge-with-icon"
              title="An EV charging session is driving this battery; it overrides the schedule until the session ends."
            >
              <Icon name="ev_station" size={14} />EV session
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
          <div className="control-actions">
            <button
              className="control-button control-button-discharge"
              disabled={controlsDisabled}
              onClick={() => send("start_discharge", { power: commandState.power })}
            >
              <span className="control-button-icon"><Icon name="play_arrow" size={16} /></span>
              {sent === "start_discharge" ? "Sent" : "Start discharge"}
            </button>
            <button
              className="control-button"
              disabled={controlsDisabled}
              onClick={() => send("stop_discharge")}
              aria-label="Stop discharge"
            >
              <span className="control-button-icon"><Icon name="stop" size={16} /></span>
              {sent === "stop_discharge" ? "Sent" : "Stop"}
            </button>
          </div>
        </div>

        <div className="control-group">
          <div className="control-actions">
            <button
              className="control-button control-button-charge"
              disabled={controlsDisabled}
              onClick={() => send("start_charge", { power: commandState.power })}
            >
              <span className="control-button-icon"><Icon name="play_arrow" size={16} /></span>
              {sent === "start_charge" ? "Sent" : "Start charge"}
            </button>
            <button
              className="control-button"
              disabled={controlsDisabled}
              onClick={() => send("stop_charge")}
              aria-label="Stop charge"
            >
              <span className="control-button-icon"><Icon name="stop" size={16} /></span>
              {sent === "stop_charge" ? "Sent" : "Stop"}
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
                send("set_limits", {
                  power_limit: commandState.powerLimit,
                  soc_limit: commandState.socLimit,
                })
              }
            >
              {sent === "set_limits" ? "Sent" : "Update limits"}
            </button>
          </div>
        )}

        <div className="control-group">
          <span className="control-label">Operating mode</span>
          <div className="control-actions control-actions-even">
            <button
              className="control-button"
              disabled={controlsDisabled}
              onClick={() => send("force_mode", { mode: "manual" })}
              title="Take the battery off its own automation so schedules and commands from here apply"
            >
              Switch to manual
            </button>
            <button
              className="control-button"
              disabled={controlsDisabled}
              onClick={() => send("force_mode", { mode: "auto" })}
              title="Hand control back to the battery's own automation"
            >
              Switch to auto
            </button>
          </div>
        </div>
      </div>}
    </div>
  );
}

import type { TelemetrySnapshot } from "../../types";

interface SystemSummaryProps {
  batteries: TelemetrySnapshot[];
}

export function SystemSummary({ batteries }: SystemSummaryProps) {
  if (batteries.length === 0) return null;

  const totalCapacity = batteries.reduce(
    (sum, b) => sum + b.remaining_capacity_wh,
    0,
  );
  const avgSoc =
    batteries.reduce((sum, b) => sum + b.rsoc, 0) / batteries.length;
  const totalPac = batteries.reduce((sum, b) => sum + b.pac_total_w, 0);
  const charging = batteries.filter((b) => b.battery_charging).length;
  const discharging = batteries.filter((b) => b.battery_discharging).length;
  const idle = batteries.length - charging - discharging;

  return (
    <div className="system-summary">
      <div className="system-summary-item">
        <span
          className="system-summary-label"
          title="Energy stored across these batteries right now, not their total capacity."
        >
          Remaining
        </span>
        <span className="system-summary-value">
          {totalCapacity.toFixed(0)} Wh
        </span>
      </div>
      <div className="system-summary-item">
        <span className="system-summary-label">Avg SoC</span>
        <span className="system-summary-value">{avgSoc.toFixed(1)}%</span>
      </div>
      <div className="system-summary-item">
        <span
          className="system-summary-label"
          title="These batteries added together. Charging on one offsets discharging on another, so this is the site's net flow."
        >
          Net flow
        </span>
        <span className="system-summary-value">
          {Math.abs(totalPac)} W
          {totalPac !== 0 && (
            <span className="metric-qualifier">
              {" "}
              {totalPac > 0 ? "out" : "in"}
            </span>
          )}
        </span>
      </div>
      <div className="system-summary-item">
        <span className="system-summary-label">Status</span>
        <span className="system-summary-value system-summary-statuses">
          {charging > 0 && (
            <span className="system-summary-status charging">
              {charging} charging
            </span>
          )}
          {discharging > 0 && (
            <span className="system-summary-status discharging">
              {discharging} discharging
            </span>
          )}
          {idle > 0 && (
            <span className="system-summary-status idle">
              {idle} idle
            </span>
          )}
        </span>
      </div>
    </div>
  );
}

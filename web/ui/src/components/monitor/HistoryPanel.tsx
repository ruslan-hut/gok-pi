import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { fetchHistory } from "../../api";
import { fmtDuration } from "../../lib/format";
import { HistoryChart } from "./HistoryChart";
import type { TelemetryPoint } from "../../types";

/**
 * HistoryPanel sits under the live battery cards: the same subject, one step back
 * in time. "What is it doing" and "what has it been doing" are the same question
 * at two resolutions, so they belong on one screen rather than behind a tab
 * switch.
 */

const RANGES = [
  { hours: 6, label: "6h" },
  { hours: 24, label: "24h" },
  { hours: 72, label: "3d" },
  { hours: 168, label: "7d" },
] as const;

const REFRESH_MS = 60 * 1000; // one bucket

/**
 * minSpanForVerdictMin is how much recorded history is needed before the panel
 * will call a window complete or incomplete. Half an hour is enough that a single
 * partial minute cannot swing the verdict.
 */
const minSpanForVerdictMin = 30;

interface HistoryPanelProps {
  agentId: string;
  batteryNames: string[];
}

export function HistoryPanel({ agentId, batteryNames }: HistoryPanelProps) {
  const [hours, setHours] = useState<number>(24);
  const [battery, setBattery] = useState<string>(batteryNames[0] ?? "");
  const [points, setPoints] = useState<TelemetryPoint[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>();
  // The window is pinned per load so the chart's x-axis does not drift under the
  // cursor between refreshes.
  const [bounds, setBounds] = useState(() => ({ from: Date.now() - 24 * 3600_000, to: Date.now() }));
  const loadedOnce = useRef(false);

  // A battery that disappears from config (renamed, removed) must not leave the
  // panel querying a name the server no longer knows.
  useEffect(() => {
    if (!batteryNames.length) return;
    if (!batteryNames.includes(battery)) setBattery(batteryNames[0]);
  }, [batteryNames, battery]);

  const load = useCallback(async () => {
    if (!agentId || !battery) return;
    setLoading(true);
    try {
      const res = await fetchHistory({ agentId, battery, hours });
      setPoints(res.points);
      const to = Date.now();
      setBounds({ from: to - hours * 3600_000, to });
      setError(undefined);
      loadedOnce.current = true;
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load history");
    } finally {
      setLoading(false);
    }
  }, [agentId, battery, hours]);

  useEffect(() => {
    load();
    const timer = setInterval(load, REFRESH_MS);
    return () => clearInterval(timer);
  }, [load]);

  const stats = useMemo(() => summarize(points), [points]);

  if (!agentId || !batteryNames.length) return null;

  return (
    <section className="card history-panel">
      <div className="history-panel-header">
        <div className="history-panel-title">
          <h2>History</h2>
          {stats && <HistorySubtitle stats={stats} />}
        </div>

        <div className="history-panel-controls">
          {batteryNames.length > 1 && (
            <select
              className="history-select"
              value={battery}
              onChange={(e) => setBattery(e.target.value)}
              aria-label="Battery"
            >
              {batteryNames.map((name) => (
                <option key={name} value={name}>
                  {name}
                </option>
              ))}
            </select>
          )}
          <div className="history-range" role="group" aria-label="Time range">
            {RANGES.map((range) => (
              <button
                key={range.hours}
                type="button"
                className={`history-range-button${hours === range.hours ? " active" : ""}`}
                aria-pressed={hours === range.hours}
                onClick={() => setHours(range.hours)}
              >
                {range.label}
              </button>
            ))}
          </div>
        </div>
      </div>

      {error && <div className="config-error">{error}</div>}

      {!error && !points.length && loadedOnce.current && !loading && (
        <p className="history-empty">
          No telemetry recorded for this window yet. History starts accumulating from the
          moment the control server was updated.
        </p>
      )}

      {!error && !loadedOnce.current && loading && (
        <div className="config-loading">
          <div className="spinner" />
          <span>Loading history…</span>
        </div>
      )}

      {points.length > 0 && (
        <HistoryChart points={points} from={bounds.from} to={bounds.to} />
      )}
    </section>
  );
}

/**
 * HistorySubtitle says how trustworthy the window below it is.
 *
 * Over a short span the completeness figure is not wrong so much as unfounded:
 * one partial minute out of three is "33% recorded", and a freshly deployed
 * server would announce a fault it has no evidence for. Below the threshold the
 * line states only what is certain — how much history exists — and makes no claim
 * about loss in either direction.
 */
function HistorySubtitle({ stats }: { stats: HistoryStats }) {
  if (stats.spanMinutes < minSpanForVerdictMin) {
    return (
      <span className="history-panel-subtitle">
        {fmtDuration(stats.spanMinutes * 60)} of telemetry recorded so far
      </span>
    );
  }

  return (
    <span
      className={`history-panel-subtitle${stats.missingMinutes > 0 ? " warn" : ""}`}
    >
      {stats.missingMinutes > 0
        ? `${fmtDuration(stats.missingMinutes * 60)} of telemetry missing · ${stats.deliveredPct.toFixed(0)}% recorded`
        : "Telemetry complete — no gaps"}
    </span>
  );
}

interface HistoryStats {
  /** Minutes between the first and last recorded bucket, gaps included. */
  spanMinutes: number;
  deliveredPct: number;
  missingMinutes: number;
}

/**
 * summarize reports how much telemetry actually landed, measured between the
 * first and last recorded minute rather than across the whole selected window —
 * a 7-day window on a server that has only been recording for a day would
 * otherwise report the missing history as loss.
 *
 * Six frames is a complete minute at the agent's 10s poll interval, so a minute
 * with fewer counts as partially missing.
 */
function summarize(points: TelemetryPoint[]): HistoryStats | null {
  if (!points.length) return null;

  const first = new Date(points[0].bucket).getTime();
  const last = new Date(points[points.length - 1].bucket).getTime();
  if (Number.isNaN(first) || Number.isNaN(last) || last < first) return null;

  const expectedMinutes = Math.round((last - first) / 60_000) + 1;
  const expected = expectedMinutes * 6;
  const received = points.reduce((sum, p) => sum + Math.min(p.samples, 6), 0);
  return {
    spanMinutes: expectedMinutes,
    deliveredPct: Math.min((received / expected) * 100, 100),
    missingMinutes: Math.round((expected - received) / 6),
  };
}

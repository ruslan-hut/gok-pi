import { useCallback, useEffect, useRef, useState } from "react";
import { fetchPrices, fetchSessions } from "../../api";
import type { BatterySummary, DayData, PricesState, ScheduleWindow, SessionRecord, SessionsResponse } from "../../types";

const POLL_INTERVAL_MS = 5 * 60 * 1000; // 5 minutes

export default function PricesDashboard() {
  const [state, setState] = useState<PricesState | null>(null);
  const [sessData, setSessData] = useState<SessionsResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>();
  const [sessError, setSessError] = useState<string>();
  const intervalRef = useRef<ReturnType<typeof setInterval>>();

  const load = useCallback(async () => {
    setLoading(true);

    // Fetch prices and sessions independently
    const pricesPromise = fetchPrices()
      .then((data) => {
        setState(data);
        setError(undefined);
      })
      .catch((err) => {
        setError(err instanceof Error ? err.message : "Failed to fetch prices");
      });

    const sessionsPromise = fetchSessions()
      .then((data) => {
        setSessData(data);
        setSessError(undefined);
      })
      .catch((err) => {
        setSessError(err instanceof Error ? err.message : "Failed to fetch sessions");
      });

    await Promise.all([pricesPromise, sessionsPromise]);
    setLoading(false);
  }, []);

  useEffect(() => {
    load();
    intervalRef.current = setInterval(load, POLL_INTERVAL_MS);
    return () => {
      if (intervalRef.current) clearInterval(intervalRef.current);
    };
  }, [load]);

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      {loading && !state && !sessData && (
        <div className="config-loading">
          <div className="spinner" />
          <span>Loading prices...</span>
        </div>
      )}
      {error && <div className="config-error">{error}</div>}
      {state?.last_error && (
        <div className="config-error">API error: {state.last_error}</div>
      )}
      {state && (
        <>
          <div className="prices-meta">
            <span>
              Last updated:{" "}
              {new Date(state.last_updated).toLocaleString()}
            </span>
            <span>
              Next update:{" "}
              {new Date(state.next_update).toLocaleString()}
            </span>
            {loading && <span className="spinner-small" />}
          </div>
          {state.today && <DayPanel label="Today" data={state.today} />}
          {state.tomorrow && (
            <DayPanel label="Tomorrow" data={state.tomorrow} />
          )}
          {!state.today && !state.tomorrow && (
            <div className="config-empty">
              No price data available yet.
            </div>
          )}
        </>
      )}
      {sessError && <div className="config-error">{sessError}</div>}
      {sessData && (
        <SessionsPanel
          summaries={sessData.summaries}
          sessions={sessData.sessions}
        />
      )}
    </div>
  );
}

function DayPanel({ label, data }: { label: string; data: DayData }) {
  const chargeHours = new Set<number>();
  const dischargeHours = new Set<number>();

  for (const w of data.schedule.charge_windows ?? []) {
    for (let h = w.start_hour; h < w.end_hour; h++) chargeHours.add(h);
  }
  for (const w of data.schedule.discharge_windows ?? []) {
    for (let h = w.start_hour; h < w.end_hour; h++) dischargeHours.add(h);
  }

  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        gap: "0.75rem",
      }}
    >
      <h4>
        {label} — {data.date}
      </h4>
      <div className="prices-stats">
        <span>
          Min: <strong>{data.stats.min_price_eur_mwh.toFixed(1)}</strong>
        </span>
        <span>
          Avg: <strong>{data.stats.avg_price_eur_mwh.toFixed(1)}</strong>
        </span>
        <span>
          Max: <strong>{data.stats.max_price_eur_mwh.toFixed(1)}</strong>
        </span>
        <span className="prices-stats-unit">EUR/MWh</span>
      </div>
      <PriceChart
        prices={data.prices}
        chargeHours={chargeHours}
        dischargeHours={dischargeHours}
        stats={data.stats}
      />
      <ScheduleTable
        chargeWindows={data.schedule.charge_windows}
        dischargeWindows={data.schedule.discharge_windows}
      />
    </div>
  );
}

function PriceChart({
  prices,
  chargeHours,
  dischargeHours,
  stats,
}: {
  prices: DayData["prices"];
  chargeHours: Set<number>;
  dischargeHours: Set<number>;
  stats: DayData["stats"];
}) {
  const width = 720;
  const height = 200;
  const padding = { top: 20, right: 12, bottom: 28, left: 48 };
  const chartW = width - padding.left - padding.right;
  const chartH = height - padding.top - padding.bottom;

  const maxPrice = Math.max(stats.max_price_eur_mwh * 1.1, 1);
  const minPrice = Math.min(0, stats.min_price_eur_mwh);
  const range = maxPrice - minPrice;
  const barW = chartW / 24 - 2;

  const yScale = (v: number) =>
    padding.top + chartH - ((v - minPrice) / range) * chartH;

  const zeroY = yScale(0);

  // Grid lines
  const gridLines: number[] = [];
  const step = niceStep(range, 5);
  for (let v = Math.ceil(minPrice / step) * step; v <= maxPrice; v += step) {
    gridLines.push(v);
  }

  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      className="price-chart-svg"
      role="img"
      aria-label="Hourly electricity prices"
    >
      {/* Grid */}
      {gridLines.map((v) => (
        <g key={v}>
          <line
            x1={padding.left}
            x2={width - padding.right}
            y1={yScale(v)}
            y2={yScale(v)}
            style={{ stroke: "var(--border-color-subtle)" }}
            strokeDasharray="2,3"
          />
          <text
            x={padding.left - 6}
            y={yScale(v) + 3}
            textAnchor="end"
            style={{ fill: "var(--color-text-muted)" }}
            fontSize="10"
          >
            {v.toFixed(0)}
          </text>
        </g>
      ))}

      {/* Average line */}
      <line
        x1={padding.left}
        x2={width - padding.right}
        y1={yScale(stats.avg_price_eur_mwh)}
        y2={yScale(stats.avg_price_eur_mwh)}
        style={{ stroke: "var(--color-warning)" }}
        strokeDasharray="4,3"
        strokeWidth="1"
        opacity="0.6"
      />

      {/* Bars */}
      {prices.map((p) => {
        const x = padding.left + (p.hour / 24) * chartW + 1;
        const barTop = yScale(Math.max(p.price_eur_mwh, 0));
        const barBottom =
          p.price_eur_mwh >= 0 ? zeroY : yScale(p.price_eur_mwh);
        const barHeight = Math.abs(barBottom - barTop);
        const y = Math.min(barTop, barBottom);

        let fill = "var(--color-text-dim)";
        if (chargeHours.has(p.hour)) fill = "var(--color-success)";
        if (dischargeHours.has(p.hour)) fill = "var(--color-danger)";

        return (
          <g key={p.hour}>
            <rect
              x={x}
              y={y}
              width={barW}
              height={Math.max(barHeight, 1)}
              style={{ fill }}
              rx="2"
            />
            <title>
              {`${String(p.hour).padStart(2, "0")}:00 — ${p.price_eur_mwh.toFixed(1)} EUR/MWh`}
            </title>
          </g>
        );
      })}

      {/* X axis labels */}
      {[0, 3, 6, 9, 12, 15, 18, 21].map((h) => (
        <text
          key={h}
          x={padding.left + (h / 24) * chartW + barW / 2}
          y={height - 6}
          textAnchor="middle"
          style={{ fill: "var(--color-text-muted)" }}
          fontSize="10"
        >
          {String(h).padStart(2, "0")}
        </text>
      ))}

      {/* Legend */}
      <rect
        x={width - 180}
        y={4}
        width={10}
        height={10}
        style={{ fill: "var(--color-success)" }}
        rx="2"
      />
      <text x={width - 166} y={13} style={{ fill: "var(--color-text-muted)" }} fontSize="10">
        Cheapest 3
      </text>
      <rect
        x={width - 115}
        y={4}
        width={10}
        height={10}
        style={{ fill: "var(--color-danger)" }}
        rx="2"
      />
      <text x={width - 101} y={13} style={{ fill: "var(--color-text-muted)" }} fontSize="10">
        Priciest 3
      </text>
      <line
        x1={width - 46}
        x2={width - 32}
        y1={9}
        y2={9}
        style={{ stroke: "var(--color-warning)" }}
        strokeDasharray="4,3"
        opacity="0.6"
      />
      <text x={width - 28} y={13} style={{ fill: "var(--color-text-muted)" }} fontSize="10">
        Avg
      </text>
    </svg>
  );
}

function ScheduleTable({
  chargeWindows,
  dischargeWindows,
}: {
  chargeWindows: ScheduleWindow[] | null;
  dischargeWindows: ScheduleWindow[] | null;
}) {
  const charge = chargeWindows ?? [];
  const discharge = dischargeWindows ?? [];

  if (charge.length === 0 && discharge.length === 0) {
    return <div className="config-empty">No schedule computed.</div>;
  }

  return (
    <div className="schedule-table">
      <table>
        <thead>
          <tr>
            <th>Type</th>
            <th>Window</th>
            <th>Avg Price</th>
          </tr>
        </thead>
        <tbody>
          {charge.map((w, i) => (
            <tr key={`c-${i}`} className="schedule-row-charge">
              <td>
                <span className="schedule-badge schedule-badge-charge">
                  CHEAPEST
                </span>
              </td>
              <td>
                {fmt2(w.start_hour)}:00 — {fmt2(w.end_hour)}:00
              </td>
              <td>{w.avg_price_eur_mwh.toFixed(1)} EUR/MWh</td>
            </tr>
          ))}
          {discharge.map((w, i) => (
            <tr key={`d-${i}`} className="schedule-row-discharge">
              <td>
                <span className="schedule-badge schedule-badge-discharge">
                  PRICIEST
                </span>
              </td>
              <td>
                {fmt2(w.start_hour)}:00 — {fmt2(w.end_hour)}:00
              </td>
              <td>{w.avg_price_eur_mwh.toFixed(1)} EUR/MWh</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function SessionsPanel({
  summaries,
  sessions,
}: {
  summaries: BatterySummary[];
  sessions: SessionRecord[];
}) {
  const hasSummaries = summaries.length > 0;
  const hasSessions = sessions.length > 0;

  if (!hasSummaries && !hasSessions) {
    return (
      <div style={{ display: "flex", flexDirection: "column", gap: "0.5rem" }}>
        <h4>Battery Sessions (48h)</h4>
        <div className="config-empty">No sessions recorded yet.</div>
      </div>
    );
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "0.75rem" }}>
      <h4>Battery Sessions (48h)</h4>

      {hasSummaries && (
        <div className="schedule-table">
          <table>
            <thead>
              <tr>
                <th>Battery</th>
                <th>Charged</th>
                <th>Charge Cost</th>
                <th>Discharged</th>
                <th>Discharge Value</th>
              </tr>
            </thead>
            <tbody>
              {summaries.map((s) => (
                <tr key={s.battery_name}>
                  <td>
                    {s.battery_name}
                    {s.active_charge && <span className="session-live-dot" />}
                    {s.active_discharge && <span className="session-live-dot" />}
                  </td>
                  <td>{fmtEnergy(s.charge_energy_wh)}</td>
                  <td>{fmtCost(s.charge_cost_eur)}</td>
                  <td>{fmtEnergy(s.discharge_energy_wh)}</td>
                  <td>{fmtCost(s.discharge_cost_eur)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {hasSessions && (
        <div className="schedule-table">
          <table>
            <thead>
              <tr>
                <th>Battery</th>
                <th>Type</th>
                <th>Started</th>
                <th>Duration</th>
                <th>Energy</th>
                <th>Avg Power</th>
                <th>SoC</th>
                <th>Cost</th>
              </tr>
            </thead>
            <tbody>
              {sessions.map((s) => (
                <tr key={s.id} className={!s.ended_at ? "session-active" : ""}>
                  <td>
                    {s.battery_name}
                    {!s.ended_at && <span className="session-live-dot" />}
                  </td>
                  <td>
                    <span
                      className={`schedule-badge ${s.type === "charge" ? "schedule-badge-charge" : "schedule-badge-discharge"}`}
                    >
                      {s.type.toUpperCase()}
                    </span>
                  </td>
                  <td>{fmtTime(s.started_at)}</td>
                  <td>{fmtDuration(s.duration_seconds)}</td>
                  <td>{fmtEnergy(s.energy_wh)}</td>
                  <td>{s.avg_power_w > 0 ? `${s.avg_power_w.toFixed(0)} W` : "—"}</td>
                  <td>
                    {s.soc_start > 0 || s.soc_end > 0
                      ? `${s.soc_start.toFixed(0)}% → ${s.soc_end.toFixed(0)}%`
                      : "—"}
                  </td>
                  <td>{fmtCost(s.cost_eur)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function fmtEnergy(wh: number): string {
  if (wh <= 0) return "—";
  if (wh >= 1000) return `${(wh / 1000).toFixed(2)} kWh`;
  return `${wh.toFixed(0)} Wh`;
}

function fmtCost(eur: number): string {
  if (eur <= 0) return "—";
  return `${eur.toFixed(4)} EUR`;
}

function fmtTime(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function fmtDuration(seconds?: number): string {
  if (!seconds || seconds <= 0) return "—";
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

function fmt2(n: number): string {
  return String(n).padStart(2, "0");
}

function niceStep(range: number, targetTicks: number): number {
  const rough = range / targetTicks;
  const mag = Math.pow(10, Math.floor(Math.log10(rough)));
  const norm = rough / mag;
  let step: number;
  if (norm <= 1.5) step = 1;
  else if (norm <= 3) step = 2;
  else if (norm <= 7) step = 5;
  else step = 10;
  return step * mag;
}

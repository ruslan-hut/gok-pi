import { useCallback, useEffect, useRef, useState } from "react";
import { fetchPrices } from "./api";
import type { DayData, PricesState, ScheduleWindow } from "./types";

const POLL_INTERVAL_MS = 5 * 60 * 1000; // 5 minutes

export default function PricesDashboard() {
  const [open, setOpen] = useState(false);
  const [state, setState] = useState<PricesState | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>();
  const intervalRef = useRef<ReturnType<typeof setInterval>>();

  const load = useCallback(async () => {
    try {
      setLoading(true);
      setError(undefined);
      const data = await fetchPrices();
      setState(data);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to fetch prices");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!open) return;
    load();
    intervalRef.current = setInterval(load, POLL_INTERVAL_MS);
    return () => {
      if (intervalRef.current) clearInterval(intervalRef.current);
    };
  }, [open, load]);

  return (
    <div className="config-panel">
      <div
        className="config-panel-header"
        style={{ cursor: "pointer" }}
        onClick={() => setOpen(!open)}
      >
        <h3>Electricity Prices</h3>
        <span className="config-toggle">{open ? "collapse" : "expand"}</span>
      </div>
      {open && (
        <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
          {loading && !state && (
            <div className="config-loading">
              <div className="spinner" />
              <span>Loading prices...</span>
            </div>
          )}
          {error && <div className="config-error">{error}</div>}
          {state?.last_error && (
            <div className="config-error">
              API error: {state.last_error}
            </div>
          )}
          {state && (
            <>
              <div className="prices-meta">
                <span>
                  Last updated: {new Date(state.last_updated).toLocaleString()}
                </span>
                <span>
                  Next update: {new Date(state.next_update).toLocaleString()}
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
        </div>
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
    <div style={{ display: "flex", flexDirection: "column", gap: "0.75rem" }}>
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
            stroke="rgba(148,163,184,0.15)"
            strokeDasharray="2,3"
          />
          <text
            x={padding.left - 6}
            y={yScale(v) + 3}
            textAnchor="end"
            fill="#94a3b8"
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
        stroke="#fbbf24"
        strokeDasharray="4,3"
        strokeWidth="1"
        opacity="0.6"
      />

      {/* Bars */}
      {prices.map((p) => {
        const x = padding.left + (p.hour / 24) * chartW + 1;
        const barTop = yScale(Math.max(p.price_eur_mwh, 0));
        const barBottom = p.price_eur_mwh >= 0 ? zeroY : yScale(p.price_eur_mwh);
        const barHeight = Math.abs(barBottom - barTop);
        const y = Math.min(barTop, barBottom);

        let fill = "rgba(148,163,184,0.5)";
        if (chargeHours.has(p.hour)) fill = "#4ade80";
        if (dischargeHours.has(p.hour)) fill = "#f87171";

        return (
          <g key={p.hour}>
            <rect
              x={x}
              y={y}
              width={barW}
              height={Math.max(barHeight, 1)}
              fill={fill}
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
          fill="#94a3b8"
          fontSize="10"
        >
          {String(h).padStart(2, "0")}
        </text>
      ))}

      {/* Legend */}
      <rect x={width - 180} y={4} width={10} height={10} fill="#4ade80" rx="2" />
      <text x={width - 166} y={13} fill="#94a3b8" fontSize="10">
        Charge
      </text>
      <rect x={width - 115} y={4} width={10} height={10} fill="#f87171" rx="2" />
      <text x={width - 101} y={13} fill="#94a3b8" fontSize="10">
        Discharge
      </text>
      <line
        x1={width - 46}
        x2={width - 32}
        y1={9}
        y2={9}
        stroke="#fbbf24"
        strokeDasharray="4,3"
        opacity="0.6"
      />
      <text x={width - 28} y={13} fill="#94a3b8" fontSize="10">
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
                  CHARGE
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
                  DISCHARGE
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

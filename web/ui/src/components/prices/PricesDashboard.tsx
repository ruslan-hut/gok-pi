import { useCallback, useEffect, useRef, useState } from "react";
import { fetchPrices, fetchSessions } from "../../api";
import type { BatterySummary, DayData, PricesState, ScheduleWindow, SessionsResponse } from "../../types";

const POLL_INTERVAL_MS = 5 * 60 * 1000; // 5 minutes
const ACTIVE_POLL_INTERVAL_MS = 30 * 1000; // 30 seconds when sessions are active

function dayBounds(daysAgo: number): { since: string; until: string } {
  const d = new Date();
  d.setHours(0, 0, 0, 0);
  d.setDate(d.getDate() - daysAgo);
  const since = d.toISOString();
  const next = new Date(d);
  next.setDate(next.getDate() + 1);
  const until = next.toISOString();
  return { since, until };
}

export default function PricesDashboard() {
  const [state, setState] = useState<PricesState | null>(null);
  const [todaySess, setTodaySess] = useState<SessionsResponse | null>(null);
  const [yesterdaySess, setYesterdaySess] = useState<SessionsResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>();
  const [sessError, setSessError] = useState<string>();
  const intervalRef = useRef<ReturnType<typeof setInterval>>();
  const hasActiveSessions = todaySess?.summaries?.some(
    (s) => s.active_charge || s.active_discharge,
  ) ?? false;

  const load = useCallback(async () => {
    setLoading(true);

    const pricesPromise = fetchPrices()
      .then((data) => {
        setState(data);
        setError(undefined);
      })
      .catch((err) => {
        setError(err instanceof Error ? err.message : "Failed to fetch prices");
      });

    const today = dayBounds(0);
    const yesterday = dayBounds(1);

    const todayPromise = fetchSessions({ since: today.since })
      .then((data) => {
        setTodaySess(data);
        setSessError(undefined);
      })
      .catch((err) => {
        setSessError(err instanceof Error ? err.message : "Failed to fetch sessions");
      });

    const yesterdayPromise = fetchSessions({ since: yesterday.since, until: yesterday.until })
      .then((data) => {
        setYesterdaySess(data);
      })
      .catch(() => {
        // yesterday error is not critical
      });

    await Promise.all([pricesPromise, todayPromise, yesterdayPromise]);
    setLoading(false);
  }, []);

  useEffect(() => {
    load();
    const ms = hasActiveSessions ? ACTIVE_POLL_INTERVAL_MS : POLL_INTERVAL_MS;
    intervalRef.current = setInterval(load, ms);
    return () => {
      if (intervalRef.current) clearInterval(intervalRef.current);
    };
  }, [load, hasActiveSessions]);

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
      {loading && !state && !todaySess && (
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
      {todaySess && (
        <SessionsPanel label="Today" summaries={todaySess.summaries} />
      )}
      {yesterdaySess && yesterdaySess.summaries.length > 0 && (
        <SessionsPanel label="Yesterday" summaries={yesterdaySess.summaries} />
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
          P{data.stats.charge_percentile}: <strong>{data.stats.low_eur_mwh.toFixed(1)}</strong>
        </span>
        <span>
          Avg: <strong>{data.stats.avg_price_eur_mwh.toFixed(1)}</strong>
        </span>
        <span>
          P{data.stats.discharge_percentile}: <strong>{data.stats.high_eur_mwh.toFixed(1)}</strong>
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
        stats={data.stats}
        isToday={label === "Today"}
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

      {/* Low threshold line — charge below this */}
      <line
        x1={padding.left}
        x2={width - padding.right}
        y1={yScale(stats.low_eur_mwh)}
        y2={yScale(stats.low_eur_mwh)}
        style={{ stroke: "var(--color-success)" }}
        strokeDasharray="4,3"
        strokeWidth="1"
        opacity="0.5"
      />

      {/* High threshold line — discharge above this */}
      <line
        x1={padding.left}
        x2={width - padding.right}
        y1={yScale(stats.high_eur_mwh)}
        y2={yScale(stats.high_eur_mwh)}
        style={{ stroke: "var(--color-danger)" }}
        strokeDasharray="4,3"
        strokeWidth="1"
        opacity="0.5"
      />

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
        x={width - 170}
        y={4}
        width={10}
        height={10}
        style={{ fill: "var(--color-success)" }}
        rx="2"
      />
      <text x={width - 156} y={13} style={{ fill: "var(--color-text-muted)" }} fontSize="10">
        ≤ P{stats.charge_percentile}
      </text>
      <rect
        x={width - 120}
        y={4}
        width={10}
        height={10}
        style={{ fill: "var(--color-danger)" }}
        rx="2"
      />
      <text x={width - 106} y={13} style={{ fill: "var(--color-text-muted)" }} fontSize="10">
        ≥ P{stats.discharge_percentile}
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
  stats,
  isToday,
}: {
  chargeWindows: ScheduleWindow[] | null;
  dischargeWindows: ScheduleWindow[] | null;
  stats: DayData["stats"];
  isToday: boolean;
}) {
  const charge = chargeWindows ?? [];
  const discharge = dischargeWindows ?? [];

  if (charge.length === 0 && discharge.length === 0) {
    return <div className="config-empty">No schedule computed.</div>;
  }

  const all = [
    ...charge.map((w, i) => ({ key: `c-${i}`, type: "charge" as const, w })),
    ...discharge.map((w, i) => ({ key: `d-${i}`, type: "discharge" as const, w })),
  ];

  // Determine active/next window
  const nowHour = new Date().getHours();
  let activeIdx = -1;
  let nextIdx = -1;

  if (isToday) {
    for (let i = 0; i < all.length; i++) {
      const { w } = all[i];
      if (nowHour >= w.start_hour && nowHour < w.end_hour) {
        activeIdx = i;
        break;
      }
    }
    if (activeIdx === -1) {
      for (let i = 0; i < all.length; i++) {
        if (all[i].w.start_hour > nowHour) {
          nextIdx = i;
          break;
        }
      }
    }
  } else {
    // Tomorrow: first window is "next"
    nextIdx = 0;
  }

  const chargeLabel = `CHARGE ≤P${stats.charge_percentile}`;
  const dischargeLabel = `DISCHARGE ≥P${stats.discharge_percentile}`;

  return (
    <>
      {/* Desktop table */}
      <div className="schedule-table hide-mobile">
        <table>
          <thead>
            <tr>
              <th>Type</th>
              <th>Window</th>
              <th>Avg Price</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {all.map(({ key, type, w }, i) => (
              <tr key={key}>
                <td>
                  <span className={`schedule-badge ${type === "charge" ? "schedule-badge-charge" : "schedule-badge-discharge"}`}>
                    {type === "charge" ? chargeLabel : dischargeLabel}
                  </span>
                </td>
                <td>
                  {fmt2(w.start_hour)}:00 — {fmt2(w.end_hour)}:00
                </td>
                <td>{w.avg_price_eur_mwh.toFixed(1)} EUR/MWh</td>
                <td>
                  {i === activeIdx && <span className="schedule-status-badge schedule-status-active">ACTIVE</span>}
                  {i === nextIdx && <span className="schedule-status-badge schedule-status-next">NEXT</span>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/* Mobile cards */}
      <div className="card-list show-mobile">
        {all.map(({ key, type, w }, i) => (
          <div key={key} className="data-card">
            <div className="data-card-header">
              <span className={`schedule-badge ${type === "charge" ? "schedule-badge-charge" : "schedule-badge-discharge"}`}>
                {type === "charge" ? chargeLabel : dischargeLabel}
              </span>
              {i === activeIdx && <span className="schedule-status-badge schedule-status-active">ACTIVE</span>}
              {i === nextIdx && <span className="schedule-status-badge schedule-status-next">NEXT</span>}
            </div>
            <div className="data-card-row">
              <span className="data-card-label">Window</span>
              <span className="data-card-value">{fmt2(w.start_hour)}:00 — {fmt2(w.end_hour)}:00</span>
            </div>
            <div className="data-card-row">
              <span className="data-card-label">Avg Price</span>
              <span className="data-card-value">{w.avg_price_eur_mwh.toFixed(1)} EUR/MWh</span>
            </div>
          </div>
        ))}
      </div>
    </>
  );
}

function SessionsPanel({
  label,
  summaries,
}: {
  label: string;
  summaries: BatterySummary[];
}) {
  if (summaries.length === 0) {
    return (
      <div style={{ display: "flex", flexDirection: "column", gap: "0.5rem" }}>
        <h4>Sessions — {label}</h4>
        <div className="config-empty">No sessions recorded.</div>
      </div>
    );
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "0.75rem" }}>
      <h4>Sessions — {label}</h4>

      <div className="schedule-table hide-mobile">
        <table>
          <thead>
            <tr>
              <th>Battery</th>
              <th>Charged</th>
              <th>Charge Cost</th>
              <th>Discharged</th>
              <th>Discharge Value</th>
              <th>Net</th>
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
                <td><SignedCost eur={s.net_cost_eur} /></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="card-list show-mobile">
        {summaries.map((s) => (
          <div key={s.battery_name} className="data-card">
            <div className="data-card-header">
              <span className="data-card-title">
                {s.battery_name}
                {s.active_charge && <span className="session-live-dot" />}
                {s.active_discharge && <span className="session-live-dot" />}
              </span>
            </div>
            <div className="data-card-row">
              <span className="data-card-label">Charged</span>
              <span className="data-card-value">{fmtEnergy(s.charge_energy_wh)}</span>
            </div>
            <div className="data-card-row">
              <span className="data-card-label">Charge Cost</span>
              <span className="data-card-value">{fmtCost(s.charge_cost_eur)}</span>
            </div>
            <div className="data-card-row">
              <span className="data-card-label">Discharged</span>
              <span className="data-card-value">{fmtEnergy(s.discharge_energy_wh)}</span>
            </div>
            <div className="data-card-row">
              <span className="data-card-label">Discharge Value</span>
              <span className="data-card-value">{fmtCost(s.discharge_cost_eur)}</span>
            </div>
            <div className="data-card-row">
              <span className="data-card-label">Net</span>
              <span className="data-card-value"><SignedCost eur={s.net_cost_eur} /></span>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

function fmtEnergy(wh: number): string {
  if (wh <= 0) return "—";
  if (wh >= 1000) return `${(wh / 1000).toFixed(2)} kWh`;
  return `${wh.toFixed(0)} Wh`;
}

function fmtCost(eur: number): string {
  if (eur === 0) return "—";
  return `${Math.abs(eur).toFixed(4)} EUR`;
}

function SignedCost({ eur }: { eur: number }) {
  if (eur === 0) return <>{"—"}</>;
  const sign = eur > 0 ? "+" : "-";
  const color = eur > 0 ? "var(--color-success)" : "var(--color-danger)";
  return <span style={{ color }}>{sign}{Math.abs(eur).toFixed(4)} EUR</span>;
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

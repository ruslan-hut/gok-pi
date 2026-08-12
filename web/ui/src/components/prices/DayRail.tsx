import { useState } from "react";
import type { DayData, PriceLimits, SessionRecord } from "../../types";

/**
 * DayRail draws one 24-hour axis shared by every lane: the hourly price, the
 * windows the scheduler picked, and the sessions that actually ran. Reading
 * down a column answers "did it charge in the cheap hours?" without
 * cross-referencing three tables.
 */

const WIDTH = 1000;
// The right gutter must fit the whole price-limit label inside the viewBox:
// the scroll container clips anything drawn past it.
const PAD = { top: 18, right: 92, bottom: 22, left: 64 };
const PRICE_H = 150;
const LANE_H = 16;
const LANE_GAP = 5;
const LANE_LABEL_GAP = 10;

interface DayRailProps {
  data: DayData;
  limits: PriceLimits;
  sessions: SessionRecord[];
  /** Only today gets a "now" marker; tomorrow has no position in time yet. */
  isToday: boolean;
}

interface Lane {
  key: string;
  label: string;
  blocks: Block[];
}

interface Block {
  key: string;
  from: number;
  to: number;
  kind: "charge" | "discharge";
  open?: boolean;
  title: string;
}

export function DayRail({ data, limits, sessions, isToday }: DayRailProps) {
  const [hoverHour, setHoverHour] = useState<number | null>(null);

  const chartW = WIDTH - PAD.left - PAD.right;
  const hourX = (h: number) => PAD.left + (h / 24) * chartW;

  const maxPrice = Math.max(data.stats.max_price_eur_mwh * 1.1, 1);
  const minPrice = Math.min(0, data.stats.min_price_eur_mwh);
  const range = maxPrice - minPrice || 1;
  const priceY = (v: number) =>
    PAD.top + PRICE_H - ((v - minPrice) / range) * PRICE_H;

  const planLane = buildPlanLane(data);
  const sessionLanes = buildSessionLanes(sessions);
  const lanes = [planLane, ...sessionLanes].filter((l) => l.blocks.length > 0);

  const lanesTop = PAD.top + PRICE_H + LANE_LABEL_GAP;
  const laneY = (i: number) => lanesTop + i * (LANE_H + LANE_GAP);
  const lanesBottom = lanes.length
    ? laneY(lanes.length - 1) + LANE_H
    : lanesTop;
  const height = lanesBottom + PAD.bottom;

  const chargeHours = hourSet(data.schedule.charge_windows);
  const dischargeHours = hourSet(data.schedule.discharge_windows);

  const now = new Date();
  const nowFraction = now.getHours() + now.getMinutes() / 60;
  const nowX = hourX(nowFraction);

  const gridValues = gridLines(minPrice, maxPrice, range);
  const barW = chartW / 24;
  const hovered = hoverHour === null ? null : data.prices[hoverHour];

  return (
    <div className="day-rail">
      <div className="day-rail-scroll">
      <svg
        viewBox={`0 0 ${WIDTH} ${height}`}
        className="day-rail-svg"
        role="img"
        aria-label={`Hourly electricity price for ${data.date} with scheduled and actual battery sessions`}
        onMouseLeave={() => setHoverHour(null)}
      >
        {gridValues.map((v) => (
          <g key={v}>
            <line
              x1={PAD.left}
              x2={WIDTH - PAD.right}
              y1={priceY(v)}
              y2={priceY(v)}
              className="day-rail-grid"
            />
            <text
              x={PAD.left - 7}
              y={priceY(v) + 3}
              textAnchor="end"
              className="day-rail-axis-label"
            >
              {v.toFixed(0)}
            </text>
          </g>
        ))}

        <text
          x={PAD.left - 7}
          y={PAD.top - 6}
          textAnchor="end"
          className="day-rail-axis-unit"
        >
          EUR/MWh
        </text>

        {/* Hovered column, drawn behind everything so it reads as a highlight
            of the whole hour rather than of the bar alone. */}
        {hoverHour !== null && (
          <rect
            x={hourX(hoverHour)}
            y={PAD.top}
            width={barW}
            height={lanesBottom - PAD.top}
            className="day-rail-hover-band"
          />
        )}

        {/* Price bars. Colour marks membership of a scheduled window. */}
        {data.prices.map((p) => {
          const zeroY = priceY(0);
          const top = priceY(Math.max(p.price_eur_mwh, 0));
          const bottom = p.price_eur_mwh >= 0 ? zeroY : priceY(p.price_eur_mwh);
          const kind = chargeHours.has(p.hour)
            ? "charge"
            : dischargeHours.has(p.hour)
              ? "discharge"
              : "neutral";
          return (
            <rect
              key={p.hour}
              x={hourX(p.hour) + 1}
              y={Math.min(top, bottom)}
              width={Math.max(barW - 2, 1)}
              height={Math.max(Math.abs(bottom - top), 1)}
              rx="2"
              className={`day-rail-bar day-rail-bar-${kind}${hoverHour === p.hour ? " day-rail-bar-hover" : ""}`}
            />
          );
        })}

        {/* The two configured price limits — the only reference lines kept, as
            they are the ones a person can act on. */}
        {limits.charge_limit_eur_mwh > 0 && (
          <ReferenceLine
            y={priceY(limits.charge_limit_eur_mwh)}
            x2={WIDTH - PAD.right}
            x1={PAD.left}
            kind="charge"
            label={`charge ≤ ${limits.charge_limit_eur_mwh}`}
          />
        )}
        {limits.discharge_limit_eur_mwh > 0 && (
          <ReferenceLine
            y={priceY(limits.discharge_limit_eur_mwh)}
            x2={WIDTH - PAD.right}
            x1={PAD.left}
            kind="discharge"
            label={`discharge ≥ ${limits.discharge_limit_eur_mwh}`}
          />
        )}

        {/* Lanes share the price axis, so a block sits directly under the hours
            that produced it. */}
        {lanes.map((lane, i) => (
          <g key={lane.key}>
            <text
              x={PAD.left - 7}
              y={laneY(i) + LANE_H / 2 + 3}
              textAnchor="end"
              className="day-rail-lane-label"
            >
              {lane.label}
            </text>
            <rect
              x={PAD.left}
              y={laneY(i)}
              width={chartW}
              height={LANE_H}
              rx="3"
              className="day-rail-lane-bed"
            />
            {lane.blocks.map((b) => (
              <rect
                key={b.key}
                x={hourX(b.from)}
                y={laneY(i)}
                width={Math.max(hourX(b.to) - hourX(b.from), 2)}
                height={LANE_H}
                rx="3"
                className={`day-rail-block day-rail-block-${b.kind}${b.open ? " day-rail-block-open" : ""}`}
              >
                <title>{b.title}</title>
              </rect>
            ))}
          </g>
        ))}

        {/* Now line crosses every lane, which is the whole point of sharing one
            axis: past and future read at a glance. */}
        {isToday && (
          <g className="day-rail-now">
            <line x1={nowX} x2={nowX} y1={PAD.top - 4} y2={lanesBottom + 4} />
            <text x={nowX} y={PAD.top - 8} textAnchor="middle">
              now
            </text>
          </g>
        )}

        {/* Hour axis */}
        {[0, 3, 6, 9, 12, 15, 18, 21].map((h) => (
          <text
            key={h}
            x={hourX(h) + barW / 2}
            y={height - 7}
            textAnchor="middle"
            className="day-rail-axis-label"
          >
            {String(h).padStart(2, "0")}
          </text>
        ))}

        {/* Hover targets sit on top so the whole column is grabbable. */}
        {data.prices.map((p) => (
          <rect
            key={`hit-${p.hour}`}
            x={hourX(p.hour)}
            y={PAD.top}
            width={barW}
            height={lanesBottom - PAD.top}
            fill="transparent"
            onMouseEnter={() => setHoverHour(p.hour)}
          />
        ))}
      </svg>
      </div>

      <div className="day-rail-readout" aria-live="polite">
        {hovered ? (
          <>
            <span className="day-rail-readout-hour">
              {String(hovered.hour).padStart(2, "0")}:00
            </span>
            <span className="day-rail-readout-price">
              {hovered.price_eur_mwh.toFixed(1)} EUR/MWh
            </span>
            {chargeHours.has(hovered.hour) && (
              <span className="day-rail-readout-tag charge">charge window</span>
            )}
            {dischargeHours.has(hovered.hour) && (
              <span className="day-rail-readout-tag discharge">
                discharge window
              </span>
            )}
          </>
        ) : (
          <span className="day-rail-readout-hint">
            Hover an hour for its price
          </span>
        )}
      </div>
    </div>
  );
}

function ReferenceLine({
  y,
  x1,
  x2,
  kind,
  label,
}: {
  y: number;
  x1: number;
  x2: number;
  kind: "charge" | "discharge";
  label: string;
}) {
  return (
    <g className={`day-rail-limit day-rail-limit-${kind}`}>
      <line x1={x1} x2={x2} y1={y} y2={y} />
      <text x={x2 + 6} y={y + 3} textAnchor="start">
        {label}
      </text>
    </g>
  );
}

function hourSet(windows: DayData["schedule"]["charge_windows"]): Set<number> {
  const set = new Set<number>();
  for (const w of windows ?? []) {
    for (let h = w.start_hour; h < w.end_hour; h++) set.add(h);
  }
  return set;
}

function buildPlanLane(data: DayData): Lane {
  const blocks: Block[] = [];
  (data.schedule.charge_windows ?? []).forEach((w, i) =>
    blocks.push({
      key: `plan-c-${i}`,
      from: w.start_hour,
      to: w.end_hour,
      kind: "charge",
      title: `Planned charge ${pad(w.start_hour)}:00–${pad(w.end_hour)}:00 · avg ${w.avg_price_eur_mwh.toFixed(1)} EUR/MWh`,
    }),
  );
  (data.schedule.discharge_windows ?? []).forEach((w, i) =>
    blocks.push({
      key: `plan-d-${i}`,
      from: w.start_hour,
      to: w.end_hour,
      kind: "discharge",
      title: `Planned discharge ${pad(w.start_hour)}:00–${pad(w.end_hour)}:00 · avg ${w.avg_price_eur_mwh.toFixed(1)} EUR/MWh`,
    }),
  );
  return { key: "plan", label: "Plan", blocks };
}

function buildSessionLanes(sessions: SessionRecord[]): Lane[] {
  const byBattery = new Map<string, Block[]>();

  for (const s of sessions) {
    if (s.type !== "charge" && s.type !== "discharge") continue;
    const start = new Date(s.started_at);
    if (Number.isNaN(start.getTime())) continue;

    const from = start.getHours() + start.getMinutes() / 60;
    const open = !s.ended_at;
    const end = open ? new Date() : new Date(s.ended_at as string);
    // A session running past midnight is clamped to the end of this day's axis.
    const rawTo = end.getHours() + end.getMinutes() / 60;
    const to = rawTo <= from ? 24 : rawTo;

    const blocks = byBattery.get(s.battery_name) ?? [];
    blocks.push({
      key: `s-${s.id}`,
      from,
      to,
      kind: s.type,
      open,
      title: `${s.type === "charge" ? "Charged" : "Discharged"} ${clock(start)}–${open ? "now" : clock(end)} · ${(s.energy_wh / 1000).toFixed(2)} kWh · SoC ${s.soc_start.toFixed(0)}→${s.soc_end.toFixed(0)}%`,
    });
    byBattery.set(s.battery_name, blocks);
  }

  return [...byBattery.entries()]
    .sort((a, b) => a[0].localeCompare(b[0]))
    .map(([name, blocks]) => ({ key: `bat-${name}`, label: name, blocks }));
}

function pad(n: number): string {
  return String(n).padStart(2, "0");
}

function clock(d: Date): string {
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function gridLines(min: number, max: number, range: number): number[] {
  const step = niceStep(range, 4);
  const out: number[] = [];
  for (let v = Math.ceil(min / step) * step; v <= max; v += step) out.push(v);
  return out;
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

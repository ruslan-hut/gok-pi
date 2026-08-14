import { useCallback, useEffect, useRef, useState } from "react";
import type { TelemetryPoint } from "../../types";

/**
 * HistoryChart plots what the battery has been doing over a time window: its
 * charge level, and the power flowing through it and the house.
 *
 * Level (%) and power (W) are different measures, so they get their own panels
 * stacked on one shared time axis rather than two y-scales on one plot — reading
 * down a column still answers "what was the load when it stopped discharging?",
 * which is the question a dual axis would answer by lying about scale.
 *
 * Colour follows the app's existing semantics: charge green, discharge amber,
 * with the zero baseline separating them by position as well as hue (that pair
 * sits in the CVD floor band, so the baseline is doing real work, not decoration).
 * House load is deliberately achromatic — it is context for the battery trace,
 * not a third competing identity, and it is separated by mark type too: a thin
 * line against a filled area.
 */

/**
 * The chart is drawn in viewBox units, so its aspect ratio is fixed by these
 * numbers and everything — including text — scales with the container. A single
 * wide geometry squeezed onto a phone therefore ends up both too short to read
 * and too small to label, which is why there are two: the narrow one keeps the
 * same panel heights over a shorter width, so the plot gets proportionally
 * taller and the type proportionally bigger.
 */
const WIDE_WIDTH = 1000;
const NARROW_WIDTH = 520;
/** Below this rendered width the narrow geometry is used. */
const NARROW_BREAKPOINT = 560;

const PAD = { top: 16, right: 16, bottom: 20, left: 46 };
const SOC_H = 96;
const POWER_H = 116;
const PANEL_GAP = 34;
const COVERAGE_H = 5;

interface HistoryChartProps {
  points: TelemetryPoint[];
  /** Window bounds in ms, so gaps at the edges are visible rather than cropped. */
  from: number;
  to: number;
}

interface Sample extends TelemetryPoint {
  t: number;
}

export function HistoryChart({ points, from, to }: HistoryChartProps) {
  const [hoverT, setHoverT] = useState<number | null>(null);
  const [container, containerWidth] = useContainerWidth();

  // Until the container has been measured, assume the wide geometry: it is what
  // desktop gets, and one frame of the wrong aspect ratio is less jarring than
  // starting narrow and snapping wider.
  const compact = containerWidth > 0 && containerWidth < NARROW_BREAKPOINT;
  const WIDTH = compact ? NARROW_WIDTH : WIDE_WIDTH;

  const samples: Sample[] = points
    .map((p) => ({ ...p, t: new Date(p.bucket).getTime() }))
    .filter((s) => !Number.isNaN(s.t))
    .sort((a, b) => a.t - b.t);

  const chartW = WIDTH - PAD.left - PAD.right;
  const span = Math.max(to - from, 1);
  const x = (t: number) => PAD.left + ((t - from) / span) * chartW;

  const socTop = PAD.top;
  const socY = (v: number) => socTop + SOC_H - (clamp(v, 0, 100) / 100) * SOC_H;

  const powerTop = socTop + SOC_H + PANEL_GAP;
  const peak = Math.max(
    1000,
    ...samples.map((s) => Math.max(Math.abs(s.pac_min_w), Math.abs(s.pac_max_w), s.consumption_max_w)),
  );
  const powerScale = niceCeil(peak);
  const powerY = (v: number) =>
    powerTop + POWER_H / 2 - (clamp(v, -powerScale, powerScale) / powerScale) * (POWER_H / 2);
  const zeroY = powerY(0);

  const coverageTop = powerTop + POWER_H + 12;
  const height = coverageTop + COVERAGE_H + PAD.bottom;

  // A run is a stretch of consecutive minutes. Splitting on gaps keeps the line
  // from bridging an outage with a straight segment that never happened.
  const runs = splitRuns(samples);
  const ticks = timeTicks(from, to, compact);

  const hovered = hoverT === null ? null : nearest(samples, hoverT);
  const hoverX = hovered ? x(hovered.t) : null;

  return (
    <div className="history-chart">
      <div className="history-chart-scroll" ref={container}>
        <svg
          viewBox={`0 0 ${WIDTH} ${height}`}
          className="history-chart-svg"
          role="img"
          aria-label="Battery charge level, battery power and house load over the selected window"
          onMouseLeave={() => setHoverT(null)}
          onMouseMove={(e) => {
            const svg = e.currentTarget;
            const rect = svg.getBoundingClientRect();
            const px = ((e.clientX - rect.left) / rect.width) * WIDTH;
            if (px < PAD.left || px > WIDTH - PAD.right) {
              setHoverT(null);
              return;
            }
            setHoverT(from + ((px - PAD.left) / chartW) * span);
          }}
        >
          <defs>
            <clipPath id="history-clip-out">
              <rect x={PAD.left} y={powerTop} width={chartW} height={zeroY - powerTop} />
            </clipPath>
            <clipPath id="history-clip-in">
              <rect x={PAD.left} y={zeroY} width={chartW} height={powerTop + POWER_H - zeroY} />
            </clipPath>
          </defs>

          {/* ── Charge level ── */}
          {[0, 50, 100].map((v) => (
            <g key={`soc-${v}`}>
              <line
                x1={PAD.left}
                x2={WIDTH - PAD.right}
                y1={socY(v)}
                y2={socY(v)}
                className="history-grid"
              />
              {/* The unit rides the top tick rather than sitting on its own line
                  above it: at this scale the two stack close enough to read as
                  one label broken across two lines. */}
              <text x={PAD.left - 6} y={socY(v) + 3} textAnchor="end" className="history-axis-label">
                {v === 100 ? "100%" : v}
              </text>
            </g>
          ))}

          {runs.map((run, i) => (
            <g key={`soc-run-${i}`}>
              <path d={areaPath(run, x, socY, (s) => s.usoc, socY(0))} className="history-soc-area" />
              <path d={linePath(run, x, socY, (s) => s.usoc)} className="history-soc-line" />
            </g>
          ))}

          {/* ── Power ── */}
          <line
            x1={PAD.left}
            x2={WIDTH - PAD.right}
            y1={zeroY}
            y2={zeroY}
            className="history-zero-line"
          />
          {[powerScale, -powerScale].map((v) => (
            <text
              key={`p-${v}`}
              x={PAD.left - 6}
              y={powerY(v) + (v > 0 ? 8 : -2)}
              textAnchor="end"
              className="history-axis-label"
            >
              {v > 0 ? `${fmtScale(v)}W` : fmtScale(v)}
            </text>
          ))}

          {/* Battery power splits at the baseline: out (discharging) above,
              in (charging) below, matching the "out"/"in" wording used on the
              live cards. */}
          {runs.map((run, i) => (
            <g key={`pac-run-${i}`}>
              <path
                d={bandPath(run, x, powerY, (s) => Math.max(s.pac_avg_w, 0), zeroY)}
                className="history-pac-out"
              />
              <path
                d={bandPath(run, x, powerY, (s) => Math.min(s.pac_avg_w, 0), zeroY)}
                className="history-pac-in"
              />
              {/* A stroke along the trace keeps the edge crisp: the fills are
                  translucent so they never bury the load line crossing them. It
                  is drawn twice, clipped to each side of zero, so the line keeps
                  the same colour as the fill it borders. */}
              <path
                d={linePath(run, x, powerY, (s) => s.pac_avg_w)}
                className="history-pac-line out"
                clipPath="url(#history-clip-out)"
              />
              <path
                d={linePath(run, x, powerY, (s) => s.pac_avg_w)}
                className="history-pac-line in"
                clipPath="url(#history-clip-in)"
              />
              <path d={linePath(run, x, powerY, (s) => s.consumption_avg_w)} className="history-load-line" />
            </g>
          ))}

          {/* ── Coverage strip: minutes the server actually received. A hole here
              means missing telemetry, not an idle battery. ── */}
          <text
            x={PAD.left - 6}
            y={coverageTop + COVERAGE_H}
            textAnchor="end"
            className="history-lane-label"
          >
            data
          </text>
          <rect
            x={PAD.left}
            y={coverageTop}
            width={chartW}
            height={COVERAGE_H}
            rx="2"
            className="history-coverage-bed"
          >
            <title>Minutes of telemetry the server received. A break is missing data.</title>
          </rect>
          {runs.map((run, i) => (
            <rect
              key={`cov-${i}`}
              x={x(run[0].t)}
              y={coverageTop}
              width={Math.max(x(run[run.length - 1].t) - x(run[0].t), 1.5)}
              height={COVERAGE_H}
              rx="2"
              className="history-coverage"
            />
          ))}

          {/* ── Time axis ── */}
          {ticks.map((tick) => (
            <text
              key={tick.t}
              x={x(tick.t)}
              y={height - 6}
              textAnchor="middle"
              className="history-axis-label"
            >
              {tick.label}
            </text>
          ))}

          {/* Crosshair spans both panels: the point of one shared axis. */}
          {hoverX !== null && (
            <g className="history-crosshair">
              <line x1={hoverX} x2={hoverX} y1={socTop - 4} y2={coverageTop + COVERAGE_H} />
              <circle cx={hoverX} cy={socY(hovered!.usoc)} r="3" className="history-dot-soc" />
              <circle cx={hoverX} cy={powerY(hovered!.pac_avg_w)} r="3" className="history-dot-pac" />
            </g>
          )}
        </svg>
      </div>

      <Readout point={hovered} />

      <div className="history-legend">
        <LegendItem className="soc" label="Charge level" />
        <LegendItem className="out" label="Battery out" />
        <LegendItem className="in" label="Battery in" />
        <LegendItem className="load" label="House load" />
      </div>
    </div>
  );
}

function LegendItem({ className, label }: { className: string; label: string }) {
  return (
    <span className="history-legend-item">
      <span className={`history-legend-swatch ${className}`} aria-hidden="true" />
      {label}
    </span>
  );
}

function Readout({ point }: { point: Sample | null }) {
  if (!point) {
    return (
      <div className="history-readout" aria-live="polite">
        <span className="history-readout-hint">Hover the chart for a reading</span>
      </div>
    );
  }

  const flow = point.pac_avg_w > 0 ? "out" : point.pac_avg_w < 0 ? "in" : "";
  const incomplete = point.samples > 0 && point.samples < 6;

  return (
    <div className="history-readout" aria-live="polite">
      <span className="history-readout-time">{clockLabel(point.t)}</span>
      <span className="history-readout-value">{point.usoc.toFixed(0)}%</span>
      <span className="history-readout-value">
        {Math.abs(point.pac_avg_w).toFixed(0)} W
        {flow && <span className="history-readout-qualifier"> {flow}</span>}
      </span>
      <span className="history-readout-value">
        {point.consumption_avg_w.toFixed(0)} W
        <span className="history-readout-qualifier"> load</span>
      </span>
      {incomplete && (
        <span className="history-readout-warn" title="Fewer than the expected 6 telemetry frames arrived for this minute">
          {point.samples}/6 frames
        </span>
      )}
    </div>
  );
}

/**
 * splitRuns breaks the series wherever more than a couple of minutes are missing,
 * so an uplink gap shows as a gap instead of being interpolated away.
 */
function splitRuns(samples: Sample[]): Sample[][] {
  const GAP_MS = 3 * 60 * 1000;
  const runs: Sample[][] = [];
  let current: Sample[] = [];

  for (const s of samples) {
    const prev = current[current.length - 1];
    if (prev && s.t - prev.t > GAP_MS) {
      runs.push(current);
      current = [];
    }
    current.push(s);
  }
  if (current.length) runs.push(current);
  return runs;
}

function linePath(
  run: Sample[],
  x: (t: number) => number,
  y: (v: number) => number,
  value: (s: Sample) => number,
): string {
  return run.map((s, i) => `${i === 0 ? "M" : "L"}${x(s.t).toFixed(2)} ${y(value(s)).toFixed(2)}`).join(" ");
}

function areaPath(
  run: Sample[],
  x: (t: number) => number,
  y: (v: number) => number,
  value: (s: Sample) => number,
  base: number,
): string {
  if (!run.length) return "";
  const top = linePath(run, x, y, value);
  return `${top} L${x(run[run.length - 1].t).toFixed(2)} ${base.toFixed(2)} L${x(run[0].t).toFixed(2)} ${base.toFixed(2)} Z`;
}

/** bandPath fills between the zero baseline and one signed side of the trace. */
function bandPath(
  run: Sample[],
  x: (t: number) => number,
  y: (v: number) => number,
  value: (s: Sample) => number,
  base: number,
): string {
  if (!run.length) return "";
  return areaPath(run, x, y, value, base);
}

function nearest(samples: Sample[], t: number): Sample | null {
  if (!samples.length) return null;
  let best = samples[0];
  let bestDist = Math.abs(best.t - t);
  for (const s of samples) {
    const dist = Math.abs(s.t - t);
    if (dist < bestDist) {
      best = s;
      bestDist = dist;
    }
  }
  // Beyond a few minutes there is no sample under the cursor — usually a gap.
  return bestDist <= 5 * 60 * 1000 ? best : null;
}

function timeTicks(
  from: number,
  to: number,
  compact: boolean,
): { t: number; label: string }[] {
  const span = to - from;
  const hours = span / 3_600_000;
  let stepH = hours <= 6 ? 1 : hours <= 30 ? 3 : hours <= 96 ? 12 : 24;
  // Half as many labels on a narrow chart: the same count would collide once the
  // type is scaled up.
  if (compact) stepH *= 2;
  const step = stepH * 3_600_000;

  const first = new Date(from);
  first.setMinutes(0, 0, 0);
  let t = first.getTime();
  while (t < from) t += 3_600_000;
  // Align to the step so labels land on whole hours people recognise.
  while (new Date(t).getHours() % stepH !== 0 && t < to) t += 3_600_000;

  const ticks: { t: number; label: string }[] = [];
  for (; t <= to; t += step) {
    const d = new Date(t);
    const label =
      stepH >= 24
        ? `${d.getDate()}/${d.getMonth() + 1}`
        : `${String(d.getHours()).padStart(2, "0")}:00`;
    ticks.push({ t, label });
  }
  return ticks;
}

function clockLabel(t: number): string {
  const d = new Date(t);
  const time = `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
  const today = new Date();
  const sameDay =
    d.getFullYear() === today.getFullYear() &&
    d.getMonth() === today.getMonth() &&
    d.getDate() === today.getDate();
  return sameDay ? time : `${d.getDate()}/${d.getMonth() + 1} ${time}`;
}

/**
 * useContainerWidth reports the rendered width of an element, so the chart can
 * pick its geometry from the space it actually has rather than from a CSS
 * breakpoint that knows nothing about the panel it sits in.
 */
function useContainerWidth(): [(node: HTMLDivElement | null) => void, number] {
  const [width, setWidth] = useState(0);
  const observer = useRef<ResizeObserver | null>(null);

  const ref = useCallback((node: HTMLDivElement | null) => {
    observer.current?.disconnect();
    if (!node) return;

    setWidth(node.clientWidth);
    observer.current = new ResizeObserver((entries) => {
      for (const entry of entries) setWidth(entry.contentRect.width);
    });
    observer.current.observe(node);
  }, []);

  useEffect(() => () => observer.current?.disconnect(), []);

  return [ref, width];
}

function clamp(v: number, min: number, max: number): number {
  return Math.min(Math.max(v, min), max);
}

/**
 * niceCeil rounds the axis up to a readable bound. The steps are fine-grained on
 * purpose: jumping 2.2kW straight to 5kW would squash a real discharge into the
 * bottom half of the panel.
 */
function niceCeil(v: number): number {
  const mag = Math.pow(10, Math.floor(Math.log10(v)));
  const norm = v / mag;
  const step = [1, 1.5, 2, 2.5, 3, 4, 5, 7.5, 10].find((s) => norm <= s) ?? 10;
  return step * mag;
}

function fmtScale(v: number): string {
  const abs = Math.abs(v);
  const label = abs >= 1000 ? `${(abs / 1000).toFixed(abs % 1000 === 0 ? 0 : 1)}k` : `${abs}`;
  return v < 0 ? `−${label}` : label;
}

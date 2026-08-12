import { useEffect, useRef, useState } from "react";

interface MetricProps {
  label: string;
  value: string;
  /** Explains the term. Vendor abbreviations are not self-evident. */
  hint?: string;
  /** Trailing qualifier shown dimmer than the value, e.g. a flow direction. */
  qualifier?: string;
}

export function Metric({ label, value, hint, qualifier }: MetricProps) {
  const changed = useValueChanged(value);

  return (
    <div className="metric">
      <span className="metric-label" title={hint}>
        {label}
        {hint && <span className="metric-hint-mark" aria-hidden="true" />}
      </span>
      <span className={`metric-value${changed ? " metric-value-changed" : ""}`}>
        {value}
        {qualifier && <span className="metric-qualifier"> {qualifier}</span>}
      </span>
    </div>
  );
}

/**
 * useValueChanged flags a value for a moment after it changes, so the one piece
 * of motion on this screen means new telemetry arrived rather than decoration.
 */
function useValueChanged(value: string): boolean {
  const [changed, setChanged] = useState(false);
  const previous = useRef(value);

  useEffect(() => {
    if (previous.current === value) return;
    previous.current = value;
    setChanged(true);
    const timer = window.setTimeout(() => setChanged(false), 700);
    return () => window.clearTimeout(timer);
  }, [value]);

  return changed;
}

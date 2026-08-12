// Shared value formatting. Energy, money and timestamps were previously
// formatted three different ways in three different files.

const LOCALE = "en-GB";

/** fmtEUR renders a money amount at fixed 2dp so columns align on the decimal. */
export function fmtEUR(eur: number): string {
  return `${eur.toFixed(2)} EUR`;
}

/**
 * fmtSignedEUR keeps the sign, which carries the meaning: the backend stores
 * charge cost as negative and discharge revenue as positive, so a positive net
 * is a profit.
 */
export function fmtSignedEUR(eur: number): string {
  const sign = eur > 0 ? "+" : eur < 0 ? "−" : "";
  return `${sign}${Math.abs(eur).toFixed(2)} EUR`;
}

/**
 * fmtEnergy renders watt-hours, switching to kWh above 1000. Precision drops as
 * the magnitude grows — "4120.00 kWh" spends two digits saying nothing.
 */
export function fmtEnergy(wh: number): string {
  if (!wh) return "—";
  const abs = Math.abs(wh);
  if (abs < 1000) return `${wh.toFixed(0)} Wh`;
  const kwh = wh / 1000;
  const decimals = Math.abs(kwh) >= 100 ? 0 : Math.abs(kwh) >= 10 ? 1 : 2;
  return `${kwh.toFixed(decimals)} kWh`;
}

export function fmtBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export function fmtDuration(seconds: number): string {
  if (seconds < 60) return `${seconds.toFixed(0)}s`;
  if (seconds < 3600) return `${(seconds / 60).toFixed(0)}m`;
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  return `${h}h ${m}m`;
}

/** fmtDateTime pins the locale so month names match the English interface. */
export function fmtDateTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString(LOCALE, {
    day: "2-digit",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function fmtDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString(LOCALE, {
    day: "2-digit",
    month: "short",
    year: "numeric",
  });
}

export function fmtTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleTimeString(LOCALE, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

/**
 * fmtAgo answers "is this live?" — the question a liveness readout exists to
 * answer. Falls back to a date once the gap stops being about freshness.
 */
export function fmtAgo(iso: string): string {
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return "—";
  const seconds = Math.round((Date.now() - then) / 1000);
  if (seconds < 0) return "just now";
  if (seconds < 10) return "just now";
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return fmtDateTime(iso);
}

import { useState } from "react";
import { scanForBattery } from "../../api";
import type { BatteryScanResult } from "../../types";
import { Icon } from "../shared/Icon";

/**
 * configuredHost pulls the host out of the battery's configured URL.
 *
 * Comparing addresses by substring would be wrong in a way that is easy to miss:
 * a configured 192.168.68.142 "contains" a found 192.168.68.14, so the shorter
 * address would be labelled as the configured one and a battery that had moved
 * would be reported as sitting where it always was.
 */
function configuredHost(url?: string): string {
  if (!url) return "";
  try {
    return new URL(url.includes("://") ? url : `http://${url}`).hostname;
  } catch {
    return "";
  }
}

interface BatteryScanProps {
  agentId?: string;
  online: boolean;
  /** Configured battery names, used to pick which battery to look for. */
  batteryNames: string[];
}

/**
 * BatteryScan asks the agent to sweep its subnet for the battery.
 *
 * It exists for one ambiguity the dashboard cannot resolve: a battery that stops
 * answering looks the same whether it took a new DHCP lease or died. Only the
 * device has a route to the battery's network, so only the device can tell the
 * two apart — and the answer decides whether the fix is editing an address or
 * walking over to the hardware.
 *
 * The range is left blank by default, which means "the subnet the battery is
 * configured on". That is where a battery with a new lease nearly always is, and
 * it keeps the common case down to one click.
 */
export function BatteryScan({ agentId, online, batteryNames }: BatteryScanProps) {
  const [battery, setBattery] = useState("");
  const [range, setRange] = useState("");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<BatteryScanResult | null>(null);
  const [error, setError] = useState("");

  const disabled = !agentId || !online || busy;

  const onScan = async () => {
    if (!agentId) return;
    setBusy(true);
    setError("");
    setResult(null);
    try {
      const chosen = battery || (batteryNames.length === 1 ? batteryNames[0] : "");
      setResult(
        await scanForBattery(agentId, {
          ...(chosen ? { battery: chosen } : {}),
          ...(range.trim() ? { range: range.trim() } : {}),
        }),
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : "Scan failed");
    } finally {
      setBusy(false);
    }
  };

  // The found battery first: on a /24 the rest of the list is whatever else
  // happens to run a web server, and it is only context.
  const entries = result
    ? [...result.results].sort((a, b) => Number(b.battery) - Number(a.battery))
    : [];
  const knownHost = configuredHost(result?.configured_url);
  const found = result?.results.filter((r) => r.battery) ?? [];
  const movedTo = found.find((r) => r.address !== knownHost);

  return (
    <section className="config-panel">
      <div className="config-panel-header">
        <h3>Find battery</h3>
      </div>

      <div className="battery-scan">
        <p className="config-section-intro">
          Sweeps an address range from the device, looking for a host that answers as the
          battery. Use it when the battery stops responding: it tells a battery that moved to
          a new DHCP lease from one that is genuinely off the air. Read-only — it reads the
          battery's status and changes nothing.
        </p>

        <div className="battery-scan-controls">
          {batteryNames.length > 1 && (
            <label className="form-field">
              <span>Battery</span>
              <select value={battery} onChange={(e) => setBattery(e.target.value)} disabled={disabled}>
                <option value="">Select a battery…</option>
                {batteryNames.map((name) => (
                  <option key={name} value={name}>
                    {name}
                  </option>
                ))}
              </select>
            </label>
          )}

          <label className="form-field">
            <span>Address range</span>
            <input
              type="text"
              value={range}
              onChange={(e) => setRange(e.target.value)}
              placeholder="the battery's own subnet"
              disabled={disabled}
            />
          </label>

          <button type="button" className="button-small" onClick={onScan} disabled={disabled}>
            <Icon name={busy ? "hourglass_empty" : "travel_explore"} size={16} />
            {busy ? "Scanning…" : "Scan"}
          </button>
        </div>

        <small className="form-help-text battery-scan-help">
          Leave the range empty to sweep the /24 around the configured address. Accepts
          <code>192.168.1.0/24</code>, <code>192.168.1.100-150</code> or a comma-separated list.
        </small>

        {busy && (
          <small className="form-help-text">
            A full /24 takes a few seconds; a range where many hosts answer takes longer.
          </small>
        )}

        {!online && (
          <div className="offline-warning">Agent is offline. The scan runs on the device.</div>
        )}

        <div className="form-status" aria-live="polite">
          {error && <small className="form-error">{error}</small>}
        </div>

        {result && (
          <div className="battery-scan-result">
            <p className="battery-scan-summary">
              {result.found === 0 ? (
                <>
                  <Icon name="search_off" size={16} />
                  <span>
                    No battery answered on <code>{result.range}</code>. It is not on this range —
                    check power and cabling, or try another range.
                  </span>
                </>
              ) : movedTo ? (
                <>
                  <Icon name="swap_horiz" size={16} />
                  <span>
                    Found at <strong>{movedTo.address}</strong>, which is not the configured
                    address. Update the battery URL in Configure to point at it.
                  </span>
                </>
              ) : (
                <>
                  <Icon name="check_circle" size={16} />
                  <span>
                    The battery answered at its configured address. Whatever is wrong is not the
                    address.
                  </span>
                </>
              )}
            </p>

            <small className="form-help-text">
              {result.battery} · swept {result.scanned} addresses on <code>{result.range}</code>{" "}
              port {result.port} · {result.responded} answered · {(result.elapsed_ms / 1000).toFixed(1)}s
              {result.truncated && " · stopped early"}
            </small>

            {entries.length > 0 && (
              <table className="battery-scan-table">
                <thead>
                  <tr>
                    <th>Address</th>
                    <th>Response</th>
                  </tr>
                </thead>
                <tbody>
                  {entries.map((entry) => (
                    <tr key={entry.address} className={entry.battery ? "battery-scan-hit" : undefined}>
                      <td>
                        <code>{entry.address}</code>
                        {entry.address === knownHost && (
                          <small className="battery-scan-tag">configured</small>
                        )}
                      </td>
                      <td>
                        {entry.battery && <Icon name="battery_full" size={14} />}
                        {entry.detail || (entry.port_open ? "port open" : "no response")}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        )}
      </div>
    </section>
  );
}

import { useCallback, useEffect, useState } from "react";
import { fetchDBRecords, fetchDBStats } from "../../api";
import { Icon } from "../shared/Icon";
import type { DBRecordsQuery, DBRecordsResponse, DBStats } from "../../types";

const PAGE_SIZE = 50;

export function DatabaseInspector() {
  const [data, setData] = useState<DBRecordsResponse | null>(null);
  const [dbStats, setDbStats] = useState<DBStats | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>();

  // Filter state
  const [agentId, setAgentId] = useState("");
  const [type, setType] = useState("");
  const [status, setStatus] = useState("");
  const [dateFrom, setDateFrom] = useState("");
  const [dateTo, setDateTo] = useState("");
  const [offset, setOffset] = useState(0);

  // Expanded row
  const [expandedId, setExpandedId] = useState<number | null>(null);

  const load = useCallback(async (query: DBRecordsQuery) => {
    setLoading(true);
    setError(undefined);
    try {
      const result = await fetchDBRecords(query);
      setData(result);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load records");
    } finally {
      setLoading(false);
    }
  }, []);

  const buildQuery = useCallback(
    (pageOffset: number): DBRecordsQuery => ({
      agent_id: agentId || undefined,
      type: type || undefined,
      status: status || undefined,
      date_from: dateFrom ? new Date(dateFrom).toISOString() : undefined,
      date_to: dateTo ? new Date(dateTo + "T23:59:59").toISOString() : undefined,
      limit: PAGE_SIZE,
      offset: pageOffset,
    }),
    [agentId, type, status, dateFrom, dateTo],
  );

  // Initial load
  useEffect(() => {
    load(buildQuery(0));
    fetchDBStats()
      .then((s) => setDbStats(s.stats))
      .catch(() => {});
  }, []);

  const handleApplyFilters = () => {
    setOffset(0);
    setExpandedId(null);
    load(buildQuery(0));
  };

  const handleClearFilters = () => {
    setAgentId("");
    setType("");
    setStatus("");
    setDateFrom("");
    setDateTo("");
    setOffset(0);
    setExpandedId(null);
    load({ limit: PAGE_SIZE, offset: 0 });
  };

  const handlePageChange = (newOffset: number) => {
    setOffset(newOffset);
    setExpandedId(null);
    load(buildQuery(newOffset));
  };

  const totalPages = data ? Math.ceil(data.total / PAGE_SIZE) : 0;
  const currentPage = data ? Math.floor(offset / PAGE_SIZE) + 1 : 1;

  return (
    <div className="db-inspector">
      <h2>Database Inspector</h2>

      {/* Stats summary */}
      {dbStats && (
        <div className="db-inspector-stats">
          <div className="db-stats-grid">
            <div className="db-stats-item">
              <span className="db-stats-label">Size</span>
              <span className="db-stats-value">{fmtBytes(dbStats.file_size_bytes)}</span>
            </div>
            <div className="db-stats-item">
              <span className="db-stats-label">Total</span>
              <span className="db-stats-value">{dbStats.total_sessions}</span>
            </div>
            <div className="db-stats-item">
              <span className="db-stats-label">Open</span>
              <span className="db-stats-value">{dbStats.open_sessions}</span>
            </div>
            <div className="db-stats-item">
              <span className="db-stats-label">Range</span>
              <span className="db-stats-value">
                {dbStats.oldest_session
                  ? `${new Date(dbStats.oldest_session).toLocaleDateString()} — ${dbStats.newest_session ? new Date(dbStats.newest_session).toLocaleDateString() : "now"}`
                  : "—"}
              </span>
            </div>
          </div>
        </div>
      )}

      {/* Filters */}
      <div className="db-inspector-filters card">
        <div className="db-inspector-filters-header">
          <Icon name="filter_list" size={18} />
          <span>Filters</span>
          {data && (
            <span className="db-inspector-count">
              {data.total} record{data.total !== 1 ? "s" : ""} found
            </span>
          )}
        </div>
        <div className="db-inspector-filter-grid">
          <div className="form-field">
            <label>Agent</label>
            <select value={agentId} onChange={(e) => setAgentId(e.target.value)}>
              <option value="">All agents</option>
              {data?.agents?.map((a) => (
                <option key={a} value={a}>{a}</option>
              ))}
            </select>
          </div>
          <div className="form-field">
            <label>Type</label>
            <select value={type} onChange={(e) => setType(e.target.value)}>
              <option value="">All types</option>
              <option value="charge">Charge</option>
              <option value="discharge">Discharge</option>
            </select>
          </div>
          <div className="form-field">
            <label>Status</label>
            <select value={status} onChange={(e) => setStatus(e.target.value)}>
              <option value="">All</option>
              <option value="open">Open</option>
              <option value="closed">Closed</option>
            </select>
          </div>
          <div className="form-field">
            <label>From</label>
            <input type="date" value={dateFrom} onChange={(e) => setDateFrom(e.target.value)} />
          </div>
          <div className="form-field">
            <label>To</label>
            <input type="date" value={dateTo} onChange={(e) => setDateTo(e.target.value)} />
          </div>
        </div>
        <div className="db-inspector-filter-actions">
          <button className="button-small" onClick={handleApplyFilters}>
            <Icon name="search" size={16} />
            Apply
          </button>
          <button className="button-small button-secondary" onClick={handleClearFilters}>
            Clear
          </button>
        </div>
      </div>

      {/* Error */}
      {error && <div className="config-error">{error}</div>}

      {/* Loading */}
      {loading && !data && (
        <div className="config-loading">
          <div className="spinner" />
          <span>Loading records...</span>
        </div>
      )}

      {/* Records table */}
      {data && data.records && data.records.length > 0 && (
        <>
          {/* Card list */}
          <div className="card-list">
            {data.records.map((r) => (
              <div
                key={r.id}
                className="data-card db-inspector-card"
                onClick={() => setExpandedId(expandedId === r.id ? null : r.id)}
              >
                <div className="data-card-header">
                  <span className="data-card-title">
                    #{r.id} — {r.battery_name}
                  </span>
                  <span className={`schedule-badge ${r.type === "charge" ? "schedule-badge-charge" : "schedule-badge-discharge"}`}>
                    {r.type}
                  </span>
                </div>
                <div className="data-card-row">
                  <span className="data-card-label">Agent</span>
                  <span className="data-card-value">{r.agent_id}</span>
                </div>
                <div className="data-card-row">
                  <span className="data-card-label">Started</span>
                  <span className="data-card-value">{fmtDateTime(r.started_at)}</span>
                </div>
                <div className="data-card-row">
                  <span className="data-card-label">Duration</span>
                  <span className="data-card-value">{r.duration_seconds ? fmtDuration(r.duration_seconds) : "—"}</span>
                </div>
                <div className="data-card-row">
                  <span className="data-card-label">Energy</span>
                  <span className="data-card-value">{fmtEnergy(r.energy_wh)}</span>
                </div>
                <div className="data-card-row">
                  <span className="data-card-label">Status</span>
                  <span className="data-card-value">
                    <span className={`badge ${r.ended_at ? "offline" : "online"}`}>
                      {r.ended_at ? "Closed" : "Open"}
                    </span>
                  </span>
                </div>
                {expandedId === r.id && <RecordDetail record={r} />}
              </div>
            ))}
          </div>

          {/* Pagination */}
          {totalPages > 1 && (
            <div className="db-inspector-pagination">
              <button
                className="button-small button-secondary"
                disabled={offset === 0}
                onClick={() => handlePageChange(Math.max(0, offset - PAGE_SIZE))}
              >
                <Icon name="chevron_left" size={16} />
                Prev
              </button>
              <span className="db-inspector-page-info">
                Page {currentPage} of {totalPages}
              </span>
              <button
                className="button-small button-secondary"
                disabled={offset + PAGE_SIZE >= data.total}
                onClick={() => handlePageChange(offset + PAGE_SIZE)}
              >
                Next
                <Icon name="chevron_right" size={16} />
              </button>
            </div>
          )}
        </>
      )}

      {data && (!data.records || data.records.length === 0) && !loading && (
        <div className="config-empty">No records match the current filters.</div>
      )}

      {loading && data && (
        <div className="db-inspector-loading-overlay">
          <div className="spinner-small" />
        </div>
      )}
    </div>
  );
}

function RecordDetail({ record }: { record?: DBRecordsResponse["records"][0] }) {
  if (!record) return null;

  return (
    <div className="db-inspector-detail" onClick={(e) => e.stopPropagation()}>
      <div className="db-inspector-detail-grid">
        <DetailItem label="Ended" value={record.ended_at ? new Date(record.ended_at).toLocaleString() : "—"} />
        <DetailItem label="Avg Power" value={record.avg_power_w ? `${record.avg_power_w.toFixed(0)} W` : "—"} />
        <DetailItem label="Peak Power" value={record.peak_power_w ? `${record.peak_power_w.toFixed(0)} W` : "—"} />
        <DetailItem label="SoC Start" value={record.soc_start ? `${record.soc_start.toFixed(1)}%` : "—"} />
        <DetailItem label="SoC End" value={record.soc_end ? `${record.soc_end.toFixed(1)}%` : "—"} />
        <DetailItem label="Avg Price" value={record.avg_price_eur_mwh ? `${record.avg_price_eur_mwh.toFixed(2)} EUR/MWh` : "—"} />
        <DetailItem label="Cost" value={fmtSignedCost(record.cost_eur)} />
        <DetailItem label="Samples" value={String(record.samples)} />
      </div>
    </div>
  );
}

function DetailItem({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="db-inspector-detail-item">
      <span className="db-inspector-detail-label">{label}</span>
      <span className={`db-inspector-detail-value${mono ? " db-inspector-cell-mono" : ""}`}>{value}</span>
    </div>
  );
}

function fmtBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function fmtEnergy(wh: number): string {
  if (wh <= 0) return "—";
  if (wh >= 1000) return `${(wh / 1000).toFixed(2)} kWh`;
  return `${wh.toFixed(0)} Wh`;
}

function fmtSignedCost(eur: number): string {
  if (eur === 0) return "—";
  const sign = eur > 0 ? "+" : "-";
  return `${sign}${Math.abs(eur).toFixed(4)} EUR`;
}

function fmtDateTime(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleString([], {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function fmtDuration(seconds: number): string {
  if (seconds < 60) return `${seconds.toFixed(0)}s`;
  if (seconds < 3600) return `${(seconds / 60).toFixed(0)}m`;
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  return `${h}h ${m}m`;
}

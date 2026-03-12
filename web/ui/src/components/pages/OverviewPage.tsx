import { useEffect, useState } from "react";
import { fetchDBStats } from "../../api";
import { Icon } from "../shared/Icon";
import type { AgentsMap, AppPage, DBStats, AgentDBStats } from "../../types";

interface OverviewPageProps {
  agents: AgentsMap;
  onNavigate: (page: AppPage) => void;
}

export function OverviewPage({ agents, onNavigate }: OverviewPageProps) {
  const [dbStats, setDbStats] = useState<DBStats | null>(null);
  const [agentStats, setAgentStats] = useState<AgentDBStats[]>([]);

  useEffect(() => {
    fetchDBStats()
      .then((data) => {
        setDbStats(data.stats);
        setAgentStats(data.agents);
      })
      .catch(() => {});
  }, []);

  const agentList = Object.values(agents);

  return (
    <div className="overview-page">
      <h2>Devices</h2>
      {agentList.length === 0 ? (
        <div className="empty-state">
          <h3>No agents connected</h3>
          <p>
            Once a gok-pi agent connects to the control server you will see it
            listed here. Ensure the server URL and shared secret are set in the
            agent configuration.
          </p>
        </div>
      ) : (
        <div className="overview-agents-grid">
          {agentList.map((agent) => (
            <button
              key={agent.agent.id}
              className="overview-agent-card card"
              onClick={() =>
                onNavigate({
                  page: "device",
                  agentId: agent.agent.id,
                  tab: "monitor",
                })
              }
            >
              <div className="agent-card-header">
                <strong>
                  {agent.device_name ||
                    agent.agent.hostname ||
                    agent.agent.id}
                </strong>
                <span
                  className={`badge ${agent.connected === false ? "offline" : "online"}`}
                >
                  {agent.connected === false ? "Offline" : "Online"}
                </span>
              </div>
              <div className="agent-card-meta">
                <span>{agent.agent.env}</span>
                {agent.connected === false && (
                  <span className="agent-last-seen">
                    Last seen{" "}
                    {(() => {
                      const d = new Date(agent.last_seen);
                      const isToday = d.toDateString() === new Date().toDateString();
                      return isToday
                        ? d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
                        : d.toLocaleDateString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
                    })()}
                  </span>
                )}
              </div>
              {agent.telemetry &&
                Object.keys(agent.telemetry).length > 0 && (
                  <div className="overview-batteries">
                    {Object.values(agent.telemetry).map((t) => (
                      <div key={t.name} className="overview-battery-mini">
                        <Icon name="battery_full" size={16} />
                        <span>{t.name}</span>
                        <span className="overview-battery-soc">
                          {t.usoc.toFixed(0)}%
                        </span>
                      </div>
                    ))}
                  </div>
                )}
              <small className="overview-agent-id">
                ID: {agent.agent.id}
              </small>
            </button>
          ))}
        </div>
      )}

      {dbStats && (
        <div className="overview-section">
          <h3>Session Database</h3>
          <div className="db-stats">
            <div className="db-stats-grid">
              <div className="db-stats-item">
                <span className="db-stats-label">Size</span>
                <span className="db-stats-value">
                  {fmtBytes(dbStats.file_size_bytes)}
                </span>
              </div>
              <div className="db-stats-item">
                <span className="db-stats-label">Sessions</span>
                <span className="db-stats-value">{dbStats.total_sessions}</span>
              </div>
              <div className="db-stats-item">
                <span className="db-stats-label">Active</span>
                <span className="db-stats-value">{dbStats.open_sessions}</span>
              </div>
              <div className="db-stats-item">
                <span className="db-stats-label">Data range</span>
                <span className="db-stats-value">
                  {dbStats.oldest_session
                    ? `${new Date(dbStats.oldest_session).toLocaleDateString()} — ${dbStats.newest_session ? new Date(dbStats.newest_session).toLocaleDateString() : "—"}`
                    : "—"}
                </span>
              </div>
            </div>
            {agentStats.length > 0 && (
              <div className="db-stats-agents">
                <table>
                  <thead>
                    <tr>
                      <th>Device</th>
                      <th>Sessions</th>
                      <th>Charge</th>
                      <th>Discharge</th>
                      <th>Energy</th>
                      <th>Net Result</th>
                    </tr>
                  </thead>
                  <tbody>
                    {agentStats.map((a) => (
                      <tr key={a.agent_id}>
                        <td>{a.agent_id}</td>
                        <td>{a.total_sessions}</td>
                        <td>{a.charge_sessions}</td>
                        <td>{a.discharge_sessions}</td>
                        <td>{fmtEnergyWh(a.total_energy_wh)}</td>
                        <td>
                          {a.net_cost_eur !== 0 ? (
                            <span style={{ color: a.net_cost_eur > 0 ? "var(--color-success)" : "var(--color-danger)" }}>
                              {a.net_cost_eur > 0 ? "+" : "-"}{Math.abs(a.net_cost_eur).toFixed(2)} EUR
                            </span>
                          ) : "—"}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

function fmtBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function fmtEnergyWh(wh: number): string {
  if (wh <= 0) return "—";
  if (wh >= 1000) return `${(wh / 1000).toFixed(1)} kWh`;
  return `${wh.toFixed(0)} Wh`;
}

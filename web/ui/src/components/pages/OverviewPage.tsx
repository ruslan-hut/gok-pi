import { useEffect, useState } from "react";
import { fetchDBStats } from "../../api";
import { fmtAgo, fmtBytes, fmtDate, fmtEnergy, fmtSignedEUR } from "../../lib/format";
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
        setAgentStats(data.agents ?? []);
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
                  className={`badge ${agent.connected === false ? "fault" : "ok"}`}
                >
                  {agent.connected === false ? "Offline" : "Online"}
                </span>
              </div>
              <div className="agent-card-meta">
                <span>{agent.agent.env}</span>
                {agent.connected === false && (
                  <span className="agent-last-seen">
                    Last seen {fmtAgo(agent.last_seen)}
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
                    ? `${fmtDate(dbStats.oldest_session)} — ${dbStats.newest_session ? fmtDate(dbStats.newest_session) : "—"}`
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
                      <th className="num">Sessions</th>
                      <th className="num">Charge</th>
                      <th className="num">Discharge</th>
                      <th className="num">Energy</th>
                      <th className="num">Net</th>
                    </tr>
                  </thead>
                  <tbody>
                    {agentStats.map((a) => (
                      <tr key={a.agent_id}>
                        <td>{a.agent_id}</td>
                        <td className="num">{a.total_sessions}</td>
                        <td className="num">{a.charge_sessions}</td>
                        <td className="num">{a.discharge_sessions}</td>
                        <td className="num">{fmtEnergy(a.total_energy_wh)}</td>
                        <td className="num">
                          {a.net_cost_eur !== 0 ? (
                            <span
                              className={
                                a.net_cost_eur < 0
                                  ? "amount-negative"
                                  : "amount-positive"
                              }
                            >
                              {fmtSignedEUR(a.net_cost_eur)}
                            </span>
                          ) : (
                            "—"
                          )}
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


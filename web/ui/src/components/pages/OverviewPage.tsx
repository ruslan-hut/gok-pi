import { useEffect, useMemo, useState } from "react";
import { fetchDBStats } from "../../api";
import { Icon } from "../shared/Icon";
import { boardTempLevel, boardTitle, fmtBoardTempShort } from "../../lib/board";
import { fmtAgo, fmtEnergy, fmtSignedEUR, fmtWatts, isLiveReading } from "../../lib/format";
import type {
  AgentsMap,
  AppPage,
  AgentDBStats,
  TelemetrySnapshot,
} from "../../types";

interface OverviewPageProps {
  agents: AgentsMap;
  onNavigate: (page: AppPage) => void;
}

interface MonthOption {
  label: string;
  from: string;
  to: string;
}

/** The current month and the 11 before it, newest first, on local-time month boundaries. */
function lastMonths(): MonthOption[] {
  const now = new Date();
  const out: MonthOption[] = [];
  for (let i = 0; i < 12; i++) {
    const start = new Date(now.getFullYear(), now.getMonth() - i, 1);
    const end = new Date(now.getFullYear(), now.getMonth() - i + 1, 1);
    out.push({
      label: start.toLocaleDateString(undefined, { month: "long", year: "numeric" }),
      from: start.toISOString(),
      to: end.toISOString(),
    });
  }
  return out;
}

export function OverviewPage({ agents, onNavigate }: OverviewPageProps) {
  const months = useMemo(lastMonths, []);
  const [monthIdx, setMonthIdx] = useState(0);
  const [agentStats, setAgentStats] = useState<AgentDBStats[]>([]);

  useEffect(() => {
    let stale = false;
    const m = months[monthIdx];
    fetchDBStats({ from: m.from, to: m.to })
      .then((data) => {
        if (!stale) setAgentStats(data.agents ?? []);
      })
      .catch(() => {
        if (!stale) setAgentStats([]);
      });
    return () => {
      stale = true;
    };
  }, [months, monthIdx]);

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
                <strong>{agentLabel(agents, agent.agent.id)}</strong>
                <span
                  className={`badge ${agent.connected === false ? "fault" : "ok"}`}
                >
                  {agent.connected === false ? "Offline" : "Online"}
                </span>
              </div>
              <div className="agent-card-meta">
                <span>{agent.agent.env}</span>
                {agent.connected !== false && agent.board?.temp_c !== undefined && (
                  <span className="board-health" title={boardTitle(agent.board)}>
                    {agent.board.throttled?.under_voltage && (
                      <span className="board-temp fault">
                        <Icon name="bolt" size={14} />
                        Under-voltage
                      </span>
                    )}
                    <span
                      className={`board-temp ${boardTempLevel(agent.board.temp_c)}`}
                    >
                      <Icon name="device_thermostat" size={14} />
                      {fmtBoardTempShort(agent.board.temp_c)}
                    </span>
                  </span>
                )}
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
                      <BatteryRow
                        key={t.name}
                        snapshot={t}
                        stale={agent.connected === false || !isLiveReading(t)}
                      />
                    ))}
                  </div>
                )}
              <small className="overview-agent-id">{agent.agent.id}</small>
            </button>
          ))}
        </div>
      )}

      <div className="overview-section">
        <div className="overview-section-head">
          <h3>Summary</h3>
          <select
            value={monthIdx}
            onChange={(e) => setMonthIdx(Number(e.target.value))}
            aria-label="Period"
          >
            {months.map((m, i) => (
              <option key={m.from} value={i}>
                {m.label}
                {i === 0 ? " (current)" : ""}
              </option>
            ))}
          </select>
        </div>
        <div className="db-stats">
          {agentStats.length === 0 ? (
            <p>No sessions in this period.</p>
          ) : (
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
                      <td title={a.agent_id}>{agentLabel(agents, a.agent_id)}</td>
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
    </div>
  );
}

function agentLabel(agents: AgentsMap, id: string): string {
  const agent = agents[id];
  return agent?.device_name || agent?.agent.hostname || id;
}

/**
 * BatteryRow answers the question the overview exists to answer: what is this
 * battery doing right now. Charge is a bar rather than a number so a nearly
 * empty battery stands out in a grid of them.
 */
function BatteryRow({
  snapshot,
  stale,
}: {
  snapshot: TelemetrySnapshot;
  stale: boolean;
}) {
  // An agent we have not heard from is reporting the last thing it said, not
  // what it is doing now, so its flow state is not claimed as current.
  const flow = stale
    ? { className: "stale", label: "No live data" }
    : snapshot.battery_discharging
      ? { className: "discharge", label: "Discharging" }
      : snapshot.battery_charging
        ? { className: "charge", label: "Charging" }
        : { className: "idle", label: "Idle" };
  const soc = Math.max(0, Math.min(100, snapshot.usoc));
  const power = stale ? 0 : Math.abs(snapshot.pac_total_w);

  return (
    <div className={`overview-battery${stale ? " overview-battery-stale" : ""}`}>
      <div className="overview-battery-top">
        <span className="overview-battery-name">{snapshot.name}</span>
        <span className="overview-battery-soc">{soc.toFixed(0)}%</span>
      </div>
      <div
        className="overview-soc-track"
        role="img"
        aria-label={`${soc.toFixed(0)} percent charged${stale ? ", last known" : ""}`}
      >
        <div
          className={`overview-soc-fill overview-soc-${flow.className}`}
          style={{ width: `${soc}%` }}
        />
      </div>
      <div className="overview-battery-state">
        <span className={`overview-flow overview-flow-${flow.className}`}>
          {flow.label}
        </span>
        {power > 0 && (
          <span className="overview-battery-power">{fmtWatts(power)}</span>
        )}
      </div>
    </div>
  );
}

import { fmtDateTime, fmtDuration } from "../../lib/format";
import type { AgentSummaryWithDeviceName } from "../../types";

/**
 * AgentStatusBadges reports three separate things that all used to look like
 * "the agent is fine":
 *
 *   Connected      — the socket is up (a heartbeat alone keeps this true)
 *   No telemetry   — the socket is up but nothing is arriving
 *   Backlog        — the agent is recording telemetry it cannot deliver
 *
 * The last two have different causes and different fixes, so they are separate
 * badges rather than one merged warning.
 */

/**
 * backlogWarnAfterSec matches the agent's own watchdog and the server's warning
 * threshold, so all three agree on what "not draining" means. Below it, a few
 * pending snapshots are just the normal flush interval.
 */
const backlogWarnAfterSec = 15 * 60;

interface AgentStatusBadgesProps {
  agent: AgentSummaryWithDeviceName;
  online: boolean;
}

export function AgentStatusBadges({ agent, online }: AgentStatusBadgesProps) {
  const backlogAge = agent.spool?.oldest_undelivered_age_sec ?? 0;
  const showBacklog = backlogAge >= backlogWarnAfterSec;

  return (
    <div className="agent-header-badges">
      <span className={`badge ${online ? "ok" : "fault"}`}>
        {online ? "Connected" : "Offline"}
      </span>

      {/* A connected agent whose telemetry stopped looks perfectly healthy from
          "Last contact" alone: the 30s heartbeat keeps that fresh. This badge is
          the only place the difference shows. */}
      {online && agent.telemetry_stalled && (
        <span
          className="badge warn"
          title={
            agent.last_telemetry_at
              ? `Last telemetry ${fmtDateTime(agent.last_telemetry_at)}`
              : "No telemetry received on this connection"
          }
        >
          No telemetry
        </span>
      )}

      {/* The age leads: it is what distinguishes a queue that is draining slowly
          from one that has stopped. The count and the underlying error are one
          hover away. */}
      {showBacklog && agent.spool && (
        <span className="badge warn" title={backlogTitle(agent.spool, backlogAge)}>
          Backlog {fmtCompactAge(backlogAge)}
        </span>
      )}
    </div>
  );
}

/**
 * fmtCompactAge trims the minutes off a whole number of hours. On a badge read at
 * a glance, "9h 0m" spends three characters saying nothing.
 */
function fmtCompactAge(seconds: number): string {
  const formatted = fmtDuration(seconds);
  return formatted.endsWith(" 0m") ? formatted.slice(0, -3) : formatted;
}

function backlogTitle(
  spool: NonNullable<AgentSummaryWithDeviceName["spool"]>,
  ageSec: number,
): string {
  const parts = [
    `${spool.pending.toLocaleString("en-GB")} snapshots waiting to be delivered`,
    `oldest recorded ${fmtDuration(ageSec)} ago`,
  ];
  if (spool.last_delivery_at) {
    parts.push(`last delivery ${fmtDateTime(spool.last_delivery_at)}`);
  }
  if (spool.flush_errors) {
    parts.push(`${spool.flush_errors} flush errors`);
  }
  if (spool.last_error) {
    parts.push(`last error: ${spool.last_error}`);
  }
  return parts.join(" · ");
}

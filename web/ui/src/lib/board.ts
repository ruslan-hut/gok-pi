import type { AgentBoardHealth } from "../types";

/**
 * Temperature bands for a Raspberry Pi SoC. The firmware starts throttling the
 * CPU at 80 °C, so that is the fault line; 70 °C leaves time to notice a
 * cabinet that is heating up before performance suffers.
 */
const boardTempWarnC = 70;
const boardTempFaultC = 80;

export type BoardLevel = "ok" | "warn" | "fault";

export function boardTempLevel(tempC: number): BoardLevel {
  if (tempC >= boardTempFaultC) return "fault";
  if (tempC >= boardTempWarnC) return "warn";
  return "ok";
}

export function fmtBoardTemp(tempC: number): string {
  return `${tempC.toFixed(1)} °C`;
}

/** fmtBoardTempShort rounds to whole degrees for glanceable places. */
export function fmtBoardTempShort(tempC: number): string {
  return `${Math.round(tempC)} °C`;
}

/**
 * boardTitle spells out the throttling flags for a tooltip. Conditions that are
 * active now come first; ones that have only happened since boot follow, as
 * they explain an earlier slowdown but need no action now.
 */
export function boardTitle(board: AgentBoardHealth): string {
  const parts: string[] = [];
  if (board.temp_c !== undefined) {
    parts.push(`SoC temperature ${fmtBoardTemp(board.temp_c)}`);
  }
  const t = board.throttled;
  if (t) {
    const now = activeConditions(board);
    const sinceBoot = sinceBootConditions(board);
    parts.push(now.length ? `now: ${now.join(", ")}` : "no throttling now");
    if (sinceBoot.length) {
      parts.push(`since boot: ${sinceBoot.join(", ")}`);
    }
  }
  return parts.join(" · ");
}

/** activeConditions lists the throttling flags that are set right now. */
export function activeConditions(board: AgentBoardHealth): string[] {
  const t = board.throttled;
  if (!t) return [];
  return [
    t.under_voltage && "under-voltage",
    t.throttled && "throttled",
    t.freq_capped && "frequency capped",
    t.soft_temp_limit && "soft temperature limit",
  ].filter((c): c is string => Boolean(c));
}

/**
 * pastConditions lists the conditions that happened since boot but are not
 * active now: the explanation for an earlier slowdown or reboot, not an alarm.
 */
export function pastConditions(board: AgentBoardHealth): string[] {
  const t = board.throttled;
  if (!t) return [];
  return [
    t.under_voltage_occurred && !t.under_voltage && "under-voltage",
    t.throttled_occurred && !t.throttled && "throttled",
  ].filter((c): c is string => Boolean(c));
}

function sinceBootConditions(board: AgentBoardHealth): string[] {
  const t = board.throttled;
  if (!t) return [];
  return [
    t.under_voltage_occurred && "under-voltage",
    t.throttled_occurred && "throttling",
    t.freq_capped_occurred && "frequency cap",
    t.soft_temp_limit_occurred && "soft temperature limit",
  ].filter((c): c is string => Boolean(c));
}

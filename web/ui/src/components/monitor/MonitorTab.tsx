import { useMemo } from "react";
import { BatteryCard } from "./BatteryCard";
import { SystemSummary } from "../layout/SystemSummary";
import type {
  AgentConfig,
  AgentSummaryWithDeviceName,
  BatteryConfig,
  TelemetrySnapshot,
} from "../../types";

interface MonitorTabProps {
  selectedAgent: AgentSummaryWithDeviceName | undefined;
  selectedAgentOnline: boolean;
  agentConfig: AgentConfig | null;
  onCommand: (command: string, target: string, payload?: unknown) => void;
  readonly?: boolean;
}

export function MonitorTab({
  selectedAgent,
  selectedAgentOnline,
  agentConfig,
  onCommand,
  readonly,
}: MonitorTabProps) {
  const batteries = useMemo(() => {
    if (!selectedAgent) return [];
    const configuredBatteryNames = new Set(
      (agentConfig?.batteries || []).map((b) => b.name),
    );
    return Object.values(selectedAgent.telemetry)
      .filter((snapshot: TelemetrySnapshot) =>
        configuredBatteryNames.has(snapshot.name),
      )
      .sort((a: TelemetrySnapshot, b: TelemetrySnapshot) =>
        a.name.localeCompare(b.name),
      );
  }, [selectedAgent, agentConfig]);

  const batteryConfigMap = useMemo(() => {
    const map = new Map<string, BatteryConfig>();
    (agentConfig?.batteries || []).forEach((battery) =>
      map.set(battery.name, battery),
    );
    return map;
  }, [agentConfig]);

  if (!selectedAgent) return null;

  return (
    <>
      <SystemSummary batteries={batteries} />
      {!selectedAgentOnline && (
        <div className="offline-warning">
          Agent is offline. Commands are disabled until it reconnects.
        </div>
      )}
      <section className="metrics-grid">
        {batteries.map((battery) => {
          const hasGoalReachedToday = (() => {
            if (
              !selectedAgent?.schedule_goal_reached ||
              !agentConfig?.schedules
            )
              return false;
            const today = new Date();
            today.setHours(0, 0, 0, 0);
            const batterySchedules = agentConfig.schedules.filter(
              (s) => s.battery_name === battery.name && s.run_once,
            );
            return batterySchedules.some((schedule) => {
              const goalTime =
                selectedAgent.schedule_goal_reached?.[schedule.name ?? ""];
              if (!goalTime) return false;
              const goalDate = new Date(goalTime);
              goalDate.setHours(0, 0, 0, 0);
              return goalDate.getTime() === today.getTime();
            });
          })();

          return (
            <BatteryCard
              key={battery.name}
              snapshot={battery}
              batteryConfig={batteryConfigMap.get(battery.name)}
              onCommand={onCommand}
              isOnline={selectedAgentOnline}
              hasGoalReachedToday={hasGoalReachedToday}
              readonly={readonly}
            />
          );
        })}
      </section>
    </>
  );
}

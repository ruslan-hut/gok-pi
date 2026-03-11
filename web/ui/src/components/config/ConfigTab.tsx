import { useEffect, useState } from "react";
import { DeviceSettings } from "./DeviceSettings";
import { BatteryConfigForm } from "./BatteryConfigForm";
import { ScheduleConfigForm } from "./ScheduleConfigForm";
import { ConfigActions } from "./ConfigActions";
import { Icon } from "../shared/Icon";
import type { AgentConfig, BatteryConfig, ScheduleConfig } from "../../types";

function formatConfigDraft(config: AgentConfig | null): string {
  const payload = {
    device_name: config?.device_name ?? "",
    env: config?.env ?? "",
    timezone: config?.timezone ?? "",
    revision: config?.revision ?? 0,
    batteries: config?.batteries ?? [],
    schedules: config?.schedules ?? [],
  };
  return JSON.stringify(payload, null, 2);
}

interface ConfigTabProps {
  config: AgentConfig | null;
  agentEnv?: string;
  agentId?: string;
  agentVersion?: string;
  draft: string;
  loading: boolean;
  saving: boolean;
  dirty: boolean;
  error?: string;
  onDraftChange: (value: string) => void;
  onSave: () => void;
  onReset: () => void;
  scheduleGoalReached?: Record<string, string>;
  onResetGoal?: (scheduleName: string) => void;
  isOnline?: boolean;
}

export function ConfigTab({
  config,
  agentEnv,
  agentId,
  agentVersion,
  draft,
  loading,
  saving,
  dirty,
  error,
  onDraftChange,
  onSave,
  onReset,
  scheduleGoalReached,
  onResetGoal,
  isOnline,
}: ConfigTabProps) {
  const [showJson, setShowJson] = useState(false);
  const [localConfig, setLocalConfig] = useState<AgentConfig | null>(null);

  // Parse draft into local config state
  useEffect(() => {
    if (draft) {
      try {
        const parsed = JSON.parse(draft) as Partial<AgentConfig>;
        setLocalConfig({
          device_name: parsed.device_name ?? config?.device_name ?? "",
          env: parsed.env ?? config?.env ?? agentEnv ?? "",
          timezone: parsed.timezone ?? config?.timezone ?? "",
          revision: parsed.revision ?? config?.revision ?? 0,
          updated_at: config?.updated_at ?? new Date().toISOString(),
          batteries: Array.isArray(parsed.batteries) ? parsed.batteries : [],
          schedules: Array.isArray(parsed.schedules) ? parsed.schedules : [],
        });
      } catch {
        // Invalid JSON, keep current state
      }
    } else {
      setLocalConfig(config);
    }
  }, [draft, config]);

  const handleConfigChange = (newConfig: AgentConfig) => {
    setLocalConfig(newConfig);
    onDraftChange(formatConfigDraft(newConfig));
  };

  const handleBatteryChange = (index: number, battery: BatteryConfig) => {
    if (!localConfig) return;
    const newBatteries = [...localConfig.batteries];
    newBatteries[index] = battery;
    handleConfigChange({ ...localConfig, batteries: newBatteries });
  };

  const handleBatteryAdd = () => {
    if (!localConfig) return;
    const newBattery: BatteryConfig = {
      name: "",
      url: "",
      token: "",
      enabled: true,
      capacity_limit: 0,
    };
    handleConfigChange({
      ...localConfig,
      batteries: [...localConfig.batteries, newBattery],
    });
  };

  const handleBatteryRemove = (index: number) => {
    if (!localConfig) return;
    const newBatteries = localConfig.batteries.filter((_, i) => i !== index);
    handleConfigChange({ ...localConfig, batteries: newBatteries });
  };

  const handleScheduleChange = (index: number, schedule: ScheduleConfig) => {
    if (!localConfig) return;
    const newSchedules = [...localConfig.schedules];
    newSchedules[index] = schedule;
    handleConfigChange({ ...localConfig, schedules: newSchedules });
  };

  const handleScheduleAdd = () => {
    if (!localConfig) return;
    const newSchedule: ScheduleConfig = {
      name: "",
      type: "discharge",
      start_time: "00:00",
      stop_time: "23:59",
      battery_name: "",
      enabled: true,
      power_limit: 0,
      soc_limit: 0,
      run_once: false,
    };
    handleConfigChange({
      ...localConfig,
      schedules: [...localConfig.schedules, newSchedule],
    });
  };

  const handleScheduleRemove = (index: number) => {
    if (!localConfig) return;
    const newSchedules = localConfig.schedules.filter((_, i) => i !== index);
    handleConfigChange({ ...localConfig, schedules: newSchedules });
  };

  if (loading) {
    return (
      <div className="config-loading">
        <div className="spinner"></div>
        <p>Loading configuration...</p>
      </div>
    );
  }

  return (
    <div className="config-tab">
      {(agentId || agentVersion) && (
        <div className="config-device-info">
          {agentId && (
            <span className="badge">Device ID: {agentId}</span>
          )}
          {agentVersion && (
            <span className="badge">Version {agentVersion}</span>
          )}
        </div>
      )}
      <p className="config-meta">
        {config
          ? `Last updated ${new Date(config.updated_at).toLocaleString()}`
          : "No remote configuration stored yet. Configure batteries and schedules below."}
      </p>

      {!showJson ? (
        <div className="config-forms">
          {localConfig && (
            <DeviceSettings
              config={localConfig}
              agentEnv={agentEnv}
              disabled={saving}
              onChange={handleConfigChange}
            />
          )}

          <div className="config-section">
            <div className="config-section-header">
              <h4>Batteries</h4>
              <button
                type="button"
                className="button-small"
                onClick={handleBatteryAdd}
                disabled={saving || !localConfig}
              >
                <Icon name="add" size={16} /> Add Battery
              </button>
            </div>
            {localConfig?.batteries.length === 0 ? (
              <p className="config-empty">
                No batteries configured. Click "Add Battery" to create one.
              </p>
            ) : (
              <div className="config-items">
                {localConfig?.batteries.map((battery, index) => (
                  <BatteryConfigForm
                    key={`battery-${index}-${battery.name || "new"}`}
                    battery={battery}
                    onChange={(b) => handleBatteryChange(index, b)}
                    onRemove={() => handleBatteryRemove(index)}
                    disabled={saving}
                  />
                ))}
              </div>
            )}
          </div>

          <div className="config-section">
            <div className="config-section-header">
              <h4>Schedules</h4>
              <button
                type="button"
                className="button-small"
                onClick={handleScheduleAdd}
                disabled={saving || !localConfig}
              >
                <Icon name="add" size={16} /> Add Schedule
              </button>
            </div>
            {localConfig?.schedules.length === 0 ? (
              <p className="config-empty">
                No schedules configured. Click "Add Schedule" to create one.
              </p>
            ) : (
              <div className="config-items">
                {localConfig?.schedules.map((schedule, index) => (
                  <ScheduleConfigForm
                    key={`schedule-${index}-${schedule.battery_name || "new"}`}
                    schedule={schedule}
                    batteryNames={
                      localConfig?.batteries.map((b) => b.name) || []
                    }
                    onChange={(s) => handleScheduleChange(index, s)}
                    onRemove={() => handleScheduleRemove(index)}
                    disabled={saving}
                    goalReachedAt={
                      schedule.name
                        ? scheduleGoalReached?.[schedule.name]
                        : undefined
                    }
                    onResetGoal={
                      schedule.name && onResetGoal
                        ? () => onResetGoal(schedule.name!)
                        : undefined
                    }
                    isOnline={isOnline}
                  />
                ))}
              </div>
            )}
          </div>

        </div>
      ) : (
        <textarea
          className="config-editor"
          value={draft}
          onChange={(event) => onDraftChange(event.target.value)}
          disabled={saving}
          spellCheck={false}
        />
      )}

      <div className="config-view-toggle">
        <button
          type="button"
          className="button-link"
          onClick={() => setShowJson(!showJson)}
          disabled={saving}
        >
          {showJson ? <><Icon name="arrow_back" size={16} /> Back to Forms</> : "Advanced: Edit JSON"}
        </button>
      </div>

      {error ? <div className="config-error">{error}</div> : null}
      <ConfigActions
        dirty={dirty}
        saving={saving}
        onSave={onSave}
        onReset={onReset}
      />
    </div>
  );
}


import { useCallback, useEffect, useMemo, useState } from "react";
import { DeviceSettings } from "./DeviceSettings";
import { BatteryConfigForm } from "./BatteryConfigForm";
import { ScheduleConfigForm } from "./ScheduleConfigForm";
import { EmailReportsForm } from "./EmailReportsForm";
import { ChargerLinksForm } from "./ChargerLinksForm";
import { ConfigActions } from "./ConfigActions";
import { ConfigSectionNav } from "./ConfigSectionNav";
import type { ConfigSection, ConfigSectionItem } from "./ConfigSectionNav";
import { CopyButton } from "../shared/CopyButton";
import { Icon } from "../shared/Icon";
import { formatConfigDraft } from "../../hooks/useConfig";
import { fmtDateTime } from "../../lib/format";
import type { AgentConfig, BatteryConfig, EmailReportsConfig, ScheduleConfig } from "../../types";

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
  readonly?: boolean;
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
  readonly,
}: ConfigTabProps) {
  const [section, setSection] = useState<ConfigSection>("batteries");
  const [localConfig, setLocalConfig] = useState<AgentConfig | null>(null);
  // Null until the charger form has loaded and reported. The rail must not
  // claim "none" for data it has not seen yet.
  const [chargerSummary, setChargerSummary] = useState<{
    links: number;
    active: number;
  } | null>(null);
  const getDraftText = useCallback(() => draft, [draft]);

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
          email_reports: parsed.email_reports ?? config?.email_reports ?? null,
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

  const handleEmailReportsChange = (next: EmailReportsConfig | null) => {
    if (!localConfig) return;
    handleConfigChange({ ...localConfig, email_reports: next });
  };

  /**
   * Which sections differ from what is stored. The draft is the source of
   * truth for what a save would send, so it is compared against the saved
   * config key by key rather than inferring from edit events.
   */
  const dirtyBySection = useMemo(() => {
    const empty = { device: false, batteries: false, schedules: false, email: false };
    if (!dirty) return empty;
    let current: Partial<AgentConfig>;
    let saved: Partial<AgentConfig>;
    try {
      current = JSON.parse(draft || "{}");
      saved = JSON.parse(formatConfigDraft(config));
    } catch {
      // Unparseable draft: flag everything rather than claim it is clean.
      return { device: true, batteries: true, schedules: true, email: true };
    }
    const differs = (key: keyof AgentConfig) =>
      JSON.stringify(current[key] ?? null) !== JSON.stringify(saved[key] ?? null);
    return {
      device:
        differs("device_name") || differs("env") || differs("timezone"),
      batteries: differs("batteries"),
      schedules: differs("schedules"),
      email: differs("email_reports"),
    };
  }, [dirty, draft, config]);

  const sectionItems: ConfigSectionItem[] = useMemo(() => {
    const batteries = localConfig?.batteries.length ?? 0;
    const schedules = localConfig?.schedules.length ?? 0;
    const enabledSchedules =
      localConfig?.schedules.filter((s) => s.enabled).length ?? 0;
    const email = localConfig?.email_reports;
    const items: ConfigSectionItem[] = [
      {
        id: "batteries",
        label: "Batteries",
        icon: "battery_full",
        meta: batteries === 0 ? "None yet" : `${batteries} configured`,
        dirty: dirtyBySection.batteries,
      },
      {
        id: "schedules",
        label: "Schedules",
        icon: "schedule",
        meta:
          schedules === 0
            ? "None yet"
            : `${enabledSchedules} of ${schedules} enabled`,
        dirty: dirtyBySection.schedules,
      },
      {
        id: "chargers",
        label: "EV chargers",
        icon: "ev_station",
        meta: !chargerSummary
          ? undefined
          : chargerSummary.active > 0
            ? `${chargerSummary.links} linked · ${chargerSummary.active} active`
            : chargerSummary.links === 0
              ? "None yet"
              : `${chargerSummary.links} linked`,
      },
      {
        id: "email",
        label: "Email reports",
        icon: "mail",
        meta: email?.enabled
          ? `On · ${email.recipients.length} recipient${email.recipients.length === 1 ? "" : "s"}`
          : "Off",
        dirty: dirtyBySection.email,
      },
      {
        id: "device",
        label: "Device",
        icon: "settings",
        meta: localConfig?.timezone || "No timezone set",
        dirty: dirtyBySection.device,
      },
    ];
    if (!readonly) {
      items.push({
        id: "advanced",
        label: "Advanced",
        icon: "code",
        meta: "Raw JSON",
        dirty: dirty,
      });
    }
    return items;
  }, [localConfig, dirtyBySection, chargerSummary, readonly, dirty]);

  if (loading) {
    return (
      <div className="config-loading">
        <div className="spinner"></div>
        <p>Loading configuration...</p>
      </div>
    );
  }

  const activeItem = sectionItems.find((i) => i.id === section) ?? sectionItems[0];
  // Charger links persist through their own endpoint, so the shared draft
  // Save/Reset bar would be misleading on that section.
  const showDraftActions = !readonly && section !== "chargers";

  return (
    <div className="config-tab">
      <header className="config-header">
        <div className="config-device-info">
          {agentId && <span className="badge">Device ID: {agentId}</span>}
          {agentVersion && <span className="badge">Version {agentVersion}</span>}
        </div>
        <p className="config-meta">
          {config
            ? `Last updated ${fmtDateTime(config.updated_at)}`
            : "No remote configuration stored yet. Start with Batteries."}
        </p>
      </header>

      <div className="config-layout">
        <ConfigSectionNav
          items={sectionItems}
          active={section}
          onSelect={setSection}
        />

        <div
          className="config-panel-body"
          id={`config-panel-${section}`}
          role="tabpanel"
          aria-labelledby={`config-section-${section}`}
        >
          <div className="config-panel-heading">
            <h4>{activeItem?.label}</h4>
            {section === "batteries" && !readonly && (
              <button
                type="button"
                className="button-small"
                onClick={handleBatteryAdd}
                disabled={saving || !localConfig}
              >
                <Icon name="add" size={16} /> Add battery
              </button>
            )}
            {section === "schedules" && !readonly && (
              <button
                type="button"
                className="button-small"
                onClick={handleScheduleAdd}
                disabled={saving || !localConfig}
              >
                <Icon name="add" size={16} /> Add schedule
              </button>
            )}
          </div>

          {section === "batteries" && (
            <>
              <p className="config-section-intro">
                Each battery the agent polls and controls. Names are referenced
                by schedules and charger links.
              </p>
              {localConfig?.batteries.length === 0 ? (
                <p className="config-empty">
                  No batteries yet. Add one to start controlling it.
                </p>
              ) : (
                <div className="config-items">
                  {localConfig?.batteries.map((battery, index) => (
                    <BatteryConfigForm
                      key={`battery-${index}-${battery.name || "new"}`}
                      index={index}
                      battery={battery}
                      onChange={(b) => handleBatteryChange(index, b)}
                      onRemove={() => handleBatteryRemove(index)}
                      disabled={saving || readonly}
                      readonly={readonly}
                    />
                  ))}
                </div>
              )}
            </>
          )}

          {section === "schedules" && (
            <>
              <p className="config-section-intro">
                Time windows that charge or discharge a battery. Times are read
                in the device timezone{localConfig?.timezone ? ` (${localConfig.timezone})` : ""}.
              </p>
              {localConfig?.schedules.length === 0 ? (
                <p className="config-empty">
                  No schedules yet. Add one to run charge or discharge windows.
                </p>
              ) : (
                <div className="config-items">
                  {localConfig?.schedules.map((schedule, index) => (
                    <ScheduleConfigForm
                      key={`schedule-${index}-${schedule.battery_name || "new"}`}
                      index={index}
                      schedule={schedule}
                      batteryNames={
                        localConfig?.batteries.map((b) => b.name) || []
                      }
                      onChange={(s) => handleScheduleChange(index, s)}
                      onRemove={() => handleScheduleRemove(index)}
                      disabled={saving || readonly}
                      readonly={readonly}
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
            </>
          )}

          {section === "chargers" && localConfig && (
            <ChargerLinksForm
              agentId={agentId}
              batteryNames={localConfig.batteries.map((b) => b.name).filter(Boolean)}
              disabled={saving}
              readonly={readonly}
              onSummaryChange={setChargerSummary}
            />
          )}

          {section === "email" && localConfig && (
            <EmailReportsForm
              value={localConfig.email_reports ?? null}
              agentId={agentId}
              disabled={saving}
              readonly={readonly}
              onChange={handleEmailReportsChange}
            />
          )}

          {section === "device" && localConfig && (
            <DeviceSettings
              config={localConfig}
              agentEnv={agentEnv}
              disabled={saving || !!readonly}
              onChange={handleConfigChange}
            />
          )}

          {section === "advanced" && (
            <>
              <p className="config-section-intro">
                The whole configuration as it will be sent. Edits here feed the
                same Save as the other sections.
              </p>
              <div className="text-content-wrapper">
                <CopyButton getText={getDraftText} />
                <textarea
                  className="config-editor"
                  value={draft}
                  onChange={(event) => onDraftChange(event.target.value)}
                  disabled={saving}
                  spellCheck={false}
                  aria-label="Raw configuration JSON"
                />
              </div>
            </>
          )}

          {error ? <div className="config-error">{error}</div> : null}

          {showDraftActions && (
            <ConfigActions
              dirty={dirty}
              saving={saving}
              onSave={onSave}
              onReset={onReset}
            />
          )}
        </div>
      </div>
    </div>
  );
}

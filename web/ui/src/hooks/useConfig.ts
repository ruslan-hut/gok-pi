import { useCallback, useEffect, useRef, useState } from "react";
import { fetchAgentConfig, updateAgentConfig } from "../api";
import type { AgentConfig } from "../types";

export function formatConfigDraft(config: AgentConfig | null): string {
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

interface UseConfigOptions {
  agentId: string | undefined;
  onDeviceNameUpdate: (agentId: string, name: string) => void;
  onMessage: (msg: string) => void;
}

export function useConfig({
  agentId,
  onDeviceNameUpdate,
  onMessage,
}: UseConfigOptions) {
  const [agentConfig, setAgentConfig] = useState<AgentConfig | null>(null);
  const [configDraft, setConfigDraft] = useState("");
  const [configDirty, setConfigDirty] = useState(false);
  const [configLoading, setConfigLoading] = useState(false);
  const [configSaving, setConfigSaving] = useState(false);
  const [configError, setConfigError] = useState<string>();

  const configDirtyRef = useRef(false);
  const onDeviceNameUpdateRef = useRef(onDeviceNameUpdate);
  const onMessageRef = useRef(onMessage);

  useEffect(() => {
    configDirtyRef.current = configDirty;
  }, [configDirty]);
  useEffect(() => {
    onDeviceNameUpdateRef.current = onDeviceNameUpdate;
  }, [onDeviceNameUpdate]);
  useEffect(() => {
    onMessageRef.current = onMessage;
  }, [onMessage]);

  // Fetch config when agent changes
  useEffect(() => {
    if (!agentId) {
      setAgentConfig(null);
      setConfigDraft("");
      setConfigDirty(false);
      setConfigError(undefined);
      return;
    }

    let cancelled = false;
    setConfigLoading(true);

    fetchAgentConfig(agentId)
      .then((cfg) => {
        if (cancelled) return;
        setAgentConfig(cfg);
        setConfigDraft(formatConfigDraft(cfg));
        setConfigDirty(false);
        setConfigError(undefined);
        if (cfg) {
          onDeviceNameUpdateRef.current(agentId, cfg.device_name ?? "");
        }
      })
      .catch((err) => {
        if (cancelled) return;
        setAgentConfig(null);
        setConfigDraft(formatConfigDraft(null));
        setConfigDirty(false);
        setConfigError(
          err instanceof Error ? err.message : "Failed to load configuration",
        );
      })
      .finally(() => {
        if (!cancelled) setConfigLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [agentId]);

  const handleRemoteConfigUpdate = useCallback(
    (updateAgentId: string, config: AgentConfig) => {
      if (updateAgentId !== agentId) return;
      setAgentConfig(config);
      setConfigError(undefined);
      if (configDirtyRef.current) {
        onMessageRef.current(
          "Remote configuration changed while editing; draft unchanged.",
        );
      } else {
        setConfigDraft(formatConfigDraft(config));
        setConfigDirty(false);
      }
    },
    [agentId],
  );

  const handleConfigSave = useCallback(async () => {
    if (!agentId) return;
    try {
      setConfigSaving(true);
      setConfigError(undefined);
      const parsed = JSON.parse(configDraft) as Partial<AgentConfig>;
      const revision =
        typeof parsed.revision === "number"
          ? parsed.revision
          : agentConfig?.revision ?? 0;
      const device_name =
        typeof parsed.device_name === "string"
          ? parsed.device_name
          : agentConfig?.device_name ?? "";
      const env =
        typeof parsed.env === "string" ? parsed.env : agentConfig?.env ?? "";
      const timezone =
        typeof parsed.timezone === "string"
          ? parsed.timezone
          : agentConfig?.timezone ?? "";
      const batteries = Array.isArray(parsed.batteries)
        ? parsed.batteries
        : [];
      const schedules = Array.isArray(parsed.schedules)
        ? parsed.schedules
        : [];

      const updated = await updateAgentConfig(agentId, {
        device_name,
        env,
        timezone,
        revision,
        batteries,
        schedules,
      });
      setAgentConfig(updated);
      setConfigDraft(formatConfigDraft(updated));
      setConfigDirty(false);
      onDeviceNameUpdateRef.current(agentId, updated.device_name ?? "");
      onMessageRef.current("Configuration saved");
    } catch (err) {
      if (err instanceof SyntaxError) {
        setConfigError("Configuration JSON is invalid");
      } else if (err instanceof Error) {
        setConfigError(err.message);
      } else {
        setConfigError("Failed to update configuration");
      }
    } finally {
      setConfigSaving(false);
    }
  }, [agentId, configDraft, agentConfig]);

  const handleConfigReset = useCallback(() => {
    setConfigDraft(formatConfigDraft(agentConfig));
    setConfigDirty(false);
    setConfigError(undefined);
  }, [agentConfig]);

  const handleDraftChange = useCallback((value: string) => {
    setConfigDraft(value);
    setConfigDirty(true);
  }, []);

  return {
    agentConfig,
    configDraft,
    configDirty,
    configLoading,
    configSaving,
    configError,
    handleDraftChange,
    handleConfigSave,
    handleConfigReset,
    handleRemoteConfigUpdate,
  };
}

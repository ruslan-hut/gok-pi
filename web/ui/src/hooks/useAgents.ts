import { useCallback, useEffect, useRef, useState } from "react";
import { fetchAgentConfig, fetchAgents } from "../api";
import type {
  AgentConfig,
  AgentSummary,
  AgentsMap,
  DashboardMessage,
  TelemetrySnapshot,
} from "../types";

const OFFLINE_GRACE_MS = 2 * 60 * 1000;

function computeConnectionStatus(agent: AgentSummary): boolean {
  const lastSeen = new Date(agent.last_seen).getTime();
  if (Number.isNaN(lastSeen)) return false;
  return Date.now() - lastSeen <= OFFLINE_GRACE_MS;
}

function getWsUrl(): string {
  const protocol = window.location.protocol === "https:" ? "wss" : "ws";
  const host = window.location.host;
  return `${protocol}://${host}/api/ui`;
}

interface UseAgentsOptions {
  onConfigUpdate?: (agentId: string, config: AgentConfig) => void;
}

export function useAgents({
  onConfigUpdate,
}: UseAgentsOptions) {
  const [agents, setAgents] = useState<AgentsMap>({});
  const [selectedAgentId, setSelectedAgentId] = useState<string>();
  const [connectionActive, setConnectionActive] = useState(false);
  const [message, setMessage] = useState<string>();
  const [lastStatusMessage, setLastStatusMessage] = useState("");
  const [statusMessageFrozen, setStatusMessageFrozen] = useState(false);

  const onConfigUpdateRef = useRef(onConfigUpdate);
  const prefetchedConfigAgentsRef = useRef<Set<string>>(new Set());
  const selectedAgentIdRef = useRef<string | undefined>();
  const statusMessageFrozenRef = useRef(false);

  // Keep refs in sync
  useEffect(() => {
    onConfigUpdateRef.current = onConfigUpdate;
  }, [onConfigUpdate]);
  useEffect(() => {
    selectedAgentIdRef.current = selectedAgentId;
    if (!statusMessageFrozenRef.current) {
      setLastStatusMessage("");
    }
  }, [selectedAgentId]);
  useEffect(() => {
    statusMessageFrozenRef.current = statusMessageFrozen;
  }, [statusMessageFrozen]);

  const updateDeviceName = useCallback(
    (agentId: string, name: string) => {
      setAgents((prev) => {
        const agent = prev[agentId];
        if (!agent) return prev;
        return { ...prev, [agentId]: { ...agent, device_name: name } };
      });
    },
    [],
  );

  // Prefetch agent config for device names
  const prefetchAgentConfig = useCallback(
    (agentId: string) => {
      if (prefetchedConfigAgentsRef.current.has(agentId)) return;
      prefetchedConfigAgentsRef.current.add(agentId);

      fetchAgentConfig(agentId)
        .then((cfg) => {
          if (!cfg) return;
          setAgents((prev) => {
            const agent = prev[agentId];
            if (!agent) return prev;
            return {
              ...prev,
              [agentId]: { ...agent, device_name: cfg.device_name },
            };
          });
        })
        .catch(() => {
          // Best-effort
        });
    },
    [],
  );

  // Message handlers
  const updateAgent = useCallback(
    (agent: AgentSummary) => {
      setAgents((prev) => {
        const existing = prev[agent.agent.id];
        return {
          ...prev,
          [agent.agent.id]: {
            ...agent,
            connected: computeConnectionStatus(agent),
            device_name: existing?.device_name,
          },
        };
      });
      prefetchAgentConfig(agent.agent.id);
    },
    [prefetchAgentConfig],
  );

  const updateTelemetry = useCallback(
    (agentId: string, snapshot: TelemetrySnapshot) => {
      setAgents((prev) => {
        const current = prev[agentId];
        if (!current) return prev;
        return {
          ...prev,
          [agentId]: {
            ...current,
            telemetry: { ...current.telemetry, [snapshot.name]: snapshot },
          },
        };
      });
    },
    [],
  );

  const removeAgent = useCallback((agentId: string) => {
    let wasConnected = false;
    setAgents((prev) => {
      const next = { ...prev };
      const current = next[agentId];
      if (!current) return prev;
      wasConnected = current.connected !== false;
      next[agentId] = { ...current, connected: false };
      return next;
    });
    if (wasConnected) {
      setMessage(`Agent ${agentId} disconnected`);
    }
  }, []);

  const handleMessage = useCallback(
    (msg: DashboardMessage) => {
      switch (msg.type) {
        case "agents.snapshot": {
          setAgents((prev) => {
            const next: AgentsMap = { ...prev };
            const seen = new Set<string>();
            msg.agents.forEach((agent) => {
              seen.add(agent.agent.id);
              const existing = prev[agent.agent.id];
              next[agent.agent.id] = {
                ...(existing ?? agent),
                ...agent,
                connected: computeConnectionStatus(agent),
                device_name: existing?.device_name,
              };
            });
            Object.keys(next).forEach((id) => {
              if (!seen.has(id)) next[id] = { ...next[id], connected: false };
            });
            return next;
          });
          msg.agents.forEach((agent) => prefetchAgentConfig(agent.agent.id));
          setSelectedAgentId((current) => {
            if (!current && msg.agents.length > 0)
              return msg.agents[0].agent.id;
            return current;
          });
          break;
        }
        case "agent.summary":
          updateAgent(msg.agent);
          break;
        case "agent.telemetry":
          updateTelemetry(msg.agent_id, msg.snapshot);
          break;
        case "agent.removed":
          removeAgent(msg.agent_id);
          break;
        case "config.updated":
          setAgents((prev) => {
            const agent = prev[msg.agent_id];
            if (agent) {
              return {
                ...prev,
                [msg.agent_id]: {
                  ...agent,
                  device_name: msg.config.device_name,
                },
              };
            }
            return prev;
          });
          onConfigUpdateRef.current?.(msg.agent_id, msg.config);
          break;
      }
    },
    [updateAgent, updateTelemetry, removeAgent, prefetchAgentConfig],
  );

  // Fetch agents on mount
  useEffect(() => {
    let cancelled = false;

    fetchAgents()
      .then((data) => {
        if (cancelled) return;
        setAgents((prev) => {
          const next: AgentsMap = { ...prev };
          Object.entries(data).forEach(([id, agent]) => {
            next[id] = {
              ...(prev[id] ?? agent),
              ...agent,
              connected: computeConnectionStatus(agent),
            };
          });
          return next;
        });
        setSelectedAgentId((current) => {
          if (!current) {
            const firstAgent = Object.values(data)[0];
            return firstAgent?.agent.id;
          }
          return current;
        });
      })
      .catch((err) => {
        if (cancelled) return;
        setMessage(err.message);
      });

    return () => {
      cancelled = true;
    };
  }, []);

  // WebSocket
  useEffect(() => {
    let isActive = true;
    let retryMs = 1000;
    let socket: WebSocket | null = null;

    const connect = () => {
      if (!isActive) return;
      socket = new WebSocket(getWsUrl());
      socket.onopen = () => {
        setConnectionActive(true);
        setMessage(undefined);
        retryMs = 1000;
      };
      socket.onmessage = (event) => {
        const data = JSON.parse(event.data) as DashboardMessage;
        const currentAgentId = selectedAgentIdRef.current;

        let isForSelectedAgent = false;
        switch (data.type) {
          case "agent.telemetry":
          case "agent.removed":
          case "config.updated":
            isForSelectedAgent = data.agent_id === currentAgentId;
            break;
          case "agent.summary":
            isForSelectedAgent = data.agent.agent.id === currentAgentId;
            break;
        }

        if (isForSelectedAgent && !statusMessageFrozenRef.current) {
          setLastStatusMessage(event.data);
        }

        handleMessage(data);
      };
      socket.onclose = () => {
        setConnectionActive(false);
        if (!isActive) return;
        setTimeout(() => {
          retryMs = Math.min(retryMs * 2, 8000);
          connect();
        }, retryMs);
      };
      socket.onerror = () => {
        setConnectionActive(false);
      };
    };

    connect();
    return () => {
      isActive = false;
      socket?.close();
    };
  }, [handleMessage]);

  // Connection status polling
  useEffect(() => {
    const interval = window.setInterval(() => {
      setAgents((prev) => {
        let changed = false;
        const next: AgentsMap = {};
        for (const [id, agent] of Object.entries(prev)) {
          const connected =
            agent.connected !== false && computeConnectionStatus(agent);
          if (connected !== agent.connected) changed = true;
          next[id] = { ...agent, connected };
        }
        return changed ? next : prev;
      });
    }, 30 * 1000);
    return () => window.clearInterval(interval);
  }, []);

  const selectedAgent = selectedAgentId ? agents[selectedAgentId] : undefined;
  const selectedAgentOnline = selectedAgent
    ? selectedAgent.connected !== false
    : false;

  return {
    agents,
    selectedAgentId,
    setSelectedAgentId,
    selectedAgent,
    selectedAgentOnline,
    connectionActive,
    message,
    setMessage,
    lastStatusMessage,
    statusMessageFrozen,
    setStatusMessageFrozen,
    updateDeviceName,
  };
}

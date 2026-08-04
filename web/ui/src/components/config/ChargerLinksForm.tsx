import { useCallback, useEffect, useState } from "react";
import { Icon } from "../shared/Icon";
import { fetchChargerLinks, fetchChargerSessions, saveChargerLinks } from "../../api";
import type { ChargerLink, ChargerSession } from "../../types";

interface ChargerLinksFormProps {
  agentId?: string;
  batteryNames: string[];
  disabled?: boolean;
  readonly?: boolean;
}

function newLink(agentId: string, batteryName: string): ChargerLink {
  return {
    name: "",
    enabled: true,
    location_id: "",
    charge_point_ids: [],
    agent_id: agentId,
    battery_name: batteryName,
    power_limit: 3000,
    soc_limit: 20,
    max_duration_min: 240,
  };
}

function formatStarted(iso: string): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return iso;
  return at.toLocaleString([], {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/**
 * ChargerLinksForm edits the links between evsys EV charging locations and this
 * agent's batteries.
 *
 * Links are server-global rather than part of the agent config, so this form
 * owns its own load/save cycle against /api/charger-links instead of feeding the
 * config draft. Only this agent's links are shown, and the others are carried
 * through untouched on save — the endpoint replaces the whole set.
 */
export function ChargerLinksForm({
  agentId,
  batteryNames,
  disabled,
  readonly,
}: ChargerLinksFormProps) {
  const [allLinks, setAllLinks] = useState<ChargerLink[]>([]);
  const [links, setLinks] = useState<ChargerLink[]>([]);
  const [sessions, setSessions] = useState<ChargerSession[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [error, setError] = useState<string>("");
  const [message, setMessage] = useState<string>("");
  // Raw text of the charge point IDs field being edited. Parsing on every
  // keystroke would swallow the separators as they are typed, so the field
  // shows what was typed until it loses focus.
  const [pointsDraft, setPointsDraft] = useState<{ index: number; text: string } | null>(null);

  const load = useCallback(async () => {
    if (!agentId) return;
    setLoading(true);
    try {
      const [fetched, active] = await Promise.all([
        fetchChargerLinks(),
        fetchChargerSessions().catch(() => [] as ChargerSession[]),
      ]);
      setAllLinks(fetched);
      setLinks(fetched.filter((l) => l.agent_id === agentId));
      setSessions(active.filter((s) => s.agent_id === agentId));
      setDirty(false);
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load charger links");
    } finally {
      setLoading(false);
    }
  }, [agentId]);

  useEffect(() => {
    void load();
  }, [load]);

  const update = (index: number, patch: Partial<ChargerLink>) => {
    setLinks((current) =>
      current.map((link, i) => (i === index ? { ...link, ...patch } : link)),
    );
    setDirty(true);
    setMessage("");
  };

  const add = () => {
    if (!agentId) return;
    setLinks((current) => [...current, newLink(agentId, batteryNames[0] ?? "")]);
    setDirty(true);
    setMessage("");
  };

  const remove = (index: number) => {
    setLinks((current) => current.filter((_, i) => i !== index));
    setDirty(true);
    setMessage("");
  };

  const save = async () => {
    if (!agentId) return;
    setSaving(true);
    setError("");
    setMessage("");
    try {
      // Other agents' links are untouched: the endpoint replaces the whole set.
      const others = allLinks.filter((l) => l.agent_id !== agentId);
      const stored = await saveChargerLinks([...others, ...links]);
      setAllLinks(stored);
      setLinks(stored.filter((l) => l.agent_id === agentId));
      setDirty(false);
      setMessage("Charger links saved.");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save charger links");
    } finally {
      setSaving(false);
    }
  };

  if (!agentId) return null;

  const busy = disabled || saving || loading;

  return (
    <div className="config-section">
      <div className="config-section-header">
        <h4>EV charger links</h4>
        {!readonly && (
          <button
            type="button"
            className="button-small"
            onClick={add}
            disabled={busy}
          >
            <Icon name="add" size={16} /> Add Link
          </button>
        )}
      </div>

      <small className="form-help-text">
        When a customer starts a charging session at a linked location, this
        battery discharges so the car draws stored energy instead of grid power.
        The discharge overrides any running schedule until the session ends.
      </small>

      {sessions.length > 0 && (
        <div className="config-item-goal-badge">
          <span className="goal-badge-text">
            <strong className="charger-session-heading">
              <Icon name="ev_station" size={16} />
              {sessions.length} active charging session
              {sessions.length > 1 ? "s" : ""}
            </strong>
            {sessions.map((s, i) => (
              <span key={`${s.charge_point_id}-${i}`} className="charger-session-row">
                {s.battery_name} is discharging for {s.charge_point_id}, since{" "}
                {formatStarted(s.started_at)}
              </span>
            ))}
          </span>
        </div>
      )}

      {loading ? (
        <p className="config-empty">Loading charger links…</p>
      ) : links.length === 0 ? (
        <p className="config-empty">
          No charger links for this agent. Click "Add Link" to connect an evsys
          location to a battery.
        </p>
      ) : (
        <div className="config-items">
          {links.map((link, index) => (
            <div className="config-item" key={`charger-link-${index}`}>
              <div className="config-item-header">
                <h5>{link.name || "Unnamed Link"}</h5>
                <div className="config-item-header-actions">
                  <label className="switch">
                    <input
                      type="checkbox"
                      checked={link.enabled}
                      onChange={(e) => update(index, { enabled: e.target.checked })}
                      disabled={busy || readonly}
                    />
                    <span className="switch-slider"></span>
                    <span className="switch-label">Enabled</span>
                  </label>
                </div>
              </div>

              <div className="config-form-grid">
                <div className="form-field">
                  <label htmlFor={`charger-name-${index}`}>Link name *</label>
                  <input
                    id={`charger-name-${index}`}
                    type="text"
                    value={link.name}
                    onChange={(e) => update(index, { name: e.target.value })}
                    disabled={busy || readonly}
                    placeholder="Office car park"
                  />
                </div>

                <div className="form-field">
                  <label htmlFor={`charger-battery-${index}`}>Battery *</label>
                  <select
                    id={`charger-battery-${index}`}
                    value={link.battery_name}
                    onChange={(e) => update(index, { battery_name: e.target.value })}
                    disabled={busy || readonly}
                  >
                    <option value="">Select a battery</option>
                    {batteryNames.map((name) => (
                      <option key={name} value={name}>
                        {name}
                      </option>
                    ))}
                    {/* A renamed or deleted battery would otherwise blank the
                        select and hide that the link points nowhere. */}
                    {link.battery_name &&
                      !batteryNames.includes(link.battery_name) && (
                        <option value={link.battery_name}>
                          {link.battery_name} — no longer configured
                        </option>
                      )}
                  </select>
                </div>

                <div className="form-field">
                  <label htmlFor={`charger-location-${index}`}>
                    evsys location ID
                  </label>
                  <input
                    id={`charger-location-${index}`}
                    type="text"
                    value={link.location_id}
                    onChange={(e) => update(index, { location_id: e.target.value })}
                    disabled={busy || readonly}
                    placeholder="loc-01"
                  />
                  <small className="form-help-text">
                    Matches every charger at that location.
                  </small>
                </div>

                <div className="form-field">
                  <label htmlFor={`charger-points-${index}`}>
                    Charge point IDs
                  </label>
                  <input
                    id={`charger-points-${index}`}
                    type="text"
                    value={
                      pointsDraft?.index === index
                        ? pointsDraft.text
                        : link.charge_point_ids.join(", ")
                    }
                    onChange={(e) =>
                      setPointsDraft({ index, text: e.target.value })
                    }
                    onBlur={(e) => {
                      setPointsDraft(null);
                      const parsed = e.target.value
                        .split(",")
                        .map((s) => s.trim())
                        .filter(Boolean);
                      // Leaving the field untouched must not mark the form dirty.
                      if (parsed.join(" ") === link.charge_point_ids.join(" ")) {
                        return;
                      }
                      update(index, { charge_point_ids: parsed });
                    }}
                    disabled={busy || readonly}
                    placeholder="Wallbox3, Wallbox4"
                  />
                  <small className="form-help-text">
                    Comma-separated fallback. Required for OCPP 2.0.1 chargers,
                    whose events carry no location.
                  </small>
                </div>

                <div className="form-field">
                  <label htmlFor={`charger-power-${index}`}>
                    Discharge power (W) *
                  </label>
                  <input
                    id={`charger-power-${index}`}
                    type="number"
                    min={1}
                    value={link.power_limit}
                    onChange={(e) =>
                      update(index, { power_limit: Number(e.target.value) || 0 })
                    }
                    disabled={busy || readonly}
                  />
                </div>

                <div className="form-field">
                  <label htmlFor={`charger-soc-${index}`}>SoC floor (%)</label>
                  <input
                    id={`charger-soc-${index}`}
                    type="number"
                    min={0}
                    max={100}
                    value={link.soc_limit}
                    onChange={(e) =>
                      update(index, {
                        soc_limit: Math.max(0, Math.min(100, Number(e.target.value) || 0)),
                      })
                    }
                    disabled={busy || readonly}
                  />
                  <small className="form-help-text">
                    The battery never discharges below this level.
                  </small>
                </div>

                <div className="form-field">
                  <label htmlFor={`charger-duration-${index}`}>
                    Max duration (minutes)
                  </label>
                  <input
                    id={`charger-duration-${index}`}
                    type="number"
                    min={0}
                    value={link.max_duration_min}
                    onChange={(e) =>
                      update(index, {
                        max_duration_min: Math.max(0, Number(e.target.value) || 0),
                      })
                    }
                    disabled={busy || readonly}
                  />
                  <small className="form-help-text">
                    Safety cap: the battery is released after this long even if
                    the charger never reports the session stopping. 0 disables it.
                  </small>
                </div>
              </div>

              {!readonly && (
                <button
                  type="button"
                  className="config-item-remove"
                  onClick={() => remove(index)}
                  disabled={busy}
                >
                  Remove Link
                </button>
              )}
            </div>
          ))}
        </div>
      )}

      {error && <div className="config-error">{error}</div>}
      {message && (
        <small className="form-help-text" style={{ color: "var(--color-success)" }}>
          {message}
        </small>
      )}

      {!readonly && dirty && (
        <div style={{ marginTop: 8, display: "flex", gap: 8, alignItems: "center" }}>
          <button type="button" className="button-small" onClick={save} disabled={busy}>
            {saving ? "Saving…" : "Save charger links"}
          </button>
          <button
            type="button"
            className="button-link"
            onClick={() => void load()}
            disabled={busy}
          >
            Discard
          </button>
          <small className="form-help-text">
            Charger links are saved separately from the agent configuration.
          </small>
        </div>
      )}
    </div>
  );
}

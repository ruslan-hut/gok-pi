import type { KeyboardEvent } from "react";
import { Icon } from "../shared/Icon";

export type ConfigSection =
  | "batteries"
  | "schedules"
  | "chargers"
  | "email"
  | "device"
  | "advanced";

export interface ConfigSectionItem {
  id: ConfigSection;
  label: string;
  icon: string;
  /** Short status shown under the label — a count, or on/off. */
  meta?: string;
  dirty?: boolean;
}

interface ConfigSectionNavProps {
  items: ConfigSectionItem[];
  active: ConfigSection;
  onSelect: (section: ConfigSection) => void;
}

export function ConfigSectionNav({
  items,
  active,
  onSelect,
}: ConfigSectionNavProps) {
  const handleKeyDown = (e: KeyboardEvent, index: number) => {
    let next: number | null = null;
    if (e.key === "ArrowDown" || e.key === "ArrowRight") {
      next = (index + 1) % items.length;
    } else if (e.key === "ArrowUp" || e.key === "ArrowLeft") {
      next = (index - 1 + items.length) % items.length;
    } else if (e.key === "Home") {
      next = 0;
    } else if (e.key === "End") {
      next = items.length - 1;
    }
    if (next !== null) {
      e.preventDefault();
      onSelect(items[next].id);
      document.getElementById(`config-section-${items[next].id}`)?.focus();
    }
  };

  return (
    <nav
      className="config-nav"
      role="tablist"
      aria-orientation="vertical"
      aria-label="Configuration sections"
    >
      {items.map((item, index) => (
        <button
          key={item.id}
          id={`config-section-${item.id}`}
          role="tab"
          type="button"
          aria-selected={active === item.id}
          aria-controls={`config-panel-${item.id}`}
          tabIndex={active === item.id ? 0 : -1}
          className={`config-nav-item${active === item.id ? " config-nav-item-active" : ""}`}
          onClick={() => onSelect(item.id)}
          onKeyDown={(e) => handleKeyDown(e, index)}
        >
          <Icon name={item.icon} size={18} className="config-nav-icon" />
          <span className="config-nav-text">
            <span className="config-nav-label">{item.label}</span>
            {item.meta && (
              <span className="config-nav-meta">{item.meta}</span>
            )}
          </span>
          {item.dirty && (
            <span
              className="config-nav-dot"
              title="Unsaved changes in this section"
              aria-label="Unsaved changes"
            />
          )}
        </button>
      ))}
    </nav>
  );
}

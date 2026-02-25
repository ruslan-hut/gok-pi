import type { ReactNode } from "react";
import { Icon } from "./Icon";

interface CollapsiblePanelProps {
  title: string;
  collapsed: boolean;
  onToggle: () => void;
  headerExtra?: ReactNode;
  children: ReactNode;
}

export function CollapsiblePanel({
  title,
  collapsed,
  onToggle,
  headerExtra,
  children,
}: CollapsiblePanelProps) {
  return (
    <section className="config-panel">
      <div
        className="config-panel-header"
        onClick={onToggle}
        style={{ cursor: "pointer" }}
      >
        <h3>{title}</h3>
        <div
          style={{ display: "flex", alignItems: "center", gap: "0.5rem" }}
        >
          {headerExtra}
          <span className="config-toggle">
            <Icon name={collapsed ? "chevron_right" : "expand_more"} size={20} />
          </span>
        </div>
      </div>
      {!collapsed && children}
    </section>
  );
}

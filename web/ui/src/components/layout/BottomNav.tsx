import type { TabId } from "../../types";

const items: { id: TabId; label: string; icon: string }[] = [
  { id: "monitor", label: "Monitor", icon: "◉" },
  { id: "configure", label: "Config", icon: "⚙" },
  { id: "tools", label: "Tools", icon: "◧" },
];

interface BottomNavProps {
  activeTab: TabId;
  onTabChange: (tab: TabId) => void;
  configDirty?: boolean;
}

export function BottomNav({
  activeTab,
  onTabChange,
  configDirty,
}: BottomNavProps) {
  return (
    <nav className="bottom-nav" aria-label="Dashboard sections">
      {items.map((item) => (
        <button
          key={item.id}
          className={`bottom-nav-item ${activeTab === item.id ? "bottom-nav-item-active" : ""}`}
          onClick={() => onTabChange(item.id)}
          aria-current={activeTab === item.id ? "page" : undefined}
        >
          <span className="bottom-nav-icon">{item.icon}</span>
          <span className="bottom-nav-label">
            {item.label}
            {item.id === "configure" && configDirty && (
              <span
                className="tab-dirty-dot"
                aria-label="Unsaved changes"
              />
            )}
          </span>
        </button>
      ))}
    </nav>
  );
}

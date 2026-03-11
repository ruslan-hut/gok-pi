import type { KeyboardEvent } from "react";
import type { DeviceTab } from "../../types";

const tabs: { id: DeviceTab; label: string }[] = [
  { id: "monitor", label: "Monitor" },
  { id: "configure", label: "Configure" },
  { id: "tools", label: "Tools" },
];

interface TabBarProps {
  activeTab: DeviceTab;
  onTabChange: (tab: DeviceTab) => void;
  configDirty?: boolean;
}

export function TabBar({ activeTab, onTabChange, configDirty }: TabBarProps) {
  const handleKeyDown = (e: KeyboardEvent, index: number) => {
    let nextIndex: number | null = null;
    if (e.key === "ArrowRight") {
      nextIndex = (index + 1) % tabs.length;
    } else if (e.key === "ArrowLeft") {
      nextIndex = (index - 1 + tabs.length) % tabs.length;
    } else if (e.key === "Home") {
      nextIndex = 0;
    } else if (e.key === "End") {
      nextIndex = tabs.length - 1;
    }
    if (nextIndex !== null) {
      e.preventDefault();
      onTabChange(tabs[nextIndex].id);
      document.getElementById(`tab-${tabs[nextIndex].id}`)?.focus();
    }
  };

  return (
    <div className="tab-bar" role="tablist" aria-label="Device sections">
      {tabs.map((tab, index) => (
        <button
          key={tab.id}
          id={`tab-${tab.id}`}
          role="tab"
          aria-selected={activeTab === tab.id}
          aria-controls={`tabpanel-${tab.id}`}
          tabIndex={activeTab === tab.id ? 0 : -1}
          className={`tab-button ${activeTab === tab.id ? "tab-button-active" : ""}`}
          onClick={() => onTabChange(tab.id)}
          onKeyDown={(e) => handleKeyDown(e, index)}
        >
          {tab.label}
          {tab.id === "configure" && configDirty && (
            <span className="tab-dirty-dot" aria-label="Unsaved changes" />
          )}
        </button>
      ))}
    </div>
  );
}

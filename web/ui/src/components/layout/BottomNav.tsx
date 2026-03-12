import { Icon } from "../shared/Icon";
import type { AppPage, DeviceTab } from "../../types";

interface BottomNavProps {
  currentPage: AppPage;
  onNavigate: (page: AppPage) => void;
  configDirty?: boolean;
  readonly?: boolean;
}

const globalItems: { page: "overview" | "electricity" | "database"; label: string; icon: string; authRequired?: boolean }[] = [
  { page: "overview", label: "Overview", icon: "dashboard" },
  { page: "electricity", label: "Electricity", icon: "bolt" },
  { page: "database", label: "Database", icon: "database", authRequired: true },
];

const deviceItems: { tab: DeviceTab; label: string; icon: string }[] = [
  { tab: "monitor", label: "Monitor", icon: "monitor_heart" },
  { tab: "configure", label: "Config", icon: "settings" },
  { tab: "tools", label: "Tools", icon: "build" },
];

export function BottomNav({
  currentPage,
  onNavigate,
  configDirty,
  readonly,
}: BottomNavProps) {
  if (currentPage.page === "device") {
    return (
      <nav className="bottom-nav" aria-label="Device sections">
        <button
          className="bottom-nav-item"
          onClick={() => onNavigate({ page: "overview" })}
        >
          <span className="bottom-nav-icon">
            <Icon name="arrow_back" size={24} />
          </span>
          <span className="bottom-nav-label">Devices</span>
        </button>
        {deviceItems.map((item) => (
          <button
            key={item.tab}
            className={`bottom-nav-item ${currentPage.tab === item.tab ? "bottom-nav-item-active" : ""}`}
            onClick={() => onNavigate({ ...currentPage, tab: item.tab })}
            aria-current={currentPage.tab === item.tab ? "page" : undefined}
          >
            <span className="bottom-nav-icon">
              <Icon name={item.icon} size={24} />
            </span>
            <span className="bottom-nav-label">
              {item.label}
              {item.tab === "configure" && configDirty && (
                <span className="tab-dirty-dot" aria-label="Unsaved changes" />
              )}
            </span>
          </button>
        ))}
      </nav>
    );
  }

  return (
    <nav className="bottom-nav" aria-label="Dashboard sections">
      {globalItems.filter((item) => !item.authRequired || !readonly).map((item) => (
        <button
          key={item.page}
          className={`bottom-nav-item ${currentPage.page === item.page ? "bottom-nav-item-active" : ""}`}
          onClick={() => onNavigate({ page: item.page })}
          aria-current={currentPage.page === item.page ? "page" : undefined}
        >
          <span className="bottom-nav-icon">
            <Icon name={item.icon} size={24} />
          </span>
          <span className="bottom-nav-label">{item.label}</span>
        </button>
      ))}
    </nav>
  );
}

import { Icon } from "../shared/Icon";
import type { AppPage } from "../../types";

interface TopNavProps {
  currentPage: AppPage;
  onNavigate: (page: AppPage) => void;
  connectionActive: boolean;
  deviceName?: string;
  onLogout: () => void;
}

export function TopNav({
  currentPage,
  onNavigate,
  connectionActive,
  deviceName,
  onLogout,
}: TopNavProps) {
  const isDevice = currentPage.page === "device";

  return (
    <nav className="top-nav">
      <div className="top-nav-brand">
        <span className="top-nav-title">GOK-Pi</span>
        <span
          className={`connection-dot ${connectionActive ? "online" : ""}`}
          role="status"
          aria-label={connectionActive ? "Live updates active" : "Reconnecting"}
          title={connectionActive ? "Live updates active" : "Reconnecting"}
        />
      </div>

      <div className="top-nav-center">
        {isDevice ? (
          <div className="top-nav-device">
            <button
              className="top-nav-back"
              onClick={() => onNavigate({ page: "overview" })}
            >
              <Icon name="arrow_back" size={18} />
              <span>Devices</span>
            </button>
            {deviceName && (
              <span className="top-nav-device-name">{deviceName}</span>
            )}
          </div>
        ) : (
          <div className="top-nav-links">
            <button
              className={`top-nav-link ${currentPage.page === "overview" ? "active" : ""}`}
              onClick={() => onNavigate({ page: "overview" })}
            >
              Overview
            </button>
            <button
              className={`top-nav-link ${currentPage.page === "electricity" ? "active" : ""}`}
              onClick={() => onNavigate({ page: "electricity" })}
            >
              Electricity
            </button>
          </div>
        )}
      </div>

      <button className="top-nav-logout" onClick={onLogout} title="Logout">
        <Icon name="logout" size={20} />
      </button>
    </nav>
  );
}

import { useState, useCallback } from "react";
import type { TabId } from "../types";

const STORAGE_KEY = "gok-pi-tab";

export function usePersistedTab(defaultTab: TabId = "monitor"): [TabId, (tab: TabId) => void] {
  const [tab, setTabState] = useState<TabId>(() => {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === "monitor" || stored === "configure" || stored === "tools") {
      return stored;
    }
    return defaultTab;
  });

  const setTab = useCallback((newTab: TabId) => {
    setTabState(newTab);
    localStorage.setItem(STORAGE_KEY, newTab);
  }, []);

  return [tab, setTab];
}

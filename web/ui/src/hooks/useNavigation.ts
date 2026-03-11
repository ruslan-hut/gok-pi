import { useState, useCallback } from "react";
import type { AppPage } from "../types";

const STORAGE_KEY = "gok-pi-nav";

const validDeviceTabs = new Set(["monitor", "configure", "tools"]);
const validPages = new Set(["overview", "electricity", "device"]);

function parseStored(): AppPage | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    if (!parsed || !validPages.has(parsed.page)) return null;
    if (parsed.page === "overview" || parsed.page === "electricity") return parsed;
    if (
      parsed.page === "device" &&
      typeof parsed.agentId === "string" &&
      validDeviceTabs.has(parsed.tab)
    ) {
      return parsed;
    }
  } catch {
    // ignore
  }
  return null;
}

export function useNavigation(): [AppPage, (page: AppPage) => void] {
  const [page, setPageState] = useState<AppPage>(() => {
    return parseStored() || { page: "overview" };
  });

  const setPage = useCallback((newPage: AppPage) => {
    setPageState(newPage);
    localStorage.setItem(STORAGE_KEY, JSON.stringify(newPage));
  }, []);

  return [page, setPage];
}

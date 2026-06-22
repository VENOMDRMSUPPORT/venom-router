import { createContext, useContext } from "react";

export interface DashboardChrome {
  onOpenSidebar: () => void;
}

export const DashboardChromeContext = createContext<DashboardChrome>({
  onOpenSidebar: () => {},
});

export function useDashboardChrome() {
  return useContext(DashboardChromeContext);
}

// The shell's primary navigation model (P2b-UI-001). A plain, static data
// array — not a router config — driving the `vn-nav-*` list and the
// content area's `activeNav` switch (see AppShell.tsx). Mirrors the shape
// of Design_System/ui_kits/venom-console/index.entry.tsx's own NAV (a
// reference composition only, never imported from), grouped exactly as
// Overview / Operate / Insights / Manage.

export interface NavItem {
  group: string;
  key: string;
  label: string;
  icon: string;
}

export const NAV_GROUPS = ["Overview", "Operate", "Insights", "Manage"] as const;

export const NAV: NavItem[] = [
  { group: "Overview", key: "overview", label: "Overview", icon: "layout-dashboard" },

  { group: "Operate", key: "providers", label: "Providers", icon: "server" },
  { group: "Operate", key: "models", label: "Models", icon: "box" },
  { group: "Operate", key: "routing", label: "Routing", icon: "route" },
  { group: "Operate", key: "playground", label: "Playground", icon: "terminal" },

  { group: "Insights", key: "usage", label: "Usage & Analytics", icon: "chart-line" },
  { group: "Insights", key: "quota", label: "Quota & Limits", icon: "gauge" },
  { group: "Insights", key: "token-health", label: "Token Health", icon: "heart-pulse" },
  { group: "Insights", key: "diagnostics", label: "Diagnostics", icon: "activity" },

  { group: "Manage", key: "api-keys", label: "API Keys", icon: "key-round" },
  { group: "Manage", key: "settings", label: "Settings", icon: "settings" },
];

export const DEFAULT_NAV_KEY = "overview";

export function navItemByKey(key: string): NavItem | undefined {
  return NAV.find((item) => item.key === key);
}

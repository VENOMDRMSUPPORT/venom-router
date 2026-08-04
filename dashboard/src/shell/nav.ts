// The shell's primary navigation model (P2b-UI-001). A plain, static data
// array — not a router config — driving the `vn-nav-*` list and the
// content area's `activeNav` switch (see AppShell.tsx). Mirrors the shape
// of Design_System/ui_kits/venom-console/index.entry.tsx's own NAV (a
// reference composition only, never imported from), grouped exactly as
// Overview / Operate / Insights / Manage.
//
// Each item also carries the per-page chrome metadata (legacy console
// parity): a one-line muted `description` rendered under the page title in
// the shared ChromeHeader, and the breadcrumb trail derived by
// breadcrumbTrail() for the shared BreadcrumbBar. One source of truth —
// every page's title/subtitle/icon/breadcrumb comes from this array.

export interface NavItem {
  group: string;
  key: string;
  label: string;
  icon: string;
  /** One-line muted subtitle shown under the page title in the header. */
  description: string;
}

export const NAV_GROUPS = ["Overview", "Operate", "Insights", "Manage"] as const;

const NAV_GROUP_ICONS: Record<(typeof NAV_GROUPS)[number], string> = {
  Overview: "layout-dashboard",
  Operate: "server",
  Insights: "chart-line",
  Manage: "settings",
};

export const NAV: NavItem[] = [
  {
    group: "Overview",
    key: "overview",
    label: "Overview",
    icon: "layout-dashboard",
    description: "Local runtime health and delivery status.",
  },

  {
    group: "Operate",
    key: "providers",
    label: "Providers",
    icon: "server",
    description: "Provider accounts, credentials, and fleet health.",
  },
  {
    group: "Operate",
    key: "models",
    label: "Live Models",
    icon: "box",
    description: "Models currently available through healthy connected provider accounts.",
  },
  {
    group: "Operate",
    key: "routing",
    label: "Routing",
    icon: "route",
    description: "Tier strategies, certified pools, and fallback chains.",
  },
  {
    group: "Operate",
    key: "playground",
    label: "Playground",
    icon: "terminal",
    description: "Chat through the Venom tiers against live routing.",
  },

  {
    group: "Insights",
    key: "usage",
    label: "Usage & Analytics",
    icon: "chart-line",
    description: "Requests, tokens, latency, and cost analytics.",
  },
  {
    group: "Insights",
    key: "quota",
    label: "Quota & Limits",
    icon: "gauge",
    description: "Quota windows, reservations, and cooldowns per account.",
  },
  {
    group: "Insights",
    key: "token-health",
    label: "Token Health",
    icon: "heart-pulse",
    description: "Credential health and refresh status across the fleet.",
  },
  {
    group: "Insights",
    key: "diagnostics",
    label: "Diagnostics",
    icon: "activity",
    description: "Health checks and routing failure inspection.",
  },

  {
    group: "Manage",
    key: "api-keys",
    label: "API Keys",
    icon: "key-round",
    description: "Gateway keys for the /v1/chat/completions surface.",
  },
  {
    group: "Manage",
    key: "settings",
    label: "Settings",
    icon: "settings",
    description: "Runtime identity and operational status.",
  },
];

export const DEFAULT_NAV_KEY = "overview";

export const NAV_SECTIONS = NAV_GROUPS.map((group) => ({
  key: group.toLowerCase(),
  label: group,
  icon: NAV_GROUP_ICONS[group],
  items: NAV.filter((item) => item.group === group).map(({ key, label, icon }) => ({
    key,
    label,
    icon,
  })),
}));

export function navItemByKey(key: string): NavItem | undefined {
  return NAV.find((item) => item.key === key);
}

/** The breadcrumb trail for a page (legacy console pattern): always rooted
 * at "Dashboard", with the item's nav group as the middle crumb whenever it
 * adds information (Overview's group IS its label, so it collapses to two
 * crumbs). The last entry is the current page. */
export function breadcrumbTrail(item: NavItem): string[] {
  return item.group === item.label
    ? ["Dashboard", item.label]
    : ["Dashboard", item.group, item.label];
}

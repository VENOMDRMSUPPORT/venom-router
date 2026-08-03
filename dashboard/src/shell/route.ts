// The shell's URL <-> page mapping. Navigation is still a plain `activeNav`
// state switch in AppShell (no router dependency); this pure module is the ONE
// place that translates between that page key and a real browser path, so every
// page gets its own shareable URL and a refresh stays on the current page
// instead of snapping back to Overview. Path-based (not hash) to match the URL
// bar the owner expects; the Go SPA handler already falls back to index.html
// for these paths (internal/httpui), so a hard refresh on /providers works.

import { DEFAULT_NAV_KEY, navItemByKey } from "./nav";

/** The internal pseudo-key for the "Connect a client" page. It has no nav
 * entry (Overview's Quick Start is the only way in), but it still earns a real
 * URL so a refresh there stays put. Owned here (not AppShell) so this module
 * has no shell dependency and there is a single source of truth. */
export const CONNECT_CLIENT_KEY = "__connect-client";
const CONNECT_CLIENT_PATH_SEGMENT = "connect-client";

export interface Route {
  navKey: string;
  /** Only ever set for the diagnostics deep link (/diagnostics/routes/{id}). */
  requestID?: string;
}

/** The canonical URL path for a page: Overview is the root, the diagnostics
 * deep link carries its request id as a sub-path, and every other page is a
 * single segment equal to its nav key. */
export function pathForRoute(navKey: string, requestID?: string): string {
  if (navKey === "diagnostics" && requestID) {
    return `/diagnostics/routes/${encodeURIComponent(requestID)}`;
  }
  if (navKey === CONNECT_CLIENT_KEY) return `/${CONNECT_CLIENT_PATH_SEGMENT}`;
  if (navKey === DEFAULT_NAV_KEY) return "/";
  return `/${navKey}`;
}

/** Reverse of pathForRoute: which page a URL path names. An unknown or
 * malformed path falls back to the default page — exactly what the old
 * mount-time hash parser did, so a stale/bad link never dead-ends. */
export function parseLocation(pathname: string): Route {
  const segments = pathname.split("/").filter((s) => s !== "");
  if (segments.length === 0) return { navKey: DEFAULT_NAV_KEY };
  if (segments[0] === "diagnostics" && segments[1] === "routes" && segments[2]) {
    return { navKey: "diagnostics", requestID: decodeURIComponent(segments[2]) };
  }
  if (segments.length === 1) {
    if (segments[0] === CONNECT_CLIENT_PATH_SEGMENT) return { navKey: CONNECT_CLIENT_KEY };
    if (navItemByKey(segments[0])) return { navKey: segments[0] };
  }
  return { navKey: DEFAULT_NAV_KEY };
}

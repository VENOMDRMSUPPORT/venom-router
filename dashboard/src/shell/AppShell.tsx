import { useEffect, useRef, useState, type ReactNode } from "react";
import {
  Banner,
  Button,
  IconButton,
  PageContextBar,
  PlannedSurface,
  SectionDeck,
  Spinner,
} from "@venom/design-system/primitives";
import { Icon } from "@venom/design-system/icons";
import {
  getSettings,
  isSessionExpired,
  putSettings,
  type SettingsResponse,
} from "../api/controlClient";
import { logout, type SessionTimes } from "../auth/authClient";
import FleetBreadcrumbChips, { type FleetView } from "../fleet/FleetBreadcrumbChips";
import FleetOverview, { type AuthCategory } from "../fleet/FleetOverview";
import DebugLogPanel from "./DebugLogPanel";
import TokenHealthSurface from "../health/TokenHealthSurface";
import ConnectClientPage from "../connect/ConnectClientPage";
import DiagnosticsSurface from "../diagnostics/DiagnosticsSurface";
import ApiKeysSurface from "../keys/ApiKeysSurface";
import ModelsSurface from "../models/ModelsSurface";
import OverviewSurface from "../overview/OverviewSurface";
import QuotaSurface from "../quota/QuotaSurface";
import PlaygroundSurface from "../playground/PlaygroundSurface";
import RoutingSurface from "../routing/RoutingSurface";
import SettingsSurface from "../settings/SettingsSurface";
import UsageSurface from "../usage/UsageSurface";
import {
  applyAppearanceSettings,
  DEFAULT_ACCENT,
  DEFAULT_DENSITY,
  DEFAULT_RADIUS_PX,
  DEFAULT_SPACING_SCALE,
  DEFAULT_THEME,
  type AccentName,
  type DensityName,
  type ThemeName,
} from "../theme-runtime";
import BrandHeader from "./BrandHeader";
import BreadcrumbBar from "./BreadcrumbBar";
import ChromeHeader from "./ChromeHeader";
import EnterpriseCustomizer, { type CustomizerValue } from "./EnterpriseCustomizer";
import { DEFAULT_NAV_KEY, NAV, NAV_GROUPS, NAV_SECTIONS, navItemByKey } from "./nav";
import { CONNECT_CLIENT_KEY, parseLocation, pathForRoute } from "./route";
import NotificationBell from "./NotificationBell";
import OwnerMenu from "./OwnerMenu";
import SearchBar from "./SearchBar";
import ThemeToggle from "./ThemeToggle";

export interface AppShellProps {
  session: SessionTimes;
  csrfToken: string;
  /** Any authenticated call that comes back session_expired must route
   * back to Login with no auth state left behind — this is that hook. */
  onSessionExpired: () => void;
  onLoggedOut: () => void;
}

interface Appearance {
  theme: ThemeName;
  density: DensityName;
  accent: AccentName;
  radiusPx: number;
  spacingScale: number;
}

const DEFAULT_APPEARANCE: Appearance = {
  theme: DEFAULT_THEME,
  density: DEFAULT_DENSITY,
  accent: DEFAULT_ACCENT,
  radiusPx: DEFAULT_RADIUS_PX,
  spacingScale: DEFAULT_SPACING_SCALE,
};

/** Maps the GET /settings wire payload to the shell's Appearance state,
 * falling back to the frozen defaults (mono/6/1.0) for absent customizer
 * fields. */
function appearanceFromSettings(settings: SettingsResponse): Appearance {
  return {
    theme: settings.theme,
    density: settings.density,
    accent: settings.accent ?? DEFAULT_ACCENT,
    radiusPx: settings.radius_px ?? DEFAULT_RADIUS_PX,
    spacingScale: settings.spacing_scale ?? DEFAULT_SPACING_SCALE,
  };
}

/** Maps the shell's Appearance state to the PUT /settings wire body. */
function appearanceToSettings(appearance: Appearance): SettingsResponse {
  return {
    theme: appearance.theme,
    density: appearance.density,
    accent: appearance.accent,
    radius_px: appearance.radiusPx,
    spacing_scale: appearance.spacingScale,
  };
}

/**
 * The app shell (P2b-UI-001): left nav + top bar + content area, built
 * directly from the `vn-shell*` CSS classes shipped in styles.css (there
 * is no shipped AppShell/Sidebar/Topbar component) plus real
 * @venom/design-system primitives/domain components. Navigation is a
 * plain `activeNav` React state switch — no router dependency, mirroring
 * AuthGate's own state-machine style.
 *
 * On mount, restores the owner's persisted theme/density from
 * GET /settings and applies them before the content area's first paint
 * (a Spinner covers that brief window so the shell never flashes the
 * package defaults and then snaps to the saved values). A failed restore
 * is non-fatal: the already-applied defaults stand, and a small notice is
 * surfaced instead of blocking the shell.
 */
export default function AppShell(props: AppShellProps) {
  const { session, csrfToken, onSessionExpired, onLoggedOut } = props;

  // The current page is derived from the URL PATH: read once at mount (so a
  // refresh or a pasted /providers link opens that page, not Overview) and kept
  // in sync both ways below — activeNav -> pushState, and popstate -> activeNav.
  const [initialRoute] = useState(routeFromLocation);
  const [activeNav, setActiveNav] = useState(initialRoute.navKey);
  // Cleared the first time the owner navigates by hand, so a deep link opens once
  // rather than re-asserting itself every time they return to Diagnostics.
  const [deepLinkRequestID, setDeepLinkRequestID] = useState(initialRoute.requestID);
  const [settingsLoaded, setSettingsLoaded] = useState(false);
  const [appearance, setAppearanceState] = useState<Appearance>(DEFAULT_APPEARANCE);
  const [appearanceNotice, setAppearanceNotice] = useState<string | null>(null);
  // The Provider Fleet's breadcrumb-row chip counts (legacy parity),
  // reported up by FleetOverview once its live data loads; null until then
  // so the chips never render placeholder values.
  const [fleetCounts, setFleetCounts] = useState<{ active: number; total: number } | null>(null);
  // The breadcrumb-row chip selection for the Provider Fleet: "all" (full
  // catalog, default) or "active" (only providers with ≥1 connected
  // account). Owned here so both the chips and FleetOverview share one
  // source of truth.
  const [fleetView, setFleetView] = useState<FleetView>("active");
  // The Providers page's auth filter, lifted here so the breadcrumb's
  // third segment can mirror it ("All Providers" / "OAuth Providers" /
  // "API KEY Providers") — FleetOverview receives it controlled.
  const [fleetCategory, setFleetCategory] = useState<AuthCategory>("all");
  // The Debug Log panel (providers page only) — a chip in the page-context
  // bar toggles it.
  const [debugOpen, setDebugOpen] = useState(false);
  const [apiKeyCreateOpen, setApiKeyCreateOpen] = useState(false);
  const appearanceRef = useRef(appearance);
  appearanceRef.current = appearance;

  useEffect(() => {
    let cancelled = false;

    async function restoreSettings() {
      try {
        const settings = await getSettings();
        if (cancelled) return;
        applyAppearance(appearanceFromSettings(settings));
      } catch (err) {
        if (cancelled) return;
        if (isSessionExpired(err)) {
          onSessionExpired();
          return;
        }
        // Non-fatal: the package defaults (already applied by
        // initializeThemeRuntime before first paint) stand as-is.
        setAppearanceNotice("Could not load your saved appearance settings — using defaults.");
      } finally {
        if (!cancelled) setSettingsLoaded(true);
      }
    }

    void restoreSettings();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Keep the URL bar in step with the active page so every page has its own
  // shareable link and a refresh stays put. pushState ONLY when the path
  // actually changes: on mount (path already matches the parsed route) and on a
  // popstate-driven change (handled below, which sets state to match the URL)
  // the guard is a no-op, so there is no feedback loop and no duplicate history
  // entry.
  useEffect(() => {
    const target = pathForRoute(activeNav, deepLinkRequestID);
    if (window.location.pathname !== target) {
      window.history.pushState(null, "", target);
    }
  }, [activeNav, deepLinkRequestID]);

  // Back/forward (and any external path change) re-derive the active page from
  // the URL. This is the counterpart to the pushState above.
  useEffect(() => {
    function onPopState() {
      const route = routeFromLocation();
      setActiveNav(route.navKey);
      setDeepLinkRequestID(route.requestID);
    }
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  function applyAppearance(next: Appearance) {
    // ALL five fields go through the DS apply* functions in one shot —
    // never hand-rolled attribute writes.
    applyAppearanceSettings(appearanceToSettings(next));
    setAppearanceState(next);
  }

  async function persistAppearance(next: Appearance) {
    try {
      await putSettings(appearanceToSettings(next), csrfToken);
    } catch (err) {
      if (isSessionExpired(err)) {
        onSessionExpired();
        return;
      }
      setAppearanceNotice(
        "Could not save your appearance settings. Your change is applied for this session only.",
      );
    }
  }

  function handleThemeChange(nextTheme: ThemeName) {
    const next = { ...appearance, theme: nextTheme };
    applyAppearance(next);
    void persistAppearance(next);
  }

  // The customizer owns theme/accent/radius/spacing but NOT density
  // (density has no header control anymore — owner request; the persisted
  // setting is still boot-applied from GET /settings). Merge over the
  // LATEST appearance (via ref) so a debounced slider persist never
  // clobbers the restored density.
  function mergeCustomizerValue(next: CustomizerValue): Appearance {
    return {
      ...appearanceRef.current,
      theme: next.theme,
      accent: next.accent,
      radiusPx: next.radiusPx,
      spacingScale: next.spacingScale,
    };
  }

  function handleCustomizerApply(next: CustomizerValue) {
    applyAppearance(mergeCustomizerValue(next));
  }

  function handleCustomizerPersist(next: CustomizerValue) {
    void persistAppearance(mergeCustomizerValue(next));
  }

  async function handleSignOut() {
    try {
      await logout(csrfToken);
    } catch {
      // logout is documented as idempotent/always-200 — a network hiccup
      // here still means the owner wants out.
    } finally {
      onLoggedOut();
    }
  }

  function handleNavigate(next: string, requestID?: string) {
    if (next !== "api-keys") setApiKeyCreateOpen(false);
    // The Debug chip only exists on the providers page — its panel leaves
    // with it.
    if (next !== "providers") setDebugOpen(false);
    // A hand navigation supersedes any prior deep link, UNLESS this navigation
    // is itself a deep link (an Overview activity row asking for one request's
    // explanation), in which case we carry its request id through.
    setDeepLinkRequestID(requestID);
    setActiveNav(next);
  }

  if (!settingsLoaded) {
    return (
      <div className="vn-shell">
        <div className="flex min-h-screen w-full items-center justify-center bg-surface-canvas">
          <Spinner size="lg" label="Loading your workspace" />
        </div>
      </div>
    );
  }

  // The nav model is static and DEFAULT_NAV_KEY is a member of it, so the
  // active item always resolves; the fallback only guards the type.
  const activeItem = navItemByKey(activeNav) ?? NAV[0];

  return (
    <div className="vn-shell">
      <nav className="vn-shell-nav vn-scroll" aria-label="Primary">
        <BrandHeader onNavigate={(key) => handleNavigate(key)} />
        {NAV_GROUPS.map((group) => (
          <div key={group}>
            <div className="vn-nav-group vn-overline">{group}</div>
            {NAV.filter((item) => item.group === group).map((item) => (
              <a
                key={item.key}
                className="vn-nav-item"
                href={pathForRoute(item.key)}
                aria-current={activeNav === item.key ? "page" : undefined}
                onClick={(e) => {
                  e.preventDefault();
                  handleNavigate(item.key);
                }}
              >
                <Icon name={item.icon} size={15} />
                {item.label}
              </a>
            ))}
          </div>
        ))}
      </nav>

      <ChromeHeader
        title={activeItem.label}
        subtitle={activeItem.description}
        icon={activeItem.icon}
      >
        <SearchBar onNavigate={handleNavigate} />
        <span className="vn-desktop-theme-toggle">
          <ThemeToggle theme={appearance.theme} onChange={handleThemeChange} />
        </span>
        <NotificationBell />
        <OwnerMenu
          session={session}
          onSignOut={handleSignOut}
          onNavigate={handleNavigate}
        />
        <EnterpriseCustomizer
          value={{
            theme: appearance.theme,
            accent: appearance.accent,
            radiusPx: appearance.radiusPx,
            spacingScale: appearance.spacingScale,
          }}
          onApply={handleCustomizerApply}
          onPersist={handleCustomizerPersist}
        />
      </ChromeHeader>

      <main className="vn-shell-main vn-scroll">
        <div className="vn-shell-content">
          {appearanceNotice ? (
            <Banner
              tone="warning"
              actions={
                <IconButton
                  icon="x"
                  label="Dismiss notice"
                  variant="ghost"
                  onClick={() => setAppearanceNotice(null)}
                />
              }
            >
              {appearanceNotice}
            </Banner>
          ) : null}

          {/* The global breadcrumb row (legacy parity): trail chip on the
              left (its third segment mirrors the providers page's live
              auth filter) plus the Debug chip there; on the Providers
              page, the fleet's Active/All chips on the right once the
              live counts have loaded. */}
          <PageContextBar
            leading={
              <span className="flex items-center gap-2">
                <BreadcrumbBar
                  item={activeItem}
                  trail={activeNav === "providers" ? ["Dashboard", "Providers", FLEET_CATEGORY_CRUMB[fleetCategory]] : undefined}
                  onNavigateHome={() => handleNavigate(DEFAULT_NAV_KEY)}
                />
                {activeNav === "providers" ? (
                  <Button
                    variant="secondary"
                    size="sm"
                    icon="terminal"
                    aria-pressed={debugOpen}
                    onClick={() => setDebugOpen((open) => !open)}
                  >
                    Debug
                  </Button>
                ) : null}
              </span>
            }
            secondary={activeNav === "providers" && fleetCounts ? (
              <FleetBreadcrumbChips
                activeCount={fleetCounts.active}
                totalCount={fleetCounts.total}
                view={fleetView}
                onViewChange={setFleetView}
              />
            ) : undefined}
            actions={activeNav === "api-keys" ? (
              <Button variant="primary" icon="plus" onClick={() => setApiKeyCreateOpen(true)}>
                <span className="vn-api-key-action-wide">New API key</span>
                <span className="vn-api-key-action-compact">New key</span>
              </Button>
            ) : undefined}
          />

          {renderSurface(
            activeNav,
            csrfToken,
            onSessionExpired,
            setFleetCounts,
            fleetView,
            fleetCategory,
            setFleetCategory,
            apiKeyCreateOpen,
            setApiKeyCreateOpen,
            handleNavigate,
            deepLinkRequestID,
          )}

          <DebugLogPanel open={debugOpen} onClose={() => setDebugOpen(false)} />
        </div>
      </main>

      <SectionDeck
        sections={NAV_SECTIONS}
        activeKey={activeNav}
        onNavigate={handleNavigate}
      />
    </div>
  );
}

// The Connect-a-client page's internal route key (P6-UI-011) is owned by
// ./route (single source of truth for the URL <-> page mapping) and re-exported
// here so existing importers keep working. It is deliberately NOT a nav.ts
// entry: the card reaches this page from Overview's Quick Start, so navItemByKey
// returns undefined for it and the page supplies its own header.
export { CONNECT_CLIENT_KEY };

/** The providers-page breadcrumb's third segment per auth filter (the
 * documented "All Providers / OAuth Providers / API KEY Providers"). */
const FLEET_CATEGORY_CRUMB: Record<AuthCategory, string> = {
  all: "All Providers",
  oauth: "OAuth Providers",
  api_key: "API KEY Providers",
};

/** Reads the current browser path into a route (see ./route). Used at mount and
 * on every history popstate so a refresh, a back/forward, or a pasted URL all
 * resolve to the right page. Safe on the server (no window) — falls back to the
 * default page. */
function routeFromLocation(): { navKey: string; requestID?: string } {
  const pathname = typeof window === "undefined" ? "/" : window.location.pathname;
  return parseLocation(pathname);
}

// Mounts each nav destination's real surface, falling through to an honest
// "not built yet" state for the keys no unit has claimed.
//
// Every branch here maps an EXISTING nav.ts key to a shipped surface — no unit
// adds a nav entry, so a surface that has no key has no home and a key with no
// surface says so rather than rendering something plausible.
function renderSurface(
  navKey: string,
  csrfToken: string,
  onSessionExpired: () => void,
  onFleetCounts: (counts: { active: number; total: number }) => void,
  fleetView: FleetView,
  fleetCategory: AuthCategory,
  onFleetCategoryChange: (category: AuthCategory) => void,
  apiKeyCreateOpen: boolean,
  onApiKeyCreateOpenChange: (open: boolean) => void,
  onNavigate: (navKey: string, requestID?: string) => void,
  deepLinkRequestID?: string,
): ReactNode {
  if (navKey === "providers") {
    return (
      <FleetOverview
        csrfToken={csrfToken}
        onSessionExpired={onSessionExpired}
        onCounts={onFleetCounts}
        view={fleetView}
        category={fleetCategory}
        onCategoryChange={onFleetCategoryChange}
      />
    );
  }

  // P6-UI-006: Quota & Limits.
  if (navKey === "quota") {
    return <QuotaSurface onSessionExpired={onSessionExpired} />;
  }

  // P6-UI-007: Token Health.
  if (navKey === "token-health") {
    return <TokenHealthSurface csrfToken={csrfToken} onSessionExpired={onSessionExpired} />;
  }

  // P6-UI-009: API Keys.
  if (navKey === "api-keys") {
    return (
      <ApiKeysSurface
        csrfToken={csrfToken}
        onSessionExpired={onSessionExpired}
        createOpen={apiKeyCreateOpen}
        onCreateOpenChange={onApiKeyCreateOpenChange}
      />
    );
  }

  // P6-UI-002: Models (the existing `models` nav key — nav.ts is unchanged).
  if (navKey === "models") {
    return <ModelsSurface csrfToken={csrfToken} onSessionExpired={onSessionExpired} />;
  }

  // P6-UI-003: Routing (the existing `routing` nav key).
  if (navKey === "routing") {
    return <RoutingSurface onSessionExpired={onSessionExpired} />;
  }

  // P6-UI-008: Diagnostics (RouteExplain + reconciliation). deepLinkRequestID
  // carries the request id from a `#diagnostics/routes/{request_id}` hash so an
  // Overview activity link opens that request's explanation directly.
  if (navKey === "diagnostics") {
    return (
      <DiagnosticsSurface
        csrfToken={csrfToken}
        onSessionExpired={onSessionExpired}
        deepLinkRequestID={deepLinkRequestID}
      />
    );
  }

  // P6-UI-010: Settings.
  if (navKey === "settings") {
    return <SettingsSurface csrfToken={csrfToken} onSessionExpired={onSessionExpired} />;
  }

  // P6-UI-004: Playground.
  if (navKey === "playground") {
    return <PlaygroundSurface />;
  }

  // P6-UI-005: Usage & Analytics.
  if (navKey === "usage") {
    return <UsageSurface onSessionExpired={onSessionExpired} />;
  }

  // P6-UI-011: Connect a client. Reached from Overview's Quick Start rather than
  // from a nav entry (nav.ts is unchanged), so it has no nav key of its own — the
  // shell routes to it through this internal pseudo-key.
  if (navKey === CONNECT_CLIENT_KEY) {
    return <ConnectClientPage csrfToken={csrfToken} onSessionExpired={onSessionExpired} />;
  }

  // P6-UI-001: Overview — the default landing surface, replacing the
  // placeholder this key used to fall through to.
  if (navKey === "overview") {
    return (
      <OverviewSurface
        csrfToken={csrfToken}
        onSessionExpired={onSessionExpired}
        onOpenQuickStart={() => onNavigate(CONNECT_CLIENT_KEY)}
        onOpenRequest={(requestID) => onNavigate("diagnostics", requestID)}
      />
    );
  }

  const item = navItemByKey(navKey);
  return (
    <PlannedSurface
      icon={item?.icon ?? "clock"}
      title={item?.label ?? "Planned surface"}
      description={item?.description ?? "This surface is intentionally reserved for a future implementation."}
    />
  );
}

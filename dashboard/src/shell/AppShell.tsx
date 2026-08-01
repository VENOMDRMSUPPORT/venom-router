import { useEffect, useRef, useState, type ReactNode } from "react";
import { Banner, EmptyState, IconButton, Spinner } from "@venom/design-system/primitives";
import { Icon } from "@venom/design-system/icons";
import {
  getSettings,
  isSessionExpired,
  putSettings,
  type SettingsResponse,
} from "../api/controlClient";
import { logout, type SessionTimes } from "../auth/authClient";
import FleetBreadcrumbChips, { type FleetView } from "../fleet/FleetBreadcrumbChips";
import FleetOverview from "../fleet/FleetOverview";
import TokenHealthSurface from "../health/TokenHealthSurface";
import ApiKeysSurface from "../keys/ApiKeysSurface";
import ModelsSurface from "../models/ModelsSurface";
import QuotaSurface from "../quota/QuotaSurface";
import RoutingSurface from "../routing/RoutingSurface";
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
import BreadcrumbBar from "./BreadcrumbBar";
import ChromeHeader from "./ChromeHeader";
import EnterpriseCustomizer, { type CustomizerValue } from "./EnterpriseCustomizer";
import { DEFAULT_NAV_KEY, NAV, NAV_GROUPS, navItemByKey } from "./nav";
import NotificationBell from "./NotificationBell";
import OwnerMenu from "./OwnerMenu";
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

  const [activeNav, setActiveNav] = useState(DEFAULT_NAV_KEY);
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
  const [fleetView, setFleetView] = useState<FleetView>("all");
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
        {/* Brand block (legacy sidebar-header pattern): accent-tinted logo
            mark + wordmark with the small-caps slogan line beneath. The
            mark is the DS route glyph rendered via currentColor, so it
            follows the customizer's live accent. */}
        <div className="vn-nav-brand">
          <Icon name="route" size={18} className="flex-none text-accent-text" />
          <div className="flex min-w-0 flex-col">
            <span className="truncate">Venom Router</span>
            <span className="vn-overline truncate">AI Control Center</span>
          </div>
        </div>
        {NAV_GROUPS.map((group) => (
          <div key={group}>
            <div className="vn-nav-group vn-overline">{group}</div>
            {NAV.filter((item) => item.group === group).map((item) => (
              <a
                key={item.key}
                className="vn-nav-item"
                href={`#${item.key}`}
                aria-current={activeNav === item.key ? "page" : undefined}
                onClick={(e) => {
                  e.preventDefault();
                  setActiveNav(item.key);
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
        <ThemeToggle theme={appearance.theme} onChange={handleThemeChange} />
        <NotificationBell />
        <OwnerMenu session={session} onSignOut={handleSignOut} />
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
              left; on the Providers page, the fleet's Active/All chips on
              the right once the live counts have loaded. */}
          <div className="flex flex-wrap items-center justify-between gap-2">
            <BreadcrumbBar item={activeItem} onNavigateHome={() => setActiveNav(DEFAULT_NAV_KEY)} />
            {activeNav === "providers" && fleetCounts ? (
              <FleetBreadcrumbChips
                activeCount={fleetCounts.active}
                totalCount={fleetCounts.total}
                view={fleetView}
                onViewChange={setFleetView}
              />
            ) : null}
          </div>

          {renderSurface(activeNav, csrfToken, onSessionExpired, setFleetCounts, fleetView)}
        </div>
      </main>

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
    </div>
  );
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
): ReactNode {
  if (navKey === "providers") {
    return (
      <FleetOverview
        csrfToken={csrfToken}
        onSessionExpired={onSessionExpired}
        onCounts={onFleetCounts}
        view={fleetView}
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
    return <ApiKeysSurface csrfToken={csrfToken} onSessionExpired={onSessionExpired} />;
  }

  // P6-UI-002: Models (the existing `models` nav key — nav.ts is unchanged).
  if (navKey === "models") {
    return <ModelsSurface csrfToken={csrfToken} onSessionExpired={onSessionExpired} />;
  }

  // P6-UI-003: Routing (the existing `routing` nav key).
  if (navKey === "routing") {
    return <RoutingSurface onSessionExpired={onSessionExpired} />;
  }

  const item = navItemByKey(navKey);
  return (
    <EmptyState
      icon={item?.icon ?? "clock"}
      title="Coming in a later phase"
      description={`${item?.label ?? "This surface"} is not built yet — it will land in a later phase of the project.`}
    />
  );
}

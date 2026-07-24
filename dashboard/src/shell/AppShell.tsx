import { useEffect, useState, type ReactNode } from "react";
import { Badge, Banner, DensityToggle, EmptyState, IconButton, Spinner, ThemeSwitcher } from "@venom/design-system/primitives";
import { Icon } from "@venom/design-system/icons";
import { getSettings, isSessionExpired, putSettings } from "../api/controlClient";
import { logout, type SessionTimes } from "../auth/authClient";
import FleetOverview from "../fleet/FleetOverview";
import { DEFAULT_DENSITY, DEFAULT_THEME, setDensity, setTheme, type DensityName, type ThemeName } from "../theme-runtime";
import { DEFAULT_NAV_KEY, NAV, NAV_GROUPS, navItemByKey } from "./nav";
import OwnerMenu from "./OwnerMenu";
import PageHeader from "./PageHeader";

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
  const [appearance, setAppearanceState] = useState<Appearance>({ theme: DEFAULT_THEME, density: DEFAULT_DENSITY });
  const [appearanceNotice, setAppearanceNotice] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function restoreSettings() {
      try {
        const settings = await getSettings();
        if (cancelled) return;
        applyAppearance(settings);
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
    setTheme(next.theme);
    setDensity(next.density);
    setAppearanceState(next);
  }

  async function persistAppearance(next: Appearance) {
    try {
      await putSettings(next, csrfToken);
    } catch (err) {
      if (isSessionExpired(err)) {
        onSessionExpired();
        return;
      }
      setAppearanceNotice("Could not save your appearance settings. Your change is applied for this session only.");
    }
  }

  function handleThemeChange(nextTheme: ThemeName) {
    const next = { ...appearance, theme: nextTheme };
    applyAppearance(next);
    void persistAppearance(next);
  }

  function handleDensityChange(nextDensity: DensityName) {
    const next = { ...appearance, density: nextDensity };
    applyAppearance(next);
    void persistAppearance(next);
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

  const activeItem = navItemByKey(activeNav);

  return (
    <div className="vn-shell">
      <nav className="vn-shell-nav vn-scroll" aria-label="Primary">
        <div className="vn-nav-brand">
          <Icon name="route" size={18} />
          Venom Router
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

      <header className="vn-shell-topbar">
        <Badge tone="info" icon="server">
          Owner console
        </Badge>
        <span className="vn-caption vn-mono-xs">Loopback-only control plane</span>
        <span className="flex-1" />
        <ThemeSwitcher value={appearance.theme} onChange={handleThemeChange} />
        <DensityToggle value={appearance.density} onChange={handleDensityChange} />
        <OwnerMenu session={session} onSignOut={handleSignOut} />
      </header>

      <main className="vn-shell-main vn-scroll">
        <div className="vn-shell-content">
          {appearanceNotice ? (
            <Banner
              tone="warning"
              actions={<IconButton icon="x" label="Dismiss notice" variant="ghost" onClick={() => setAppearanceNotice(null)} />}
            >
              {appearanceNotice}
            </Banner>
          ) : null}

          <PageHeader title={activeItem?.label ?? "Overview"} />

          {renderSurface(activeNav, csrfToken, onSessionExpired)}
        </div>
      </main>
    </div>
  );
}

// P2b-UI-001 scope: every nav destination other than Providers is a
// placeholder. Providers (P2b-UI-003) renders the real Provider Fleet.
function renderSurface(navKey: string, csrfToken: string, onSessionExpired: () => void): ReactNode {
  if (navKey === "providers") {
    return <FleetOverview csrfToken={csrfToken} onSessionExpired={onSessionExpired} />;
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

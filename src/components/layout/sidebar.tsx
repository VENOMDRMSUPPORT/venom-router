import { Link, useRouterState, useNavigate, useRouteContext } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  LayoutDashboard,
  Server,
  Brain,
  Zap,
  GitBranch,
  FlaskConical,
  BarChart3,
  Gauge,
  Key,
  Bug,
  Settings,
  ChevronRight,
  ChevronDown,
  Globe,
  Gift,
  LogOut,
  X,
} from "lucide-react";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { supabase } from "@/integrations/supabase/client";
import { Logo } from "@/components/brand/logo";

interface NavItem {
  label: string;
  to: string;
  icon: React.ComponentType<{ className?: string }>;
  exact?: boolean;
  badge?: string;
  highlight?: boolean;
}

const NAV_PRIMARY: NavItem[] = [
  { label: "Overview", to: "/overview", icon: LayoutDashboard, exact: true },
];

const NAV_OPERATE: NavItem[] = [
  { label: "Models", to: "/models", icon: Brain },
  { label: "Venom Models", to: "/venom-models", icon: Zap, highlight: true },
  { label: "Routing Rules", to: "/routing", icon: GitBranch },
  { label: "Playground", to: "/playground", icon: FlaskConical },
];

const NAV_INSIGHTS: NavItem[] = [
  { label: "Usage & Analytics", to: "/usage", icon: BarChart3 },
  { label: "Quota & Limits", to: "/quota", icon: Gauge },
  { label: "Diagnostics", to: "/diagnostics", icon: Bug },
];

const NAV_MANAGE: NavItem[] = [
  { label: "API Keys", to: "/api-keys", icon: Key },
  { label: "Settings", to: "/settings", icon: Settings },
];

const PROVIDER_CHILDREN = [
  { label: "OAuth Providers", to: "/providers/oauth", icon: Globe },
  { label: "Free Providers", to: "/providers/free", icon: Gift },
];

function NavRow({
  item,
  pathname,
  onNavigate,
}: {
  item: NavItem;
  pathname: string;
  onNavigate?: () => void;
}) {
  const active = item.exact
    ? pathname === item.to
    : pathname === item.to || pathname.startsWith(item.to + "/");
  const Icon = item.icon;
  return (
    <li>
      <Link
        to={item.to}
        onClick={onNavigate}
        className={cn(
          "group relative flex items-center gap-3 rounded-lg px-3 py-2 text-[13px] font-medium transition-all",
          active
            ? "bg-sidebar-accent text-sidebar-accent-foreground shadow-[inset_1px_0_0_0_var(--sidebar-primary)]"
            : "text-sidebar-foreground/70 hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground",
        )}
      >
        <Icon
          className={cn(
            "h-[15px] w-[15px] flex-shrink-0 transition-colors",
            active
              ? "text-sidebar-primary"
              : "text-sidebar-foreground/50 group-hover:text-sidebar-accent-foreground",
            item.highlight && !active && "text-sidebar-primary",
          )}
        />
        <span className="flex-1 truncate">{item.label}</span>
        {item.badge && (
          <span className="ml-auto rounded-full bg-sidebar-primary/15 px-1.5 py-0.5 text-[10px] font-semibold text-sidebar-primary">
            {item.badge}
          </span>
        )}
      </Link>
    </li>
  );
}

function NavGroup({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="px-2">
      <div className="px-3 pt-4 pb-1.5 text-[10px] font-semibold uppercase tracking-[0.14em] text-sidebar-muted">
        {label}
      </div>
      <ul className="space-y-0.5">{children}</ul>
    </div>
  );
}

interface SidebarContentProps {
  pathname: string;
  onNavigate?: () => void;
}

function SidebarBody({ pathname, onNavigate }: SidebarContentProps) {
  const providersActive = pathname.startsWith("/providers");
  const [providersOpen, setProvidersOpen] = useState(providersActive);

  useEffect(() => {
    if (providersActive) setProvidersOpen(true);
  }, [providersActive]);

  return (
    <nav className="scrollbar-sidebar flex-1 overflow-y-auto py-2">
      <div className="px-2">
        <ul className="space-y-0.5">
          {NAV_PRIMARY.map((item) => (
            <NavRow key={item.to} item={item} pathname={pathname} onNavigate={onNavigate} />
          ))}
        </ul>
      </div>

      <NavGroup label="Providers">
        <li>
          <button
            onClick={() => setProvidersOpen((o) => !o)}
            className={cn(
              "group flex w-full items-center gap-3 rounded-lg px-3 py-2 text-[13px] font-medium transition-all",
              providersActive
                ? "bg-sidebar-accent text-sidebar-accent-foreground"
                : "text-sidebar-foreground/70 hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground",
            )}
          >
            <Server
              className={cn(
                "h-[15px] w-[15px] flex-shrink-0",
                providersActive
                  ? "text-sidebar-primary"
                  : "text-sidebar-foreground/50 group-hover:text-sidebar-accent-foreground",
              )}
            />
            <span className="flex-1 text-left truncate">Providers</span>
            {providersOpen ? (
              <ChevronDown className="h-3.5 w-3.5 opacity-60" />
            ) : (
              <ChevronRight className="h-3.5 w-3.5 opacity-60" />
            )}
          </button>
          {providersOpen && (
            <ul className="mt-1 ml-4 space-y-0.5 border-l border-sidebar-border/70 pl-2">
              {PROVIDER_CHILDREN.map((child) => {
                const active = pathname === child.to || pathname.startsWith(child.to + "/");
                const Icon = child.icon;
                return (
                  <li key={child.to}>
                    <Link
                      to={child.to}
                      onClick={onNavigate}
                      className={cn(
                        "group flex items-center gap-2.5 rounded-md px-2.5 py-1.5 text-[12px] font-medium transition-colors",
                        active
                          ? "bg-sidebar-accent/80 text-sidebar-accent-foreground"
                          : "text-sidebar-foreground/60 hover:bg-sidebar-accent/40 hover:text-sidebar-accent-foreground",
                      )}
                    >
                      <Icon
                        className={cn(
                          "h-3.5 w-3.5 flex-shrink-0",
                          active ? "text-sidebar-primary" : "text-sidebar-foreground/50",
                        )}
                      />
                      <span className="truncate">{child.label}</span>
                    </Link>
                  </li>
                );
              })}
            </ul>
          )}
        </li>
      </NavGroup>

      <NavGroup label="Operate">
        {NAV_OPERATE.map((item) => (
          <NavRow key={item.to} item={item} pathname={pathname} onNavigate={onNavigate} />
        ))}
      </NavGroup>

      <NavGroup label="Insights">
        {NAV_INSIGHTS.map((item) => (
          <NavRow key={item.to} item={item} pathname={pathname} onNavigate={onNavigate} />
        ))}
      </NavGroup>

      <NavGroup label="Manage">
        {NAV_MANAGE.map((item) => (
          <NavRow key={item.to} item={item} pathname={pathname} onNavigate={onNavigate} />
        ))}
      </NavGroup>
    </nav>
  );
}

function SidebarFooter() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [busy, setBusy] = useState(false);
  const { user } = useRouteContext({ from: "/_authenticated" });
  const email = user?.email ?? null;

  async function handleSignOut() {
    setBusy(true);
    try {
      await queryClient.cancelQueries();
      queryClient.clear();
      const { error } = await supabase.auth.signOut();
      if (error) throw error;
      navigate({ to: "/auth", replace: true });
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Sign out failed");
      setBusy(false);
    }
  }

  const initial = email?.[0]?.toUpperCase() ?? "O";

  return (
    <div className="border-t border-sidebar-border/70 p-3">
      <div className="flex items-center gap-2.5 rounded-lg bg-sidebar-accent/40 px-2.5 py-2">
        <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-gradient-brand text-[12px] font-semibold text-white shadow-elegant">
          {initial}
        </div>
        <div className="min-w-0 flex-1">
          <div className="truncate text-[12px] font-semibold text-sidebar-foreground">
            {email ?? "Owner"}
          </div>
          <div className="flex items-center gap-1.5">
            <div className="h-1.5 w-1.5 rounded-full bg-emerald-400 animate-pulse" />
            <span className="text-[10px] text-sidebar-muted">Owner · Online</span>
          </div>
        </div>
        <button
          type="button"
          onClick={handleSignOut}
          disabled={busy}
          title="Sign out"
          className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-sidebar-foreground/60 hover:bg-sidebar-accent hover:text-sidebar-foreground transition-colors disabled:opacity-40"
        >
          <LogOut className="h-3.5 w-3.5" />
        </button>
      </div>
    </div>
  );
}

function SidebarHeader({ onClose }: { onClose?: () => void }) {
  return (
    <div className="flex h-16 items-center justify-between gap-2 border-b border-sidebar-border/70 px-4">
      <Link to="/overview" className="min-w-0">
        <Logo />
      </Link>
      {onClose && (
        <button
          onClick={onClose}
          className="md:hidden flex h-8 w-8 items-center justify-center rounded-md text-sidebar-foreground/60 hover:bg-sidebar-accent hover:text-sidebar-foreground"
        >
          <X className="h-4 w-4" />
        </button>
      )}
    </div>
  );
}

interface SidebarProps {
  mobileOpen?: boolean;
  onMobileClose?: () => void;
}

export function Sidebar({ mobileOpen, onMobileClose }: SidebarProps) {
  const pathname = useRouterState({ select: (s) => s.location.pathname });

  return (
    <>
      <aside className="hidden md:flex h-screen w-64 flex-col bg-gradient-sidebar text-sidebar-foreground border-r border-sidebar-border/70 shrink-0">
        <SidebarHeader />
        <SidebarBody pathname={pathname} />
        <SidebarFooter />
      </aside>

      <div
        className={cn(
          "md:hidden fixed inset-0 z-50 transition-opacity",
          mobileOpen ? "opacity-100 pointer-events-auto" : "opacity-0 pointer-events-none",
        )}
      >
        <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" onClick={onMobileClose} />
        <aside
          className={cn(
            "absolute inset-y-0 left-0 flex w-72 max-w-[85vw] flex-col bg-gradient-sidebar text-sidebar-foreground border-r border-sidebar-border/70 shadow-2xl transition-transform",
            mobileOpen ? "translate-x-0" : "-translate-x-full",
          )}
        >
          <SidebarHeader onClose={onMobileClose} />
          <SidebarBody pathname={pathname} onNavigate={onMobileClose} />
          <SidebarFooter />
        </aside>
      </div>
    </>
  );
}

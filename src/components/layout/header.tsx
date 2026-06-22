import { Menu, Search, Bell } from "lucide-react";
import { cn } from "@/lib/utils";
import { useDashboardChrome } from "@/lib/use-dashboard-chrome";
import { ThemeToggle } from "@/components/layout/theme-toggle";

interface HeaderProps {
  title: string;
  description?: string;
  icon?: React.ReactNode;
  iconClassName?: string;
  actions?: React.ReactNode;
  onOpenSidebar?: () => void;
}

export function Header({
  title,
  description,
  icon,
  iconClassName,
  actions,
  onOpenSidebar,
}: HeaderProps) {
  const chrome = useDashboardChrome();
  const openSidebar = onOpenSidebar ?? chrome.onOpenSidebar;
  return (
    <header className="sticky top-0 z-30 flex min-h-16 items-center gap-3 border-b border-border bg-card/95 backdrop-blur-md shadow-[0_1px_0_0_hsl(var(--border))] px-4 sm:px-6">
      {openSidebar && (
        <button
          onClick={openSidebar}
          className="md:hidden flex h-9 w-9 shrink-0 items-center justify-center rounded-lg border border-border bg-card text-muted-foreground hover:bg-accent hover:text-accent-foreground transition-colors"
          aria-label="Open menu"
        >
          <Menu className="h-4 w-4" />
        </button>
      )}

      <div className="grid grid-cols-[minmax(0,1fr)_auto] flex-1 items-center gap-4 sm:flex sm:items-center sm:justify-between">
        <div className="flex min-w-0 items-center gap-3">
          {icon && (
            <div
              className={cn(
                "hidden sm:flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-gradient-to-br from-primary/10 to-primary/5 text-primary border border-primary/15",
                iconClassName,
              )}
            >
              {icon}
            </div>
          )}
          <div className="min-w-0">
            <h1 className="font-display text-[15px] font-bold text-foreground tracking-tight truncate">
              {title}
            </h1>
            {description && (
              <p className="text-xs text-muted-foreground mt-0.5 line-clamp-1">{description}</p>
            )}
          </div>
        </div>

        <div className="flex items-center gap-2 shrink-0">
          <div className="hidden lg:flex items-center gap-2 rounded-lg border border-border bg-muted/40 px-3 py-1.5 text-xs text-muted-foreground min-w-[240px]">
            <Search className="h-3.5 w-3.5" />
            <span>Search…</span>
            <kbd className="ml-auto rounded border border-border bg-background px-1.5 py-0.5 text-[10px] font-mono">
              ⌘K
            </kbd>
          </div>
          <ThemeToggle />
          <button className="hidden sm:flex h-9 w-9 items-center justify-center rounded-lg border border-border bg-card text-muted-foreground hover:bg-accent hover:text-accent-foreground transition-colors relative">
            <Bell className="h-4 w-4" />
            <span className="absolute top-1.5 right-1.5 h-1.5 w-1.5 rounded-full bg-primary" />
          </button>
          {actions}
        </div>
      </div>
    </header>
  );
}

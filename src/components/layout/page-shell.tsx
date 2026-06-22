import type { LucideIcon } from "lucide-react";

interface PageShellProps {
  icon: LucideIcon;
  label: string;
  description: string;
  children?: React.ReactNode;
}

export function PageShell({ icon: Icon, label, description, children }: PageShellProps) {
  return (
    <div className="flex h-full min-h-[480px] flex-col items-center justify-center px-6 py-16 text-center">
      <div className="relative">
        <div className="absolute inset-0 -m-4 rounded-3xl bg-gradient-to-br from-primary/20 to-transparent blur-2xl opacity-60" />
        <div className="relative flex h-20 w-20 items-center justify-center rounded-2xl bg-gradient-to-br from-card to-muted border border-border shadow-elegant">
          <Icon className="h-9 w-9 text-primary" strokeWidth={1.5} />
        </div>
      </div>
      <h2 className="mt-6 font-display text-lg font-semibold text-foreground tracking-tight">
        {label}
      </h2>
      <p className="mt-2 max-w-md text-sm text-muted-foreground leading-relaxed">{description}</p>
      {children && <div className="mt-6">{children}</div>}
      <div className="mt-8 inline-flex items-center gap-2 rounded-full border border-border bg-muted/50 px-3 py-1 text-[11px] text-muted-foreground">
        <span className="h-1.5 w-1.5 rounded-full bg-warning animate-pulse" />
        Coming in next milestone
      </div>
    </div>
  );
}

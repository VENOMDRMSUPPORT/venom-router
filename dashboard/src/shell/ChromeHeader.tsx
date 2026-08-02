import type { ReactNode } from "react";
import { Icon } from "@venom/design-system/icons";

export interface ChromeHeaderProps {
  /** Current page title (from the shared nav metadata). */
  title: string;
  /** One-line muted subtitle under the title. */
  subtitle: string;
  /** DS icon glyph for the page, rendered in a squared tile. */
  icon: string;
  /** Right-side action cluster (search, theme toggle, notifications, owner menu). */
  children?: ReactNode;
}

export default function ChromeHeader(props: ChromeHeaderProps) {
  const { title, subtitle, icon, children } = props;

  return (
    <header className="vn-shell-topbar">
      <div className="flex min-w-0 flex-1 items-center gap-3">
        <div
          className="flex h-9 w-9 flex-none items-center justify-center rounded-lg border border-border-default bg-surface-secondary text-text-primary shadow-xs"
          aria-hidden="true"
        >
          <Icon name={icon} size={18} />
        </div>
        <div className="min-w-0">
          <h1 className="truncate text-sm sm:text-base font-bold tracking-tight text-text-primary leading-snug">
            {title}
          </h1>
          <p className="truncate text-xs text-text-muted leading-none mt-0.5">{subtitle}</p>
        </div>
      </div>
      <div className="flex flex-none items-center gap-2 sm:gap-3">{children}</div>
    </header>
  );
}

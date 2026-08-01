import type { ReactNode } from "react";
import { Icon } from "@venom/design-system/icons";

export interface ChromeHeaderProps {
  /** Current page title (from the shared nav metadata). */
  title: string;
  /** One-line muted subtitle under the title. */
  subtitle: string;
  /** DS icon glyph for the page, rendered accent-tinted in a squared tile. */
  icon: string;
  /** Right-side action cluster (theme toggle, notifications, owner menu). */
  children?: ReactNode;
}

/**
 * The shared 70px top-bar chrome (legacy console header pattern, ported
 * onto our DS tokens): LEFT — a squared icon tile (soft surface, default
 * border, the current page's icon tinted with the live accent) beside the
 * page title and its one-line muted description; RIGHT — whatever action
 * cluster the shell passes as children. Every page's title/subtitle/icon
 * flows through here from the single nav metadata source (see nav.ts) —
 * pages never render their own top-bar chrome.
 */
export default function ChromeHeader(props: ChromeHeaderProps) {
  const { title, subtitle, icon, children } = props;

  return (
    <header className="vn-shell-topbar">
      <div className="flex min-w-0 flex-1 items-center gap-3">
        <div
          className="flex h-10 w-10 flex-none items-center justify-center rounded-lg border border-border-default bg-surface-secondary text-accent-text"
          aria-hidden="true"
        >
          <Icon name={icon} size={20} />
        </div>
        <div className="min-w-0">
          <h1 className="truncate text-sm font-semibold tracking-tight text-text-primary">
            {title}
          </h1>
          <p className="truncate vn-caption">{subtitle}</p>
        </div>
      </div>
      <div className="flex flex-none items-center gap-2">{children}</div>
    </header>
  );
}

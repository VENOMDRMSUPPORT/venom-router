import type { MouseEvent } from "react";
import { Icon } from "@venom/design-system/icons";

export interface BrandHeaderProps {
  onNavigate?: (navKey: string) => void;
}

export default function BrandHeader(props: BrandHeaderProps) {
  function handleClick(e: MouseEvent<HTMLAnchorElement>) {
    if (props.onNavigate) {
      e.preventDefault();
      props.onNavigate("overview");
    }
  }

  return (
    <div className="vn-nav-brand select-text">
      <a
        href="#overview"
        onClick={handleClick}
        className="vn-brand-tile group"
        aria-label="Venom Router home"
      >
        <Icon name="route" size={20} className="vn-brand-icon text-accent-text" />
        <span className="vn-brand-status-dot" />
      </a>
      <div className="flex min-w-0 flex-col leading-none">
        <div className="flex items-center gap-1">
          <span className="truncate text-[15.5px] font-extrabold tracking-[0.03em] text-text-primary">
            Venom <span className="text-accent-text font-black drop-shadow-sm">Router</span>
          </span>
        </div>
        <div className="flex items-center gap-1 mt-1">
          <span className="h-1.5 w-1.5 rounded-full bg-status-healthy shadow-[0_0_6px_var(--status-healthy)] animate-pulse flex-none mr-0.5" />
          <span className="truncate font-mono text-[9.2px] font-extrabold tracking-[0.07em] text-text-muted uppercase">
            AI Control Center
          </span>
        </div>
      </div>
    </div>
  );
}

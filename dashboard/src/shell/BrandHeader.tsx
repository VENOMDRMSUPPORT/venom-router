import type { MouseEvent } from "react";
import { Icon } from "@venom/design-system/icons";
import "./shell.css";

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
          {/* Token-scale equivalents of the redesign's hand-tuned values:
              15.5px -> text-lg (16px), 0.03em -> tracking-wide (0.04em). */}
          <span className="truncate text-lg font-extrabold tracking-wide text-text-primary">
            Venom <span className="text-accent-text font-black drop-shadow-sm">Router</span>
          </span>
        </div>
        <div className="flex items-center gap-1 mt-1">
          {/* bg-status-healthy-fg is the real preset class (the redesign's
              `bg-status-healthy` mapped to no token and rendered nothing);
              the glow lives in shell.css since a shadow is not scale
              material. */}
          <span className="h-1.5 w-1.5 rounded-full bg-status-healthy-fg vnd-brand-pulse animate-pulse flex-none mr-0.5" />
          <span className="truncate font-mono text-2xs font-extrabold tracking-wider text-text-muted uppercase">
            AI Control Center
          </span>
        </div>
      </div>
    </div>
  );
}

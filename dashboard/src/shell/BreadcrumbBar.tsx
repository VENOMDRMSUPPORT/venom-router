import type { MouseEvent } from "react";
import { Breadcrumbs, type BreadcrumbItem } from "@venom/design-system/primitives";
import { breadcrumbTrail, type NavItem } from "./nav";

export interface BreadcrumbBarProps {
  /** The active nav item — the trail derives from its group/label. */
  item: NavItem;
  /** Invoked when the root "Dashboard" crumb is activated. */
  onNavigateHome: () => void;
  /** Optional full-trail override (root first). The Providers page uses
   * this so its third segment mirrors the live auth filter ("Dashboard /
   * Providers / OAuth Providers"); everywhere else the trail still derives
   * from the nav metadata. */
  trail?: string[];
}

/**
 * The global breadcrumb bar (legacy console pattern): the full trail sits
 * in one bordered chip under the header — "Dashboard / <Group> / <Page>" —
 * with the current leaf emphasized (the DS Breadcrumbs primitive's own
 * aria-current="page" treatment). Present on every page, driven by the
 * same nav metadata as the header. The shell's nav is state-driven (no
 * router), so anchor activations are intercepted: "Dashboard" navigates
 * home, intermediate group crumbs are inert.
 */
export default function BreadcrumbBar(props: BreadcrumbBarProps) {
  const { item, onNavigateHome } = props;

  const trail = props.trail ?? breadcrumbTrail(item);
  const items: BreadcrumbItem[] = trail.map((label, i) => {
    if (i === trail.length - 1) return { label };
    return i === 0 ? { label, href: "#overview" } : { label };
  });

  function handleClickCapture(event: MouseEvent<HTMLDivElement>) {
    const anchor = (event.target as HTMLElement).closest("a");
    if (!anchor) return;
    event.preventDefault();
    if (anchor.getAttribute("href") === "#overview") onNavigateHome();
  }

  return (
    <div
      className="inline-flex items-center self-start rounded-lg border border-border-default bg-surface-secondary px-3.5 py-1.5 shadow-sm"
      onClickCapture={handleClickCapture}
    >
      <Breadcrumbs items={items} />
    </div>
  );
}

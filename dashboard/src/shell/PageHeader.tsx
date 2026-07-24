import type { ReactNode } from "react";
import { Breadcrumbs, type BreadcrumbItem } from "@venom/design-system/primitives";

export interface PageHeaderProps {
  title: ReactNode;
  description?: ReactNode;
  primaryAction?: ReactNode;
  breadcrumbs?: BreadcrumbItem[];
}

/** The shell's consistent page-header pattern (P2b-UI-001): optional
 * breadcrumbs, a title + description, and an optional primary action —
 * every surface mounted into `vn-shell-content` opens with this. */
export default function PageHeader(props: PageHeaderProps) {
  const { title, description, primaryAction, breadcrumbs } = props;

  return (
    <div className="flex flex-col gap-2">
      {breadcrumbs && breadcrumbs.length > 0 ? <Breadcrumbs items={breadcrumbs} /> : null}
      <div className="flex items-start justify-between gap-4">
        <div className="flex flex-col gap-1">
          <h1 className="vn-display">{title}</h1>
          {description ? <p className="vn-caption">{description}</p> : null}
        </div>
        {primaryAction ? <div className="flex items-center gap-2">{primaryAction}</div> : null}
      </div>
    </div>
  );
}

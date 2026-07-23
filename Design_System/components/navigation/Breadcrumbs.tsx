import * as React from "react";

export interface BreadcrumbItem {
  label: React.ReactNode;
  /** Omit on the last (current-page) item. */
  href?: string;
}

export interface BreadcrumbsProps {
  items?: BreadcrumbItem[];
  className?: string;
}

export function Breadcrumbs(props: BreadcrumbsProps) {
  const { items = [], className = "" } = props;
  return (
    <nav aria-label="Breadcrumb" className={("vn-breadcrumbs " + className).trim()}>
      {items.map((it, i) => {
        const last = i === items.length - 1;
        return (
          <React.Fragment key={i}>
            {i > 0 ? <span className="vn-crumb-sep" aria-hidden="true">/</span> : null}
            {last ? <span aria-current="page">{it.label}</span> : <a href={it.href || "#"}>{it.label}</a>}
          </React.Fragment>
        );
      })}
    </nav>
  );
}

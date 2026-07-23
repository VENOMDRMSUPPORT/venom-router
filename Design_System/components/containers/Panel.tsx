import * as React from "react";

export interface PanelProps {
  title?: React.ReactNode;
  actions?: React.ReactNode;
  children?: React.ReactNode;
  className?: string;
}

export function Panel(props: PanelProps) {
  const { title, actions, children, className = "" } = props;
  return (
    <section className={("vn-panel " + className).trim()}>
      {title != null ? (
        <header className="vn-panel-header">
          <h3 className="vn-title-sub" style={{ margin: 0 }}>{title}</h3>
          {actions ? <div style={{ display: "flex", gap: "var(--space-2)" }}>{actions}</div> : null}
        </header>
      ) : null}
      {children}
    </section>
  );
}

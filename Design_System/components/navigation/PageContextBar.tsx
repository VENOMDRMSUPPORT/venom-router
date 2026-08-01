import * as React from "react";

export interface PageContextBarProps {
  leading: React.ReactNode;
  actions?: React.ReactNode;
  secondary?: React.ReactNode;
  className?: string;
}

export function PageContextBar(props: PageContextBarProps) {
  const { leading, actions, secondary, className = "" } = props;
  return (
    <div className={("vn-page-context " + className).trim()}>
      <div className="vn-page-context-leading">{leading}</div>
      {actions || secondary ? (
        <div className="vn-page-context-end">
          {secondary ? <div className="vn-page-context-secondary">{secondary}</div> : null}
          {actions ? <div className="vn-page-context-actions">{actions}</div> : null}
        </div>
      ) : null}
    </div>
  );
}

import * as React from "react";
import { Icon } from "../icons/Icon";

export type AlertTone = "info" | "warning" | "critical" | "healthy" | "unknown";

const ICONS: Record<AlertTone, string> = { info: "info", warning: "triangle-alert", critical: "circle-x", healthy: "circle-check", unknown: "circle-help" };

export interface AlertProps {
  tone?: AlertTone;
  title?: React.ReactNode;
  code?: React.ReactNode;
  children?: React.ReactNode;
  actions?: React.ReactNode;
  className?: string;
}

export function Alert(props: AlertProps) {
  const { tone = "info", title, code, children, actions, className = "" } = props;
  return (
    <div className={("vn-alert vn-alert--" + tone + " " + className).trim()} role={tone === "critical" ? "alert" : "status"}>
      <Icon name={ICONS[tone]} size={15} />
      <div style={{ flex: 1 }}>
        {title ? <p className="vn-alert-title">{title}</p> : null}
        <div>{children}</div>
        {code ? <div className="vn-alert-code">{code}</div> : null}
        {actions ? <div style={{ display: "flex", gap: "var(--space-2)", marginTop: "var(--space-2)" }}>{actions}</div> : null}
      </div>
    </div>
  );
}

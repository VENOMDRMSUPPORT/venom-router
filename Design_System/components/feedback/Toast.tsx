import * as React from "react";
import { Icon } from "../icons/Icon";

export type ToastTone = "healthy" | "critical" | "info" | "warning";

const ICONS: Record<ToastTone, string> = { healthy: "circle-check", critical: "circle-x", info: "info", warning: "triangle-alert" };

/** A CSS custom-property value assigned to a typed style property — csstype's literal unions (e.g. `fontWeight`) don't include arbitrary `var(...)` strings, so this narrows the assertion to the exact property type instead of widening to `any`. */
const titleStyle: React.CSSProperties = { fontWeight: "var(--font-weight-medium)" as React.CSSProperties["fontWeight"] };

export interface ToastProps {
  tone?: ToastTone;
  title?: React.ReactNode;
  detail?: React.ReactNode;
  onDismiss?: () => void;
  className?: string;
}

export function Toast(props: ToastProps) {
  const { tone = "info", title, detail, onDismiss, className = "" } = props;
  return (
    <div className={("vn-toast vn-toast--" + tone + " " + className).trim()} role="status" aria-live="polite">
      <Icon name={ICONS[tone]} size={15} />
      <div style={{ flex: 1 }}>
        <div style={titleStyle}>{title}</div>
        {detail ? <div className="vn-caption" style={{ marginTop: 2 }}>{detail}</div> : null}
      </div>
      {onDismiss ? <button type="button" className="vn-btn vn-btn--icon vn-btn--ghost vn-btn--sm" aria-label="Dismiss" onClick={onDismiss}><Icon name="x" size={12} /></button> : null}
    </div>
  );
}

export interface ToastStackProps {
  children?: React.ReactNode;
}

export function ToastStack(props: ToastStackProps) { return <div className="vn-toast-stack">{props.children}</div>; }

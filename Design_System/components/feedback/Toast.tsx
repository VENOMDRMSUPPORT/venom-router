import * as React from "react";
import { Icon } from "../icons/Icon";

export type ToastTone = "healthy" | "critical" | "info" | "warning" | "loading" | "custom";
export type ToastPosition = "bottom-right" | "top-right" | "bottom-center" | "top-center" | "bottom-left" | "top-left";

export interface ToastAction {
  label: string;
  onClick: () => void;
}

export interface ToastProps {
  id?: string;
  tone?: ToastTone;
  title?: React.ReactNode;
  detail?: React.ReactNode;
  duration?: number;
  action?: ToastAction;
  dismissible?: boolean;
  onDismiss?: () => void;
  className?: string;
  style?: React.CSSProperties;
}

const TONE_ICONS: Record<ToastTone, string> = {
  healthy: "circle-check",
  critical: "circle-x",
  info: "info",
  warning: "triangle-alert",
  loading: "loader-2",
  custom: "sparkles",
};

export function Toast(props: ToastProps) {
  const {
    tone = "info",
    title,
    detail,
    duration = 4000,
    action,
    dismissible = true,
    onDismiss,
    className = "",
    style,
  } = props;

  const isInfinite = duration === Infinity || duration <= 0;

  return (
    <div
      className={`vn-toast vn-toast--${tone} ${className}`.trim()}
      role={tone === "critical" ? "alert" : "status"}
      aria-live={tone === "critical" ? "assertive" : "polite"}
      style={style}
    >
      <Icon
        name={TONE_ICONS[tone] || "info"}
        size={18}
        className={tone === "loading" ? "vn-spin" : ""}
      />
      <div className="vn-toast-content">
        <div className="vn-toast-title">{title}</div>
        {detail ? <div className="vn-toast-detail">{detail}</div> : null}
      </div>

      {action ? (
        <button
          type="button"
          className="vn-btn vn-btn--sm vn-btn--ghost vn-toast__action"
          onClick={() => {
            action.onClick();
            onDismiss?.();
          }}
        >
          {action.label}
        </button>
      ) : null}

      {dismissible && onDismiss ? (
        <button
          type="button"
          className="vn-btn vn-btn--icon vn-btn--ghost vn-btn--sm"
          aria-label="Dismiss notification"
          onClick={onDismiss}
        >
          <Icon name="x" size={14} />
        </button>
      ) : null}

      {!isInfinite && duration > 0 ? (
        <div
          className="vn-toast__progress-bar"
          style={{ animationDuration: `${duration}ms` }}
        />
      ) : null}
    </div>
  );
}

export interface ToastStackProps {
  children?: React.ReactNode;
  position?: ToastPosition;
  className?: string;
}

export function ToastStack(props: ToastStackProps) {
  const { children, position = "bottom-right", className = "" } = props;
  return (
    <div className={`vn-toast-stack vn-toast-stack--${position} ${className}`.trim()}>
      {children}
    </div>
  );
}

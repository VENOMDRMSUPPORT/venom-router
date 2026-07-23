import * as React from "react";
import { Icon } from "../icons/Icon";

/** The 7 status roles plus the 3 tier accents and the plain brand accent — every value `.vn-badge--*` defines in css/components-core.css. */
export type BadgeTone =
  | "healthy"
  | "degraded"
  | "warning"
  | "critical"
  | "info"
  | "unknown"
  | "inactive"
  | "tier-lite"
  | "tier-pro"
  | "tier-max"
  | "accent";

export interface BadgeProps {
  tone?: BadgeTone;
  icon?: string;
  dot?: boolean;
  outline?: boolean;
  mono?: boolean;
  children?: React.ReactNode;
  className?: string;
  title?: string;
}

export function Badge(props: BadgeProps) {
  const { tone = "inactive", icon, dot = false, outline = false, mono = false, children, className = "", title } = props;
  const cls = ["vn-badge", "vn-badge--" + tone, dot ? "vn-badge--dot" : "", outline ? "vn-badge--outline" : "", mono ? "vn-badge--mono" : "", className].filter(Boolean).join(" ");
  return <span className={cls} title={title}>{icon ? <Icon name={icon} size={12} /> : null}{children}</span>;
}

/** The 7 status roles StatusBadge can render — narrower than the full BadgeTone (no tier/accent). */
export type StatusBadgeStatus = "healthy" | "degraded" | "warning" | "critical" | "info" | "unknown" | "inactive";

export interface StatusBadgeProps {
  status: StatusBadgeStatus;
  label?: React.ReactNode;
  className?: string;
  title?: string;
}

/** StatusBadge — Badge preconfigured from the semantic status roles, icon enforced. */
export function StatusBadge(props: StatusBadgeProps) {
  const { status, label, className = "", title } = props;
  const icons: Record<StatusBadgeStatus, string> = { healthy: "circle-check", degraded: "triangle-alert", warning: "triangle-alert", critical: "circle-x", info: "info", unknown: "circle-help", inactive: "pause" };
  return <Badge tone={status} icon={icons[status] || "circle-help"} className={className} title={title}>{label != null ? label : status}</Badge>;
}

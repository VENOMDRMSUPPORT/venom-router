import * as React from "react";
/** The 7 status roles plus the 3 tier accents and the plain brand accent — every value `.vn-badge--*` defines in css/components-core.css. */
export type BadgeTone = "healthy" | "degraded" | "warning" | "critical" | "info" | "unknown" | "inactive" | "tier-lite" | "tier-pro" | "tier-max" | "accent";
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
export declare function Badge(props: BadgeProps): React.JSX.Element;
/** The 7 status roles StatusBadge can render — narrower than the full BadgeTone (no tier/accent). */
export type StatusBadgeStatus = "healthy" | "degraded" | "warning" | "critical" | "info" | "unknown" | "inactive";
export interface StatusBadgeProps {
    status: StatusBadgeStatus;
    label?: React.ReactNode;
    className?: string;
    title?: string;
}
/** StatusBadge — Badge preconfigured from the semantic status roles, icon enforced. */
export declare function StatusBadge(props: StatusBadgeProps): React.JSX.Element;

import * as React from "react";
export interface CardProps extends React.HTMLAttributes<HTMLDivElement> {
    padded?: boolean;
    interactive?: boolean;
    selected?: boolean;
}
export declare function Card(props: CardProps): React.JSX.Element;
export type StatusTone = "healthy" | "degraded" | "warning" | "critical" | "info" | "unknown" | "inactive";
export interface StatCardProps {
    label: React.ReactNode;
    value: React.ReactNode;
    meta?: React.ReactNode;
    tone?: StatusTone;
    icon?: string;
    className?: string;
}
export declare function StatCard(props: StatCardProps): React.JSX.Element;

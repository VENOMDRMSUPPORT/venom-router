import * as React from "react";
export interface TimelineItem {
    title: React.ReactNode;
    detail?: React.ReactNode;
    time?: React.ReactNode;
    tone?: "healthy" | "critical" | "warning" | "accent";
}
export interface TimelineProps {
    items?: TimelineItem[];
    className?: string;
}
export declare function Timeline(props: TimelineProps): React.JSX.Element;

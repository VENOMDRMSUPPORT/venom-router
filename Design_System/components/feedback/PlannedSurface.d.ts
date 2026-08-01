import * as React from "react";
export interface PlannedSurfaceProps {
    icon?: string;
    eyebrow?: React.ReactNode;
    title: React.ReactNode;
    description: React.ReactNode;
    statusLabel?: React.ReactNode;
    note?: React.ReactNode;
    className?: string;
}
export declare function PlannedSurface(props: PlannedSurfaceProps): React.JSX.Element;

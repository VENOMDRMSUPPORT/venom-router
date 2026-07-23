import * as React from "react";
export type BannerTone = "info" | "warning" | "critical";
export interface BannerProps {
    tone?: BannerTone;
    children?: React.ReactNode;
    actions?: React.ReactNode;
    className?: string;
}
export declare function Banner(props: BannerProps): React.JSX.Element;

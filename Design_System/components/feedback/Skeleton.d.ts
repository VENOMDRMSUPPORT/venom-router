import * as React from "react";
export interface SkeletonProps {
    width?: string | number;
    height?: string | number;
    className?: string;
    style?: React.CSSProperties;
}
export declare function Skeleton(props: SkeletonProps): React.JSX.Element;

import * as React from "react";
export interface ProgressProps {
    value?: number;
    max?: number;
    label?: string;
    indeterminate?: boolean;
    className?: string;
}
export declare function Progress(props: ProgressProps): React.JSX.Element;

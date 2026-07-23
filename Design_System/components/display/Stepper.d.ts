import * as React from "react";
export interface StepperProps {
    steps?: string[];
    /** Index of the active step; steps before it render as done. */
    current?: number;
    className?: string;
}
export declare function Stepper(props: StepperProps): React.JSX.Element;

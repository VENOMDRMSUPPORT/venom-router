import * as React from "react";
export type SegmentedOption = string | {
    value: string;
    label: React.ReactNode;
};
export interface SegmentedControlProps {
    options?: SegmentedOption[];
    value?: string;
    onChange?: (value: string) => void;
    label?: string;
    className?: string;
}
export declare function SegmentedControl(props: SegmentedControlProps): React.JSX.Element;

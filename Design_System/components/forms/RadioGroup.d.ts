import * as React from "react";
export interface RadioOption {
    value: string;
    label: React.ReactNode;
    description?: React.ReactNode;
    disabled?: boolean;
}
export interface RadioGroupProps {
    name?: string;
    options?: Array<string | RadioOption>;
    value?: string;
    onChange?: (value: string) => void;
    label?: string;
    disabled?: boolean;
    className?: string;
}
export declare function RadioGroup(props: RadioGroupProps): React.JSX.Element;

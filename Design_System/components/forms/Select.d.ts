import * as React from "react";
export interface SelectOption {
    value: string;
    label: React.ReactNode;
    disabled?: boolean;
}
export interface SelectProps extends Omit<React.SelectHTMLAttributes<HTMLSelectElement>, "children"> {
    options?: Array<string | SelectOption>;
    invalid?: boolean;
    /** Pass explicit `<option>` children instead of `options` for full control. */
    children?: React.ReactNode;
}
export declare function Select(props: SelectProps): React.JSX.Element;

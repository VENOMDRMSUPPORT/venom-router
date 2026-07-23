import * as React from "react";
export interface CheckboxProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "type"> {
    label?: React.ReactNode;
    indeterminate?: boolean;
}
export declare function Checkbox(props: CheckboxProps): React.JSX.Element;

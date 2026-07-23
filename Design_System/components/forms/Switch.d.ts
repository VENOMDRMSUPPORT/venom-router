import * as React from "react";
export interface SwitchProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "type"> {
    label?: React.ReactNode;
}
export declare function Switch(props: SwitchProps): React.JSX.Element;

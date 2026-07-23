import * as React from "react";
export interface SliderProps extends React.InputHTMLAttributes<HTMLInputElement> {
    label?: string;
    showValue?: boolean;
    unit?: string;
}
export declare function Slider(props: SliderProps): React.JSX.Element;

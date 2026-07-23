import * as React from "react";
export interface ComboboxProps {
    options?: string[];
    value?: string;
    onChange?: (value: string) => void;
    placeholder?: string;
    disabled?: boolean;
    id?: string;
    className?: string;
}
export declare function Combobox(props: ComboboxProps): React.JSX.Element;

import * as React from "react";
export interface SearchFieldProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "type"> {
    /** Accessible name (this field renders no visible `<label>`). */
    label?: string;
    /** Optional one-step clear action for controlled search fields. */
    onClear?: () => void;
}
export declare function SearchField(props: SearchFieldProps): React.JSX.Element;

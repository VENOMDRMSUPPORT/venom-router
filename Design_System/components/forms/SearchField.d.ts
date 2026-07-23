import * as React from "react";
export interface SearchFieldProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "type"> {
    /** Accessible name (this field renders no visible `<label>`). */
    label?: string;
}
export declare function SearchField(props: SearchFieldProps): React.JSX.Element;

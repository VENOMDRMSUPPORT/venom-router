import * as React from "react";
export interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {
    mono?: boolean;
    invalid?: boolean;
}
/** Forwards its ref to the underlying `<input>` — required for callers that need to focus it imperatively (e.g. `Drawer`'s `initialFocusRef`). */
export declare const Input: React.ForwardRefExoticComponent<InputProps & React.RefAttributes<HTMLInputElement>>;

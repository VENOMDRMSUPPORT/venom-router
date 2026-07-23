import * as React from "react";
export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
    variant?: "primary" | "secondary" | "ghost" | "danger";
    size?: "sm" | "md" | "lg";
    icon?: string;
    loading?: boolean;
}
/** Forwards its ref to the underlying `<button>` — required for callers (DropdownMenu, Popover, Tooltip) that focus/measure the trigger imperatively. */
export declare const Button: React.ForwardRefExoticComponent<ButtonProps & React.RefAttributes<HTMLButtonElement>>;

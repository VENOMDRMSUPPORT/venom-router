import * as React from "react";
export interface IconButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
    icon: string;
    /** Required accessible name — an icon-only control never ships without one. */
    label: string;
    variant?: "ghost" | "secondary" | "danger" | "primary";
    size?: "sm" | "md" | "lg";
}
/** Forwards its ref to the underlying `<button>` — required for callers (DropdownMenu, Popover, Tooltip) that focus/measure the trigger imperatively. */
export declare const IconButton: React.ForwardRefExoticComponent<IconButtonProps & React.RefAttributes<HTMLButtonElement>>;

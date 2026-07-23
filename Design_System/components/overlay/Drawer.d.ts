import * as React from "react";
export interface DrawerProps {
    open: boolean;
    onClose?: () => void;
    title?: React.ReactNode;
    description?: React.ReactNode;
    children?: React.ReactNode;
    footer?: React.ReactNode;
    wide?: boolean;
    /** When `false`, Escape and the scrim no longer close the drawer — the caller must provide an explicit action (matches AlertDialog's non-dismissible contract). Defaults to `true`. */
    dismissible?: boolean;
    /** Element to focus when the drawer opens. Falls back to the first focusable descendant, then the drawer container itself if it has no focusable content. */
    initialFocusRef?: React.RefObject<HTMLElement>;
    className?: string;
}
/**
 * Drawer — a right-edge panel dialog. Moves focus in on open (initialFocusRef, else
 * first focusable, else itself), traps Tab/Shift+Tab, closes on Escape (unless
 * `dismissible={false}`), and restores focus to the exact opener on close. Participates
 * in the shared overlay stack so a nested Dialog/AlertDialog opened from inside a Drawer
 * takes over Escape/Tab correctly.
 */
export declare function Drawer(props: DrawerProps): React.JSX.Element;
/** Sheet — alias of Drawer for detail-inspection surfaces. Same component, same behavior. */
export declare function Sheet(props: DrawerProps): React.JSX.Element;

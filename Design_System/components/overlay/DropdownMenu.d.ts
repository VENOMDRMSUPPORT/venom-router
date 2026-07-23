import * as React from "react";
export interface DropdownMenuItem {
    type?: "item" | "separator" | "label";
    label?: React.ReactNode;
    icon?: string;
    kbd?: string;
    danger?: boolean;
    disabled?: boolean;
    onSelect?: () => void;
}
export interface DropdownMenuProps {
    /** The trigger element. Receives `onClick`/`onKeyDown`/`aria-expanded`/`aria-haspopup` — any handlers already on it are called first. */
    trigger: React.ReactElement;
    items: DropdownMenuItem[];
    align?: "start" | "end";
    className?: string;
}
/**
 * DropdownMenu — real DOM focus movement (roving tabindex), not a painted active index.
 * ArrowDown/ArrowUp/Home/End move focus among enabled items; Enter/Space activate (native
 * button behavior); Escape closes and restores focus to the trigger; Tab closes and lets
 * focus continue to the next widget (WAI-ARIA menu pattern). Disabled items are skipped by
 * every navigation path. Single-character typeahead (no buffering) jumps to the next item
 * whose label starts with the typed letter.
 */
export declare function DropdownMenu(props: DropdownMenuProps): React.JSX.Element;
export interface ContextMenuItem {
    type?: "item" | "separator";
    label?: React.ReactNode;
    icon?: string;
    danger?: boolean;
    onSelect?: () => void;
}
export interface ContextMenuProps {
    items: ContextMenuItem[];
    children?: React.ReactNode;
}
/** ContextMenu — same menu contract, opened at the pointer via right-click on its child. */
export declare function ContextMenu(props: ContextMenuProps): React.JSX.Element;

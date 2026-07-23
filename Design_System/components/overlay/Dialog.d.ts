import * as React from "react";
export interface DialogProps {
    open: boolean;
    onClose?: () => void;
    title?: React.ReactNode;
    description?: React.ReactNode;
    children?: React.ReactNode;
    footer?: React.ReactNode;
    wide?: boolean;
    dismissible?: boolean;
    initialFocusRef?: React.RefObject<HTMLElement>;
    className?: string;
}
/** Dialog — a centered modal. Same focus/overlay contract as Drawer (see overlay-stack.ts): moves focus in, traps Tab, closes on Escape (unless `dismissible={false}`), restores focus to the opener, participates in the shared overlay stack for correct nested-overlay behavior. */
export declare function Dialog(props: DialogProps): React.JSX.Element;
export interface AlertDialogProps {
    open: boolean;
    title?: React.ReactNode;
    description?: React.ReactNode;
    confirmLabel?: React.ReactNode;
    cancelLabel?: React.ReactNode;
    danger?: boolean;
    onConfirm?: () => void;
    onCancel?: () => void;
    children?: React.ReactNode;
}
/** AlertDialog — blocking confirmation; always non-dismissible (no scrim/Escape dismiss), an explicit choice is required. */
export declare function AlertDialog(props: AlertDialogProps): React.JSX.Element;

import * as React from "react";
export type ToastTone = "healthy" | "critical" | "info" | "warning" | "loading" | "custom";
export type ToastPosition = "bottom-right" | "top-right" | "bottom-center" | "top-center" | "bottom-left" | "top-left";
export interface ToastAction {
    label: string;
    onClick: () => void;
}
export interface ToastProps {
    id?: string;
    tone?: ToastTone;
    title?: React.ReactNode;
    detail?: React.ReactNode;
    duration?: number;
    action?: ToastAction;
    dismissible?: boolean;
    onDismiss?: () => void;
    className?: string;
    style?: React.CSSProperties;
}
export declare function Toast(props: ToastProps): React.JSX.Element;
export interface ToastStackProps {
    children?: React.ReactNode;
    position?: ToastPosition;
    className?: string;
}
export declare function ToastStack(props: ToastStackProps): React.JSX.Element;

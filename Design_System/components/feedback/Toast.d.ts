import * as React from "react";
export type ToastTone = "healthy" | "critical" | "info" | "warning";
export interface ToastProps {
    tone?: ToastTone;
    title?: React.ReactNode;
    detail?: React.ReactNode;
    onDismiss?: () => void;
    className?: string;
}
export declare function Toast(props: ToastProps): React.JSX.Element;
export interface ToastStackProps {
    children?: React.ReactNode;
}
export declare function ToastStack(props: ToastStackProps): React.JSX.Element;

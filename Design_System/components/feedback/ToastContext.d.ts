import * as React from "react";
import { ToastPosition, ToastTone, ToastAction } from "./Toast";
export interface ToastOptions {
    id?: string;
    tone?: ToastTone;
    title?: React.ReactNode;
    detail?: React.ReactNode;
    duration?: number;
    action?: ToastAction;
    dismissible?: boolean;
    onDismiss?: () => void;
    position?: ToastPosition;
}
export interface ToastItem extends ToastOptions {
    id: string;
    tone: ToastTone;
    createdAt: number;
}
type ToastListener = (toasts: ToastItem[]) => void;
declare class ToastEventManager {
    private toasts;
    private listeners;
    private maxToasts;
    subscribe(listener: ToastListener): () => void;
    private notify;
    show(title: React.ReactNode, options?: ToastOptions): string;
    dismiss(id?: string): void;
}
export declare const toastManager: ToastEventManager;
export declare const toast: {
    success: (title: React.ReactNode, options?: ToastOptions) => string;
    danger: (title: React.ReactNode, options?: ToastOptions) => string;
    warning: (title: React.ReactNode, options?: ToastOptions) => string;
    info: (title: React.ReactNode, options?: ToastOptions) => string;
    loading: (title: React.ReactNode, options?: ToastOptions) => string;
    custom: (title: React.ReactNode, options?: ToastOptions) => string;
    promise: <T>(promise: Promise<T>, msgs: {
        loading: React.ReactNode;
        success: React.ReactNode;
        error: React.ReactNode;
    }, options?: ToastOptions) => Promise<T>;
    dismiss: (id?: string) => void;
    clearAll: () => void;
};
export interface ToastProviderProps {
    children?: React.ReactNode;
    position?: ToastPosition;
}
export declare function ToastProvider({ children, position }: ToastProviderProps): React.JSX.Element;
export declare function useToast(): {
    success: (title: React.ReactNode, options?: ToastOptions) => string;
    danger: (title: React.ReactNode, options?: ToastOptions) => string;
    warning: (title: React.ReactNode, options?: ToastOptions) => string;
    info: (title: React.ReactNode, options?: ToastOptions) => string;
    loading: (title: React.ReactNode, options?: ToastOptions) => string;
    custom: (title: React.ReactNode, options?: ToastOptions) => string;
    promise: <T>(promise: Promise<T>, msgs: {
        loading: React.ReactNode;
        success: React.ReactNode;
        error: React.ReactNode;
    }, options?: ToastOptions) => Promise<T>;
    dismiss: (id?: string) => void;
    clearAll: () => void;
};
export {};

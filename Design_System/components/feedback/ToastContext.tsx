import * as React from "react";
import { Toast, ToastStack, ToastPosition, ToastTone, ToastAction } from "./Toast";

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

class ToastEventManager {
  private toasts: ToastItem[] = [];
  private listeners: Set<ToastListener> = new Set();
  private maxToasts = 5;

  subscribe(listener: ToastListener) {
    this.listeners.add(listener);
    listener(this.toasts);
    return () => {
      this.listeners.delete(listener);
    };
  }

  private notify() {
    this.listeners.forEach((l) => l([...this.toasts]));
  }

  show(title: React.ReactNode, options: ToastOptions = {}): string {
    const id = options.id || `toast-${Math.random().toString(36).substring(2, 9)}`;
    const tone = options.tone || "info";
    const newItem: ToastItem = {
      ...options,
      id,
      title,
      tone,
      createdAt: Date.now(),
    };

    const existingIndex = this.toasts.findIndex((t) => t.id === id);
    if (existingIndex >= 0) {
      this.toasts[existingIndex] = newItem;
    } else {
      this.toasts = [newItem, ...this.toasts].slice(0, this.maxToasts);
    }

    this.notify();

    const duration = options.duration ?? 4000;
    if (duration > 0 && duration !== Infinity) {
      setTimeout(() => {
        this.dismiss(id);
      }, duration);
    }

    return id;
  }

  dismiss(id?: string) {
    if (!id) {
      this.toasts = [];
    } else {
      const target = this.toasts.find((t) => t.id === id);
      target?.onDismiss?.();
      this.toasts = this.toasts.filter((t) => t.id !== id);
    }
    this.notify();
  }
}

export const toastManager = new ToastEventManager();

export const toast = {
  success: (title: React.ReactNode, options?: ToastOptions) =>
    toastManager.show(title, { ...options, tone: "healthy" }),
  danger: (title: React.ReactNode, options?: ToastOptions) =>
    toastManager.show(title, { ...options, tone: "critical" }),
  warning: (title: React.ReactNode, options?: ToastOptions) =>
    toastManager.show(title, { ...options, tone: "warning" }),
  info: (title: React.ReactNode, options?: ToastOptions) =>
    toastManager.show(title, { ...options, tone: "info" }),
  loading: (title: React.ReactNode, options?: ToastOptions) =>
    toastManager.show(title, { ...options, tone: "loading", duration: options?.duration ?? Infinity }),
  custom: (title: React.ReactNode, options?: ToastOptions) =>
    toastManager.show(title, options),
  promise: async <T,>(
    promise: Promise<T>,
    msgs: { loading: React.ReactNode; success: React.ReactNode; error: React.ReactNode },
    options?: ToastOptions
  ): Promise<T> => {
    const id = toast.loading(msgs.loading, options);
    try {
      const result = await promise;
      toast.success(msgs.success, { ...options, id, duration: options?.duration ?? 4000 });
      return result;
    } catch (err) {
      toast.danger(msgs.error, { ...options, id, duration: options?.duration ?? 5000 });
      throw err;
    }
  },
  dismiss: (id?: string) => toastManager.dismiss(id),
  clearAll: () => toastManager.dismiss(),
};

export interface ToastProviderProps {
  children?: React.ReactNode;
  position?: ToastPosition;
}

export function ToastProvider({ children, position = "bottom-right" }: ToastProviderProps) {
  const [toasts, setToasts] = React.useState<ToastItem[]>([]);

  React.useEffect(() => {
    return toastManager.subscribe(setToasts);
  }, []);

  return (
    <>
      {children}
      <ToastStack position={position}>
        {toasts.map((t) => (
          <Toast
            key={t.id}
            id={t.id}
            tone={t.tone}
            title={t.title}
            detail={t.detail}
            duration={t.duration}
            action={t.action}
            dismissible={t.dismissible}
            onDismiss={() => toastManager.dismiss(t.id)}
          />
        ))}
      </ToastStack>
    </>
  );
}

export function useToast() {
  return toast;
}

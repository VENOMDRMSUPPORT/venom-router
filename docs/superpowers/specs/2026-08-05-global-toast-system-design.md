# Design Specification: Global Toast System

## Overview
This document specifies the architecture, visual design, and integration guidelines for the unified, world-class Global Toast System in `@venom/design-system` and its application across all dashboard pages in `venom-router`.

## Goals & Functional Requirements
- **6 Tone States**: Support `healthy` (success), `critical` (danger/error), `warning` (alert), `info`, `loading` (spinner), and `custom`/`promise` states.
- **Glassmorphic Aesthetic & Dual Theme Support**: High-contrast, backdrop-blur elevation glassmorphism optimized for both `venom-dark` and `venom-light` themes.
- **Imperative & Hook API**: Provide standalone helper methods (`toast.success()`, `toast.danger()`, `toast.warning()`, `toast.info()`, `toast.loading()`, `toast.promise()`, `toast.custom()`, `toast.dismiss()`) as well as the React `useToast()` hook.
- **Interactive Features**: Animated timer progress bar (countdown bar), pause timer on hover, action button support (e.g. "Undo", "View Details"), manual dismiss button, stacked animation queue (max 5 toasts).
- **Flexible Positioning**: Default placement at `bottom-right` with configurable overrides (`top-right`, `bottom-center`, `top-center`, `bottom-left`, `top-left`).
- **Global Integration**: Mounted at the root of `dashboard` (`AppShell.tsx` / `App.tsx`) so all routes and components can trigger toasts out-of-the-box.
- **Accessibility**: Full ARIA compliance (`role="status"`, `role="alert"`, `aria-live="polite"` / `"assertive"`), keyboard escape dismiss, and reduced-motion considerations.

## Visual Design & CSS Styling Specs
### Class Hierarchy & Styling Tokens
- `.vn-toast`: Base container with flex row layout, glassmorphism (`backdrop-filter: blur(12px)`), theme tokens (`--toast-bg`, `--toast-fg`, `--toast-border`, `--toast-shadow`), left tone border bar (`4px`).
- `.vn-toast--healthy`: Green tone bar (`var(--status-healthy-fg)`).
- `.vn-toast--critical`: Red tone bar (`var(--status-critical-fg)`).
- `.vn-toast--warning`: Amber tone bar (`var(--status-warning-fg)`).
- `.vn-toast--info`: Blue tone bar (`var(--status-info-fg)`).
- `.vn-toast--loading`: Spinner indicator with accent bar.
- `.vn-toast__progress-bar`: Linear shrinking progress bar attached to the bottom edge.
- `.vn-toast__action`: Styled button for quick user actions inside the toast.
- `.vn-toast-stack`: Fixed container supporting multiple screen positions (`bottom-right`, `top-right`, etc.), flex column layout, gap `var(--space-2)`, `z-index: var(--z-toast)`.

## API & Data Structures
```ts
export type ToastTone = "healthy" | "critical" | "warning" | "info" | "loading" | "custom";
export type ToastPosition = "bottom-right" | "top-right" | "bottom-center" | "top-center" | "bottom-left" | "top-left";

export interface ToastAction {
  label: string;
  onClick: () => void;
}

export interface ToastOptions {
  id?: string;
  tone?: ToastTone;
  title?: React.ReactNode;
  detail?: React.ReactNode;
  duration?: number; // default 4000ms, Infinity for manual dismiss
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
```

### Imperative Toast API
```ts
export const toast = {
  success: (title: React.ReactNode, options?: ToastOptions) => string,
  danger: (title: React.ReactNode, options?: ToastOptions) => string,
  warning: (title: React.ReactNode, options?: ToastOptions) => string,
  info: (title: React.ReactNode, options?: ToastOptions) => string,
  loading: (title: React.ReactNode, options?: ToastOptions) => string,
  custom: (title: React.ReactNode, options?: ToastOptions) => string,
  promise: <T>(
    promise: Promise<T>,
    msgs: { loading: React.ReactNode; success: React.ReactNode; error: React.ReactNode },
    options?: ToastOptions
  ) => Promise<T>,
  dismiss: (id?: string) => void,
  clearAll: () => void,
};
```

## Component Architecture
1. `Design_System/components/feedback/Toast.tsx`: Visual component for rendering individual toasts with progress bar, action buttons, tone icons, and `ToastStack`.
2. `Design_System/components/feedback/ToastContext.tsx`: React Context, `ToastProvider`, `useToast()` hook, and global listener subscription for `toast.*` calls.
3. `Design_System/css/components-core.css`: CSS styles, backdrop blur glassmorphic tokens, linear timer keyframes, stacked slide-in/out animations.
4. `Design_System/themes/venom-dark.css` & `venom-light.css`: Theme token definition for `--toast-bg`, `--toast-border`, `--toast-shadow`, `--toast-fg`.
5. `dashboard/src/shell/AppShell.tsx`: Root-level mounting of `ToastProvider`.

## Verification & Testing Plan
- **Unit Tests**: Test `Toast.tsx` and `ToastContext.tsx` in `@venom/design-system` for item addition, auto-dismiss timers, action button clicks, and clear all.
- **E2E & Theme Tests**: Run Playwright suite to ensure glassmorphic rendering across `venom-dark` and `venom-light` themes.

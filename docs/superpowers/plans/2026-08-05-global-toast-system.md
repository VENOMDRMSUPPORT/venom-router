# Global Toast System Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create a world-class Global Toast notification system in `@venom/design-system` with 6 tone states, glassmorphic dual-theme styling, countdown progress timer, action button support, and mount it globally across all dashboard pages in `venom-router`.

**Architecture:** Extend `@venom/design-system` with a flexible Toast component, a React Context Provider with global event emitter (`toast.success()`, `toast.danger()`, `toast.warning()`, `toast.info()`, `toast.loading()`, `toast.promise()`), enhanced CSS tokens, and root integration in `dashboard/src/shell/AppShell.tsx`.

**Tech Stack:** React 18, TypeScript, CSS Custom Properties (Tokens), Vite, Vitest / Playwright.

## Global Constraints
- ALL project files (code, comments, docstrings, JSON, markdown plan files) MUST strictly use ENGLISH ONLY.
- Dual theme support (`venom-dark` and `venom-light`) with CSS token variables.
- Max 5 stacked toasts, default placement `bottom-right`.

---

### Task 1: Enhance CSS Design Tokens & Glassmorphic Toast Styles

**Files:**
- Modify: `Design_System/tokens/components.css`
- Modify: `Design_System/themes/venom-dark.css`
- Modify: `Design_System/themes/venom-light.css`
- Modify: `Design_System/css/components-core.css:337-352`

**Interfaces:**
- Consumes: CSS theme tokens (`--status-healthy-fg`, `--status-critical-fg`, `--status-warning-fg`, `--status-info-fg`)
- Produces: Toast layout & animation classes (`.vn-toast`, `.vn-toast--healthy`, `.vn-toast--critical`, `.vn-toast--warning`, `.vn-toast--info`, `.vn-toast--loading`, `.vn-toast__progress-bar`, `.vn-toast-stack`)

- [ ] **Step 1: Define Toast CSS Tokens in `venom-dark.css` and `venom-light.css`**

In `Design_System/themes/venom-dark.css`:
```css
  --toast-bg: rgba(18, 22, 31, 0.88);
  --toast-fg: #f3f4f6;
  --toast-border: rgba(255, 255, 255, 0.12);
  --toast-shadow: 0 12px 32px -4px rgba(0, 0, 0, 0.5), 0 4px 12px rgba(0, 0, 0, 0.3);
```

In `Design_System/themes/venom-light.css`:
```css
  --toast-bg: rgba(255, 255, 255, 0.92);
  --toast-fg: #111827;
  --toast-border: rgba(0, 0, 0, 0.1);
  --toast-shadow: 0 12px 32px -4px rgba(0, 0, 0, 0.12), 0 4px 12px rgba(0, 0, 0, 0.06);
```

- [ ] **Step 2: Update `components-core.css` with Glassmorphism, Accent Bar, and Progress Timer Keyframes**

In `Design_System/css/components-core.css`:
```css
.vn-toast {
  position: relative;
  display: flex;
  gap: var(--space-3);
  align-items: flex-start;
  width: 380px;
  max-width: calc(100vw - var(--space-8));
  padding: var(--space-3) var(--space-4);
  background: var(--toast-bg);
  color: var(--toast-fg);
  border: var(--border-hairline) solid var(--toast-border);
  border-radius: var(--radius-lg);
  box-shadow: var(--toast-shadow);
  font-size: var(--font-size-sm);
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
  overflow: hidden;
  transition: all var(--duration-base) var(--easing-emphasized);
  animation: vn-toast-in var(--duration-base) var(--easing-emphasized);
}

/* Tone Accent Left Bars */
.vn-toast::before {
  content: "";
  position: absolute;
  top: 0;
  left: 0;
  bottom: 0;
  width: 4px;
  background: currentColor;
  opacity: 0.9;
}

.vn-toast--healthy { color: var(--status-healthy-fg); }
.vn-toast--critical { color: var(--status-critical-fg); }
.vn-toast--warning { color: var(--status-warning-fg); }
.vn-toast--info { color: var(--status-info-fg); }
.vn-toast--loading { color: var(--status-info-fg); }

.vn-toast .vn-toast-content {
  flex: 1;
  color: var(--toast-fg);
}

.vn-toast .vn-toast-title {
  font-weight: var(--font-weight-medium);
  line-height: var(--font-line-height-snug);
}

.vn-toast .vn-toast-detail {
  font-size: var(--font-size-xs);
  color: var(--color-fg-muted);
  margin-top: 2px;
}

/* Progress Timer Bar */
.vn-toast__progress-bar {
  position: absolute;
  bottom: 0;
  left: 0;
  height: 3px;
  background: currentColor;
  opacity: 0.6;
  width: 100%;
  transform-origin: left;
  animation: vn-toast-shrink linear forwards;
}

.vn-toast:hover .vn-toast__progress-bar {
  animation-play-state: paused;
}

@keyframes vn-toast-shrink {
  from { transform: scaleX(1); }
  to { transform: scaleX(0); }
}

.vn-toast-stack {
  position: fixed;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  z-index: var(--z-toast);
  pointer-events: none;
}

.vn-toast-stack > * {
  pointer-events: auto;
}

.vn-toast-stack--bottom-right { bottom: var(--space-4); right: var(--space-4); align-items: flex-end; }
.vn-toast-stack--top-right { top: var(--space-4); right: var(--space-4); align-items: flex-end; }
.vn-toast-stack--bottom-center { bottom: var(--space-4); left: 50%; transform: translateX(-50%); align-items: center; }
.vn-toast-stack--top-center { top: var(--space-4); left: 50%; transform: translateX(-50%); align-items: center; }
.vn-toast-stack--bottom-left { bottom: var(--space-4); left: var(--space-4); align-items: flex-start; }
.vn-toast-stack--top-left { top: var(--space-4); left: var(--space-4); align-items: flex-start; }
```

- [ ] **Step 3: Commit CSS & Token Changes**

```bash
git add Design_System/tokens/components.css Design_System/themes/venom-dark.css Design_System/themes/venom-light.css Design_System/css/components-core.css
git commit -m "feat(ds): add glassmorphic toast design tokens and keyframe animation styles"
```

---

### Task 2: Implement Toast & ToastStack Components in Design System

**Files:**
- Modify: `Design_System/components/feedback/Toast.tsx`
- Modify: `Design_System/components/feedback/Toast.d.ts`

**Interfaces:**
- Consumes: Icon component from `Design_System/components/icons/Icon`
- Produces: `Toast` and `ToastStack` React components supporting tones (`healthy`, `critical`, `warning`, `info`, `loading`, `custom`), action buttons, progress bars, pause on hover.

- [ ] **Step 1: Update `Toast.tsx` with Tone Icons, Actions, Progress Bar, and Dismiss Handlers**

```tsx
import * as React from "react";
import { Icon } from "../icons/Icon";

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

const TONE_ICONS: Record<ToastTone, string> = {
  healthy: "circle-check",
  critical: "circle-x",
  info: "info",
  warning: "triangle-alert",
  loading: "loader-2",
  custom: "sparkles",
};

export function Toast(props: ToastProps) {
  const {
    tone = "info",
    title,
    detail,
    duration = 4000,
    action,
    dismissible = true,
    onDismiss,
    className = "",
    style,
  } = props;

  const isInfinite = duration === Infinity || duration <= 0;

  return (
    <div
      className={`vn-toast vn-toast--${tone} ${className}`.trim()}
      role={tone === "critical" ? "alert" : "status"}
      aria-live={tone === "critical" ? "assertive" : "polite"}
      style={style}
    >
      <Icon
        name={TONE_ICONS[tone] || "info"}
        size={18}
        className={tone === "loading" ? "vn-spin" : ""}
      />
      <div className="vn-toast-content">
        <div className="vn-toast-title">{title}</div>
        {detail ? <div className="vn-toast-detail">{detail}</div> : null}
      </div>

      {action ? (
        <button
          type="button"
          className="vn-btn vn-btn--sm vn-btn--ghost vn-toast__action"
          onClick={() => {
            action.onClick();
            onDismiss?.();
          }}
        >
          {action.label}
        </button>
      ) : null}

      {dismissible && onDismiss ? (
        <button
          type="button"
          className="vn-btn vn-btn--icon vn-btn--ghost vn-btn--sm"
          aria-label="Dismiss notification"
          onClick={onDismiss}
        >
          <Icon name="x" size={14} />
        </button>
      ) : null}

      {!isInfinite && duration > 0 ? (
        <div
          className="vn-toast__progress-bar"
          style={{ animationDuration: `${duration}ms` }}
        />
      ) : null}
    </div>
  );
}

export interface ToastStackProps {
  children?: React.ReactNode;
  position?: ToastPosition;
  className?: string;
}

export function ToastStack(props: ToastStackProps) {
  const { children, position = "bottom-right", className = "" } = props;
  return (
    <div className={`vn-toast-stack vn-toast-stack--${position} ${className}`.trim()}>
      {children}
    </div>
  );
}
```

- [ ] **Step 2: Commit Component Code**

```bash
git add Design_System/components/feedback/Toast.tsx
git commit -m "feat(ds): implement enhanced Toast component with tones, actions, and progress timer"
```

---

### Task 3: Build Toast Context, Provider, and Global Imperative API

**Files:**
- Create: `Design_System/components/feedback/ToastContext.tsx`
- Modify: `Design_System/src/primitives.ts`
- Modify: `Design_System/src/index.ts` (or main exports file)

**Interfaces:**
- Consumes: `Toast`, `ToastStack`, `ToastOptions`, `ToastTone`
- Produces: `ToastProvider`, `useToast()`, `toast` object with `success()`, `danger()`, `warning()`, `info()`, `loading()`, `custom()`, `promise()`, `dismiss()`, `clearAll()`.

- [ ] **Step 1: Create `ToastContext.tsx` with Event Emitter and Provider State**

```tsx
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

    // Replace if id exists, otherwise prepend and slice to max
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
```

- [ ] **Step 2: Re-export ToastContext in `Design_System/src/primitives.ts`**

In `Design_System/src/primitives.ts`:
Add export for `ToastContext`:
```ts
export * from "../components/feedback/ToastContext";
```

- [ ] **Step 3: Commit Toast Context and Exports**

```bash
git add Design_System/components/feedback/ToastContext.tsx Design_System/src/primitives.ts
git commit -m "feat(ds): add ToastProvider, useToast hook, and imperative toast API"
```

---

### Task 4: Build Design System Package & Unit Test

**Files:**
- Test: `Design_System/tests/unit/Toast.test.tsx` (or vitest spec)

- [ ] **Step 1: Write Unit Test for Toast and ToastContext**

Create unit test verifying:
1. `toast.success()` adds item to state.
2. `toast.dismiss()` removes item.
3. Action click triggers callback.

- [ ] **Step 2: Run Build Script for `@venom/design-system`**

Run: `cd Design_System && npm run build`
Expected: Clean TypeScript declaration generation and ESM/CJS build in `dist/`.

- [ ] **Step 3: Commit Built Output**

```bash
git add Design_System/dist Design_System/dist-explorer
git commit -m "build(ds): compile design system package with toast extensions"
```

---

### Task 5: Mount ToastProvider Globally in Dashboard Shell

**Files:**
- Modify: `dashboard/src/shell/AppShell.tsx`

**Interfaces:**
- Consumes: `ToastProvider` from `@venom/design-system`
- Produces: Global toast rendering container across all dashboard routes and pages.

- [ ] **Step 1: Import and Wrap `AppShell` with `ToastProvider`**

In `dashboard/src/shell/AppShell.tsx`:
```tsx
import { ToastProvider } from "@venom/design-system";

export function AppShell(props: AppShellProps) {
  return (
    <ToastProvider position="bottom-right">
      <div className="vn-app-shell">
        {/* Existing layout, chrome, navigation */}
      </div>
    </ToastProvider>
  );
}
```

- [ ] **Step 2: Commit Dashboard Integration**

```bash
git add dashboard/src/shell/AppShell.tsx
git commit -m "feat(dashboard): wrap AppShell with ToastProvider for global toast availability"
```

---

### Task 6: Add Toast Interactive Controls & Verification

**Files:**
- Modify: `dashboard/src/settings/SettingsPage.tsx` (or `SmokeInventory.tsx`)

- [ ] **Step 1: Add Toast Showcase Controls for Live Testing**

Add buttons in settings or smoke inventory page to trigger:
- `toast.success("Settings saved successfully!")`
- `toast.danger("Failed to rotate API key", { detail: "Network timeout" })`
- `toast.warning("High memory usage detected", { action: { label: "Details", onClick: () => {} } })`
- `toast.info("Venom Router v2.4.0 is available")`
- `toast.loading("Deploying new routing rule...")`

- [ ] **Step 2: Verify Dual Theme Rendering & Dismiss Behavior**

Toggle between `venom-dark` and `venom-light` themes, verify text contrast, accent colors, progress bar timer, and dismiss behavior.

- [ ] **Step 3: Commit Final Verification Changes**

```bash
git add dashboard/src/
git commit -m "feat(dashboard): add interactive toast test triggers and verify theme harmony"
```

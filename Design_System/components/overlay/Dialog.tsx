import * as React from "react";
import { Icon } from "../icons/Icon";
import { Button } from "../actions/Button";
import { getFocusable, useOverlayStack } from "./overlay-stack";

let uid = 0;
function useId(prefix: string): string {
  const ref = React.useRef<string | null>(null);
  if (ref.current == null) ref.current = prefix + "-" + ++uid;
  return ref.current;
}

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
export function Dialog(props: DialogProps) {
  const { open, onClose, title, description, children, footer, wide = false, dismissible = true, initialFocusRef, className = "" } = props;
  const rootRef = React.useRef<HTMLDivElement>(null);
  const openerRef = React.useRef<Element | null>(null);
  const titleId = useId("vn-dialog-title");
  const descId = useId("vn-dialog-desc");
  const { isTopmost } = useOverlayStack(open);

  React.useEffect(() => {
    if (!open) return;
    openerRef.current = document.activeElement;
    const node = rootRef.current;
    const raf = requestAnimationFrame(() => {
      if (!node) return;
      const preferred = initialFocusRef && initialFocusRef.current;
      if (preferred && node.contains(preferred)) {
        preferred.focus();
        return;
      }
      const focusables = getFocusable(node);
      if (focusables.length) focusables[0].focus();
      else node.focus();
    });
    return () => {
      cancelAnimationFrame(raf);
      const opener = openerRef.current as HTMLElement | null;
      if (opener && typeof opener.focus === "function" && document.contains(opener)) opener.focus();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  React.useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (!isTopmost) return;
      if (e.key === "Escape") {
        if (dismissible) {
          e.preventDefault();
          onClose && onClose();
        }
        return;
      }
      if (e.key === "Tab") {
        const node = rootRef.current;
        if (!node) return;
        const focusables = getFocusable(node);
        if (!focusables.length) {
          e.preventDefault();
          return;
        }
        const first = focusables[0];
        const last = focusables[focusables.length - 1];
        const active = document.activeElement;
        if (e.shiftKey && (active === first || !node.contains(active))) {
          e.preventDefault();
          last.focus();
        } else if (!e.shiftKey && active === last) {
          e.preventDefault();
          first.focus();
        }
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, isTopmost, dismissible, onClose]);

  if (!open) return null;
  const labelledBy = typeof title !== "undefined" ? titleId : undefined;
  const describedBy = typeof description !== "undefined" ? descId : undefined;

  return (
    <>
      <div className="vn-scrim" onClick={dismissible ? onClose : undefined}></div>
      <div
        ref={rootRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={labelledBy}
        aria-describedby={describedBy}
        tabIndex={-1}
        className={("vn-dialog " + (wide ? "vn-dialog--wide " : "") + className).trim()}
      >
        <div className="vn-dialog-header">
          <h2 id={titleId} className="vn-title-sub" style={{ margin: 0, flex: 1 }}>
            {title}
          </h2>
          {dismissible ? (
            <button type="button" className="vn-btn vn-btn--icon vn-btn--ghost vn-btn--sm" aria-label="Close" onClick={onClose}>
              <Icon name="x" size={14} />
            </button>
          ) : null}
        </div>
        <div className="vn-dialog-body">
          {description ? (
            <p id={descId} style={{ marginTop: 0 }}>
              {description}
            </p>
          ) : null}
          {children}
        </div>
        {footer ? <div className="vn-dialog-footer">{footer}</div> : null}
      </div>
    </>
  );
}

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
export function AlertDialog(props: AlertDialogProps) {
  const { open, title, description, confirmLabel = "Confirm", cancelLabel = "Cancel", danger = false, onConfirm, onCancel, children } = props;
  const rootRef = React.useRef<HTMLDivElement>(null);
  const openerRef = React.useRef<Element | null>(null);
  const titleId = useId("vn-alertdialog-title");
  const descId = useId("vn-alertdialog-desc");
  const { isTopmost } = useOverlayStack(open);

  React.useEffect(() => {
    if (!open) return;
    openerRef.current = document.activeElement;
    const node = rootRef.current;
    const raf = requestAnimationFrame(() => {
      if (!node) return;
      const focusables = getFocusable(node);
      if (focusables.length) focusables[0].focus();
      else node.focus();
    });
    return () => {
      cancelAnimationFrame(raf);
      const opener = openerRef.current as HTMLElement | null;
      if (opener && typeof opener.focus === "function" && document.contains(opener)) opener.focus();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  React.useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (!isTopmost || e.key !== "Tab") return;
      const node = rootRef.current;
      if (!node) return;
      const focusables = getFocusable(node);
      if (!focusables.length) {
        e.preventDefault();
        return;
      }
      const first = focusables[0];
      const last = focusables[focusables.length - 1];
      const active = document.activeElement;
      if (e.shiftKey && (active === first || !node.contains(active))) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && active === last) {
        e.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, isTopmost]);

  if (!open) return null;
  return (
    <>
      <div className="vn-scrim"></div>
      <div ref={rootRef} role="alertdialog" aria-modal="true" aria-labelledby={title ? titleId : undefined} aria-describedby={description ? descId : undefined} tabIndex={-1} className="vn-dialog">
        <div className="vn-dialog-header">
          <h2 id={titleId} className="vn-title-sub" style={{ margin: 0 }}>
            {title}
          </h2>
        </div>
        <div className="vn-dialog-body">
          {description ? (
            <p id={descId} style={{ marginTop: 0 }}>
              {description}
            </p>
          ) : null}
          {children}
        </div>
        <div className="vn-dialog-footer">
          <Button onClick={onCancel}>{cancelLabel}</Button>
          <Button variant={danger ? "danger" : "primary"} onClick={onConfirm}>
            {confirmLabel}
          </Button>
        </div>
      </div>
    </>
  );
}

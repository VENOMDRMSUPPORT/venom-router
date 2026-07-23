import * as React from "react";
import { Icon } from "../icons/Icon";
import { getFocusable, useOverlayStack } from "./overlay-stack";

let uid = 0;
function useId(prefix: string): string {
  const ref = React.useRef<string | null>(null);
  if (ref.current == null) ref.current = prefix + "-" + ++uid;
  return ref.current;
}

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
export function Drawer(props: DrawerProps) {
  const {
    open,
    onClose,
    title,
    description,
    children,
    footer,
    wide = false,
    dismissible = true,
    initialFocusRef,
    className = "",
  } = props;
  const rootRef = React.useRef<HTMLDivElement>(null);
  const openerRef = React.useRef<Element | null>(null);
  const titleId = useId("vn-drawer-title");
  const descId = useId("vn-drawer-desc");
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
        className={("vn-drawer " + (wide ? "vn-drawer--wide " : "") + className).trim()}
      >
        <div className="vn-drawer-header">
          <h2 id={titleId} className="vn-title-sub" style={{ margin: 0, flex: 1 }}>
            {title}
          </h2>
          {dismissible ? (
            <button type="button" className="vn-btn vn-btn--icon vn-btn--ghost vn-btn--sm" aria-label="Close" onClick={onClose}>
              <Icon name="x" size={14} />
            </button>
          ) : null}
        </div>
        <div className="vn-drawer-body vn-scroll">
          {description ? (
            <p id={descId} className="vn-caption" style={{ marginTop: 0 }}>
              {description}
            </p>
          ) : null}
          {children}
        </div>
        {footer ? <div className="vn-drawer-footer">{footer}</div> : null}
      </div>
    </>
  );
}

/** Sheet — alias of Drawer for detail-inspection surfaces. Same component, same behavior. */
export function Sheet(props: DrawerProps) {
  return <Drawer {...props} />;
}

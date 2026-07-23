import * as React from "react";

/**
 * The package overlay contract: a module-level stack of currently-open overlays
 * (Dialog/AlertDialog/Drawer/Sheet). Only the topmost overlay traps Tab and closes on
 * Escape — so a Dialog opened from inside a Drawer (or vice versa) behaves correctly
 * without either overlay needing to know about the other's existence.
 */
let stack: number[] = [];
let counter = 0;
const listeners = new Set<() => void>();
function notify() {
  listeners.forEach((l) => l());
}

export function useOverlayStack(open: boolean) {
  const idRef = React.useRef<number | null>(null);
  const [, forceRender] = React.useState(0);

  React.useEffect(() => {
    const onChange = () => forceRender((t) => t + 1);
    listeners.add(onChange);
    return () => {
      listeners.delete(onChange);
    };
  }, []);

  React.useEffect(() => {
    if (open) {
      if (idRef.current == null) {
        idRef.current = ++counter;
        stack.push(idRef.current);
        notify();
      }
    } else if (idRef.current != null) {
      const id = idRef.current;
      stack = stack.filter((x) => x !== id);
      idRef.current = null;
      notify();
    }
    return () => {
      if (idRef.current != null) {
        const id = idRef.current;
        stack = stack.filter((x) => x !== id);
        idRef.current = null;
        notify();
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const isTopmost = idRef.current != null && stack.length > 0 && stack[stack.length - 1] === idRef.current;
  return { isTopmost, depth: stack.length };
}

/** Every focusable descendant of `root`, in DOM order — the shared focus-trap query. */
export function getFocusable(root: HTMLElement): HTMLElement[] {
  const nodes = root.querySelectorAll<HTMLElement>(
    'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
  );
  return Array.from(nodes).filter((el) => el.offsetParent !== null || el === document.activeElement);
}

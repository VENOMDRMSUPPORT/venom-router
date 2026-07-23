import * as React from "react";
import { Icon } from "../icons/Icon";

export interface DropdownMenuItem {
  type?: "item" | "separator" | "label";
  label?: React.ReactNode;
  icon?: string;
  kbd?: string;
  danger?: boolean;
  disabled?: boolean;
  onSelect?: () => void;
}

export interface DropdownMenuProps {
  /** The trigger element. Receives `onClick`/`onKeyDown`/`aria-expanded`/`aria-haspopup` — any handlers already on it are called first. */
  trigger: React.ReactElement;
  items: DropdownMenuItem[];
  align?: "start" | "end";
  className?: string;
}

/**
 * DropdownMenu — real DOM focus movement (roving tabindex), not a painted active index.
 * ArrowDown/ArrowUp/Home/End move focus among enabled items; Enter/Space activate (native
 * button behavior); Escape closes and restores focus to the trigger; Tab closes and lets
 * focus continue to the next widget (WAI-ARIA menu pattern). Disabled items are skipped by
 * every navigation path. Single-character typeahead (no buffering) jumps to the next item
 * whose label starts with the typed letter.
 */
export function DropdownMenu(props: DropdownMenuProps) {
  const { trigger, items = [], align = "start", className = "" } = props;
  const [open, setOpen] = React.useState(false);
  const [activeIndex, setActiveIndex] = React.useState(-1);
  const rootRef = React.useRef<HTMLSpanElement>(null);
  const triggerRef = React.useRef<HTMLElement | null>(null);
  const itemRefs = React.useRef<Array<HTMLButtonElement | null>>([]);

  const enabledIndexes = React.useMemo(
    () => items.map((it, i) => (it.type !== "separator" && it.type !== "label" && !it.disabled ? i : -1)).filter((i) => i !== -1),
    [items]
  );

  const close = React.useCallback((restoreFocus: boolean) => {
    setOpen(false);
    setActiveIndex(-1);
    if (restoreFocus) triggerRef.current?.focus();
  }, []);

  React.useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, [open]);

  React.useEffect(() => {
    if (open) setActiveIndex(enabledIndexes[0] ?? -1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  React.useEffect(() => {
    if (open && activeIndex >= 0) itemRefs.current[activeIndex]?.focus();
  }, [open, activeIndex]);

  const moveTo = (dir: 1 | -1) => {
    if (!enabledIndexes.length) return;
    const pos = enabledIndexes.indexOf(activeIndex);
    const nextPos = pos === -1 ? (dir === 1 ? 0 : enabledIndexes.length - 1) : (pos + dir + enabledIndexes.length) % enabledIndexes.length;
    setActiveIndex(enabledIndexes[nextPos]);
  };

  const typeahead = (letter: string) => {
    if (!enabledIndexes.length) return;
    const labels = items.map((it) => (typeof it.label === "string" ? it.label.toLowerCase() : ""));
    const startPos = (enabledIndexes.indexOf(activeIndex) + 1 + enabledIndexes.length) % enabledIndexes.length;
    for (let k = 0; k < enabledIndexes.length; k++) {
      const idx = enabledIndexes[(startPos + k) % enabledIndexes.length];
      if (labels[idx] && labels[idx].startsWith(letter)) {
        setActiveIndex(idx);
        return;
      }
    }
  };

  const onTriggerKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowDown" || e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      setOpen(true);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setOpen(true);
      setActiveIndex(enabledIndexes[enabledIndexes.length - 1] ?? -1);
    }
  };

  const activate = (it: DropdownMenuItem) => {
    if (it.disabled) return;
    it.onSelect && it.onSelect();
    close(true);
  };

  const onMenuKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      e.preventDefault();
      close(true);
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      moveTo(1);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      moveTo(-1);
    } else if (e.key === "Home") {
      e.preventDefault();
      if (enabledIndexes.length) setActiveIndex(enabledIndexes[0]);
    } else if (e.key === "End") {
      e.preventDefault();
      if (enabledIndexes.length) setActiveIndex(enabledIndexes[enabledIndexes.length - 1]);
    } else if (e.key === "Tab") {
      close(false);
    } else if (e.key.length === 1 && !e.metaKey && !e.ctrlKey && !e.altKey) {
      typeahead(e.key.toLowerCase());
    }
  };

  return (
    <span ref={rootRef} style={{ position: "relative", display: "inline-flex" }}>
      {React.cloneElement(trigger, {
        ref: (node: HTMLElement | null) => {
          triggerRef.current = node;
        },
        onClick: (e: React.MouseEvent) => {
          trigger.props.onClick && trigger.props.onClick(e);
          setOpen((o) => !o);
        },
        onKeyDown: (e: React.KeyboardEvent) => {
          trigger.props.onKeyDown && trigger.props.onKeyDown(e);
          onTriggerKeyDown(e);
        },
        "aria-expanded": open,
        "aria-haspopup": "menu",
      })}
      {open ? (
        <div
          className={("vn-menu " + className).trim()}
          role="menu"
          onKeyDown={onMenuKeyDown}
          style={{ position: "absolute", top: "calc(100% + 4px)", [align === "end" ? "right" : "left"]: 0 }}
        >
          {items.map((it, i) => {
            if (it.type === "separator") return <hr key={i} className="vn-menu-sep" role="separator" />;
            if (it.type === "label")
              return (
                <div key={i} className="vn-menu-label vn-overline">
                  {it.label}
                </div>
              );
            return (
              <button
                key={i}
                ref={(node) => {
                  itemRefs.current[i] = node;
                }}
                type="button"
                role="menuitem"
                tabIndex={i === activeIndex ? 0 : -1}
                disabled={it.disabled}
                aria-disabled={it.disabled || undefined}
                className={"vn-menu-item" + (it.danger ? " vn-menu-item--danger" : "")}
                onClick={() => activate(it)}
                onFocus={() => setActiveIndex(i)}
              >
                {it.icon ? <Icon name={it.icon} size={14} /> : null}
                {it.label}
                {it.kbd ? <kbd className="vn-kbd vn-menu-kbd">{it.kbd}</kbd> : null}
              </button>
            );
          })}
        </div>
      ) : null}
    </span>
  );
}

export interface ContextMenuItem {
  type?: "item" | "separator";
  label?: React.ReactNode;
  icon?: string;
  danger?: boolean;
  onSelect?: () => void;
}

export interface ContextMenuProps {
  items: ContextMenuItem[];
  children?: React.ReactNode;
}

/** ContextMenu — same menu contract, opened at the pointer via right-click on its child. */
export function ContextMenu(props: ContextMenuProps) {
  const { items = [], children } = props;
  const [pos, setPos] = React.useState<{ x: number; y: number } | null>(null);

  React.useEffect(() => {
    if (!pos) return;
    const close = () => setPos(null);
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close();
    };
    document.addEventListener("click", close);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("click", close);
      document.removeEventListener("keydown", onKey);
    };
  }, [pos]);

  return (
    <span onContextMenu={(e: React.MouseEvent) => { e.preventDefault(); setPos({ x: e.clientX, y: e.clientY }); }} style={{ display: "contents" }}>
      {children}
      {pos ? (
        <div className="vn-menu" role="menu" style={{ position: "fixed", left: pos.x, top: pos.y, zIndex: "var(--z-dropdown)" as React.CSSProperties["zIndex"] }}>
          {items.map((it, i) =>
            it.type === "separator" ? (
              <hr key={i} className="vn-menu-sep" role="separator" />
            ) : (
              <button
                key={i}
                type="button"
                role="menuitem"
                className={"vn-menu-item" + (it.danger ? " vn-menu-item--danger" : "")}
                onClick={() => it.onSelect && it.onSelect()}
              >
                {it.icon ? <Icon name={it.icon} size={14} /> : null}
                {it.label}
              </button>
            )
          )}
        </div>
      ) : null}
    </span>
  );
}

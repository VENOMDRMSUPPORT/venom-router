import * as React from "react";

export interface PopoverProps {
  trigger: React.ReactElement;
  /** Either static content, or a render-prop that receives a `close` callback. */
  children?: React.ReactNode | ((close: () => void) => React.ReactNode);
  align?: "start" | "end";
  className?: string;
}

export function Popover(props: PopoverProps) {
  const { trigger, children, align = "start", className = "" } = props;
  const [open, setOpen] = React.useState(false);
  const root = React.useRef<HTMLSpanElement>(null);
  React.useEffect(() => {
    const onDoc = (e: MouseEvent) => { if (root.current && !root.current.contains(e.target as Node)) setOpen(false); };
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") setOpen(false); };
    document.addEventListener("mousedown", onDoc); document.addEventListener("keydown", onKey);
    return () => { document.removeEventListener("mousedown", onDoc); document.removeEventListener("keydown", onKey); };
  }, []);
  return (
    <span ref={root} style={{ position: "relative", display: "inline-flex" }}>
      {React.cloneElement(trigger, { onClick: () => setOpen(!open), "aria-expanded": open, "aria-haspopup": "dialog" })}
      {open ? (
        <div className={("vn-menu " + className).trim()} role="dialog"
          style={{ position: "absolute", top: "calc(100% + 4px)", [align === "end" ? "right" : "left"]: 0, padding: "var(--space-3)", minWidth: 240 }}>
          {typeof children === "function" ? children(() => setOpen(false)) : children}
        </div>
      ) : null}
    </span>
  );
}

import * as React from "react";

export interface TooltipProps {
  content: React.ReactNode;
  /** Exactly one element — receives `aria-describedby` while the tooltip is open. */
  children: React.ReactElement;
  className?: string;
}

/** Hover/focus tooltip. Content must be supplementary — never the only path to critical info. */
export function Tooltip(props: TooltipProps) {
  const { content, children, className = "" } = props;
  const [open, setOpen] = React.useState(false);
  const id = React.useRef("vtt-" + Math.random().toString(36).slice(2, 8)).current;
  return (
    <span style={{ position: "relative", display: "inline-flex" }}
      onMouseEnter={() => setOpen(true)} onMouseLeave={() => setOpen(false)}
      onFocus={() => setOpen(true)} onBlur={() => setOpen(false)}
      onKeyDown={(e: React.KeyboardEvent) => e.key === "Escape" && setOpen(false)}>
      {React.cloneElement(React.Children.only(children), { "aria-describedby": open ? id : undefined })}
      {open ? <span role="tooltip" id={id} className={("vn-tooltip " + className).trim()} style={{ bottom: "calc(100% + 6px)", left: "50%", translate: "-50% 0" }}>{content}</span> : null}
    </span>
  );
}

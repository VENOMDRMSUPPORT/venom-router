import * as React from "react";
import { Icon } from "../icons/Icon";

export interface IconButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  icon: string;
  /** Required accessible name — an icon-only control never ships without one. */
  label: string;
  variant?: "ghost" | "secondary" | "danger" | "primary";
  size?: "sm" | "md" | "lg";
}

/** Forwards its ref to the underlying `<button>` — required for callers (DropdownMenu, Popover, Tooltip) that focus/measure the trigger imperatively. */
export const IconButton = React.forwardRef<HTMLButtonElement, IconButtonProps>(function IconButton(props, ref) {
  const { icon, label, variant = "ghost", size = "md", disabled, className = "", children, ...rest } = props;
  const cls = ["vn-btn", "vn-btn--icon", "vn-btn--" + variant, size !== "md" ? "vn-btn--" + size : "", className].filter(Boolean).join(" ");
  return (
    <button ref={ref} type="button" className={cls} aria-label={label} disabled={disabled} {...rest}>
      <Icon name={icon} size={size === "sm" ? 12 : 16} />
      {children}
    </button>
  );
});

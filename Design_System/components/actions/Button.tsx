import * as React from "react";
import { Icon } from "../icons/Icon";

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: "primary" | "secondary" | "ghost" | "danger";
  size?: "sm" | "md" | "lg";
  icon?: string;
  loading?: boolean;
}

/** Forwards its ref to the underlying `<button>` — required for callers (DropdownMenu, Popover, Tooltip) that focus/measure the trigger imperatively. */
export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(function Button(props, ref) {
  const { variant = "secondary", size = "md", icon, loading = false, disabled, children, className = "", type = "button", ...rest } = props;
  const cls = ["vn-btn", "vn-btn--" + variant, size !== "md" ? "vn-btn--" + size : "", loading ? "is-loading" : "", className].filter(Boolean).join(" ");
  return (
    <button ref={ref} type={type} className={cls} disabled={disabled || loading} aria-busy={loading || undefined} {...rest}>
      {icon ? <Icon name={icon} size={size === "sm" ? 12 : 16} /> : null}
      {children}
    </button>
  );
});

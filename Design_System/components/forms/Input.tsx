import * as React from "react";

export interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {
  mono?: boolean;
  invalid?: boolean;
}

/** Forwards its ref to the underlying `<input>` — required for callers that need to focus it imperatively (e.g. `Drawer`'s `initialFocusRef`). */
export const Input = React.forwardRef<HTMLInputElement, InputProps>(function Input(props, ref) {
  const { mono = false, invalid = false, className = "", ...rest } = props;
  const cls = ["vn-input", mono ? "vn-input--mono" : "", className].filter(Boolean).join(" ");
  return <input ref={ref} className={cls} aria-invalid={invalid || undefined} {...rest} />;
});

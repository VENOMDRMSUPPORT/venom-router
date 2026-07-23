import * as React from "react";

export interface SelectOption {
  value: string;
  label: React.ReactNode;
  disabled?: boolean;
}

export interface SelectProps extends Omit<React.SelectHTMLAttributes<HTMLSelectElement>, "children"> {
  options?: Array<string | SelectOption>;
  invalid?: boolean;
  /** Pass explicit `<option>` children instead of `options` for full control. */
  children?: React.ReactNode;
}

export function Select(props: SelectProps) {
  const { options = [], invalid = false, className = "", children, ...rest } = props;
  return (
    <span className={("vn-select " + className).trim()}>
      <select aria-invalid={invalid || undefined} {...rest}>
        {children || options.map((o) => {
          const opt: SelectOption = typeof o === "string" ? { value: o, label: o } : o;
          return <option key={opt.value} value={opt.value} disabled={opt.disabled}>{opt.label}</option>;
        })}
      </select>
    </span>
  );
}

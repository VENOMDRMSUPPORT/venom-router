import * as React from "react";

export interface CheckboxProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "type"> {
  label?: React.ReactNode;
  indeterminate?: boolean;
}

export function Checkbox(props: CheckboxProps) {
  const { label, indeterminate = false, className = "", ...rest } = props;
  const ref = React.useRef<HTMLInputElement>(null);
  React.useEffect(() => { if (ref.current) ref.current.indeterminate = indeterminate; }, [indeterminate]);
  return (
    <label className={("vn-check " + className).trim()}>
      <input ref={ref} type="checkbox" {...rest} />
      {label ? <span>{label}</span> : null}
    </label>
  );
}

import * as React from "react";

export interface SwitchProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "type"> {
  label?: React.ReactNode;
}

export function Switch(props: SwitchProps) {
  const { label, className = "", ...rest } = props;
  return (
    <label className={("vn-switch " + className).trim()}>
      <input type="checkbox" role="switch" {...rest} />
      {label ? <span>{label}</span> : null}
    </label>
  );
}

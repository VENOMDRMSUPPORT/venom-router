import * as React from "react";

export interface ButtonGroupProps {
  children?: React.ReactNode;
  label?: string;
  className?: string;
}

export function ButtonGroup(props: ButtonGroupProps) {
  const { children, label, className = "" } = props;
  return <div role="group" aria-label={label} className={("vn-btn-group " + className).trim()}>{children}</div>;
}

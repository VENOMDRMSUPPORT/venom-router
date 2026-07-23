import * as React from "react";

export interface ProgressProps {
  value?: number;
  max?: number;
  label?: string;
  indeterminate?: boolean;
  className?: string;
}

export function Progress(props: ProgressProps) {
  const { value, max = 100, label, indeterminate = false, className = "" } = props;
  const pct = indeterminate ? 40 : Math.max(0, Math.min(100, ((value ?? 0) / max) * 100));
  return (
    <div className={("vn-progress " + (indeterminate ? "vn-progress--indeterminate " : "") + className).trim()}
      role="progressbar" aria-label={label}
      aria-valuemin={0} aria-valuemax={max} aria-valuenow={indeterminate ? undefined : value}
      aria-valuetext={indeterminate ? "In progress" : undefined}>
      <div style={{ width: pct + "%" }}></div>
    </div>
  );
}

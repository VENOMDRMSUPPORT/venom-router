import * as React from "react";

export interface SliderProps extends React.InputHTMLAttributes<HTMLInputElement> {
  label?: string;
  showValue?: boolean;
  unit?: string;
}

export function Slider(props: SliderProps) {
  const { label, showValue = true, unit = "", className = "", ...rest } = props;
  const val = props.value != null ? props.value : props.defaultValue;
  return (
    <div className={className} style={{ display: "flex", alignItems: "center", gap: "var(--space-3)" }}>
      <input type="range" className="vn-slider" aria-label={label} {...rest} />
      {showValue ? <span className="vn-data vn-text-secondary" style={{ minWidth: 48, textAlign: "right" }}>{val}{unit}</span> : null}
    </div>
  );
}

import * as React from "react";

export type SegmentedOption = string | { value: string; label: React.ReactNode };

export interface SegmentedControlProps {
  options?: SegmentedOption[];
  value?: string;
  onChange?: (value: string) => void;
  label?: string;
  className?: string;
}

export function SegmentedControl(props: SegmentedControlProps) {
  const { options = [], value, onChange, label, className = "" } = props;
  return (
    <div className={("vn-segmented " + className).trim()} role="group" aria-label={label}>
      {options.map((o) => {
        const v = typeof o === "string" ? o : o.value;
        const lab = typeof o === "string" ? o : o.label;
        return (
          <button key={v} type="button" aria-pressed={v === value} onClick={() => onChange && onChange(v)}>{lab}</button>
        );
      })}
    </div>
  );
}

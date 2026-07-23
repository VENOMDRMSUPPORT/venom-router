import * as React from "react";

export interface RadioOption {
  value: string;
  label: React.ReactNode;
  description?: React.ReactNode;
  disabled?: boolean;
}

export interface RadioGroupProps {
  name?: string;
  options?: Array<string | RadioOption>;
  value?: string;
  onChange?: (value: string) => void;
  label?: string;
  disabled?: boolean;
  className?: string;
}

export function RadioGroup(props: RadioGroupProps) {
  const { name, options = [], value, onChange, label, disabled, className = "" } = props;
  return (
    <div role="radiogroup" aria-label={label} className={("vn-radio-group " + className).trim()}>
      {options.map((o) => {
        const opt: RadioOption = typeof o === "string" ? { value: o, label: o } : o;
        return (
          <label key={opt.value} className="vn-check">
            <input type="radio" name={name} value={opt.value} checked={value === opt.value}
              disabled={disabled || opt.disabled} onChange={() => onChange && onChange(opt.value)} />
            <span>{opt.label}{opt.description ? <span className="vn-field-desc" style={{ display: "block" }}>{opt.description}</span> : null}</span>
          </label>
        );
      })}
    </div>
  );
}

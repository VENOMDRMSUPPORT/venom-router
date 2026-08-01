import * as React from "react";
import { Icon } from "../icons/Icon";

export interface SearchFieldProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "type"> {
  /** Accessible name (this field renders no visible `<label>`). */
  label?: string;
  /** Optional one-step clear action for controlled search fields. */
  onClear?: () => void;
}

export function SearchField(props: SearchFieldProps) {
  const { className = "", label = "Search", onClear, value, disabled, ...rest } = props;
  const hasValue = value != null && String(value).length > 0;
  return (
    <span className={("vn-search " + className).trim()} style={{ display: "inline-block" }}>
      <Icon name="search" size={14} />
      <input type="search" className="vn-input" aria-label={label} value={value} disabled={disabled} {...rest} />
      {onClear && hasValue ? (
        <button type="button" className="vn-search-clear" aria-label="Clear search" onClick={onClear} disabled={disabled}>
          <Icon name="x" size={14} />
        </button>
      ) : null}
    </span>
  );
}

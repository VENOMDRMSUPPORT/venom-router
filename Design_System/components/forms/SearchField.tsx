import * as React from "react";
import { Icon } from "../icons/Icon";

export interface SearchFieldProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "type"> {
  /** Accessible name (this field renders no visible `<label>`). */
  label?: string;
}

export function SearchField(props: SearchFieldProps) {
  const { className = "", label = "Search", ...rest } = props;
  return (
    <span className={("vn-search " + className).trim()} style={{ display: "inline-block" }}>
      <Icon name="search" size={14} />
      <input type="search" className="vn-input" aria-label={label} {...rest} />
    </span>
  );
}

import * as React from "react";
import { Icon } from "../icons/Icon";

export interface TagProps {
  onRemove?: () => void;
  removeLabel?: string;
  icon?: string;
  mono?: boolean;
  children?: React.ReactNode;
  className?: string;
}

export function Tag(props: TagProps) {
  const { onRemove, removeLabel = "Remove", icon, mono = false, children, className = "" } = props;
  return (
    <span className={("vn-tag " + className).trim()} style={mono ? { fontFamily: "var(--font-family-mono)" } : undefined}>
      {icon ? <Icon name={icon} size={12} /> : null}
      {children}
      {onRemove ? <button type="button" aria-label={removeLabel} onClick={onRemove}><Icon name="x" size={10} /></button> : null}
    </span>
  );
}

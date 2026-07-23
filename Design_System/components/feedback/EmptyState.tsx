import * as React from "react";
import { Icon } from "../icons/Icon";

export interface EmptyStateProps {
  icon?: string;
  title: React.ReactNode;
  description?: React.ReactNode;
  action?: React.ReactNode;
  className?: string;
}

export function EmptyState(props: EmptyStateProps) {
  const { icon = "inbox", title, description, action, className = "" } = props;
  return (
    <div className={("vn-empty " + className).trim()}>
      <Icon name={icon} size={28} />
      <div className="vn-empty-title">{title}</div>
      {description ? <div className="vn-empty-desc">{description}</div> : null}
      {action}
    </div>
  );
}

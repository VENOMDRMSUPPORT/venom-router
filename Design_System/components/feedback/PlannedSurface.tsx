import * as React from "react";
import { Icon } from "../icons/Icon";

export interface PlannedSurfaceProps {
  icon?: string;
  eyebrow?: React.ReactNode;
  title: React.ReactNode;
  description: React.ReactNode;
  statusLabel?: React.ReactNode;
  note?: React.ReactNode;
  className?: string;
}

export function PlannedSurface(props: PlannedSurfaceProps) {
  const {
    icon = "shield-check",
    eyebrow,
    title,
    description,
    statusLabel = "Planned surface",
    note = "This surface is intentionally not populated until its implementation is complete.",
    className = "",
  } = props;
  return (
    <section className={("vn-planned-surface " + className).trim()}>
      <div className="vn-planned-icon"><Icon name={icon} size={26} /></div>
      {eyebrow ? <div className="vn-overline vn-planned-eyebrow">{eyebrow}</div> : null}
      <h2 className="vn-planned-title">{title}</h2>
      <div className="vn-planned-description">{description}</div>
      <span className="vn-planned-status">{statusLabel}</span>
      {note ? (
        <div className="vn-planned-note">
          <Icon name="shield-check" size={14} />
          <span>{note}</span>
        </div>
      ) : null}
    </section>
  );
}

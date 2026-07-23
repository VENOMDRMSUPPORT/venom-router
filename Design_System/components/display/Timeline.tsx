import * as React from "react";

export interface TimelineItem {
  title: React.ReactNode;
  detail?: React.ReactNode;
  time?: React.ReactNode;
  tone?: "healthy" | "critical" | "warning" | "accent";
}

export interface TimelineProps {
  items?: TimelineItem[];
  className?: string;
}

export function Timeline(props: TimelineProps) {
  const { items = [], className = "" } = props;
  return (
    <ol className={("vn-timeline " + className).trim()}>
      {items.map((it, i) => (
        <li key={i}>
          <span className={"vn-timeline-dot" + (it.tone ? " vn-timeline-dot--" + it.tone : "")} aria-hidden="true"></span>
          <div className="vn-body-compact">{it.title}</div>
          {it.detail ? <div className="vn-caption">{it.detail}</div> : null}
          {it.time ? <div className="vn-caption vn-mono-xs">{it.time}</div> : null}
        </li>
      ))}
    </ol>
  );
}

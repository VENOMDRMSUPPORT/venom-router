import * as React from "react";

export interface CardProps extends React.HTMLAttributes<HTMLDivElement> {
  padded?: boolean;
  interactive?: boolean;
  selected?: boolean;
}

export function Card(props: CardProps) {
  const { padded = true, interactive = false, selected = false, children, className = "", ...rest } = props;
  const cls = ["vn-card", padded ? "vn-card--pad" : "", interactive ? "vn-card--interactive" : "", selected ? "vn-card--selected" : "", className].filter(Boolean).join(" ");
  const extra = interactive ? { tabIndex: 0, role: "button" as const } : {};
  return <div className={cls} {...extra} {...rest}>{children}</div>;
}

export type StatusTone = "healthy" | "degraded" | "warning" | "critical" | "info" | "unknown" | "inactive";

export interface StatCardProps {
  label: React.ReactNode;
  value: React.ReactNode;
  meta?: React.ReactNode;
  tone?: StatusTone;
  icon?: string;
  className?: string;
}

export function StatCard(props: StatCardProps) {
  const { label, value, meta, tone, icon, className = "" } = props;
  return (
    <div className={("vn-card vn-stat " + className).trim()}>
      <span className="vn-stat-label">{label}</span>
      <span className="vn-stat-value" style={tone ? { color: "var(--status-" + tone + "-fg)" } : undefined}>{value}</span>
      {meta ? <span className="vn-stat-meta">{meta}</span> : null}
    </div>
  );
}

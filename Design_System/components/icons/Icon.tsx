import * as React from "react";

/** Canonical domain-concept -> glyph map (see icons/icon-map.md). */
export const DOMAIN_ICON_MAP: Record<string, string> = {
  chat: "message-square", streaming: "radio", tools: "wrench",
  structured_output: "braces", reasoning: "brain", vision: "eye",
  context_window: "scan-text", coding: "code",
  authentication: "shield-check", api_key: "key-round", oauth: "fingerprint",
  provider: "server", account: "user-round", model: "box",
  certification: "badge-check", probe: "flask-conical", quota: "gauge",
  cooldown: "hourglass", routing: "route", fallback: "corner-down-right",
  diagnostics: "activity", trace: "list-tree", security: "lock",
  backup: "archive", restore: "archive-restore", health: "heart-pulse",
  latency: "timer", cost: "coins", free: "hand-coins", paid: "credit-card",
  unknown: "circle-help",
};

export interface IconProps {
  /** A domain concept name (see DOMAIN_ICON_MAP) or a literal Lucide glyph name from icons/icons.css. */
  name: string;
  size?: number;
  /** Accessible name. Omit for decorative icons paired with visible text (renders `aria-hidden`). */
  label?: string;
  className?: string;
  style?: React.CSSProperties;
}

export function Icon(props: IconProps) {
  const { name, size = 16, label, className = "", style } = props;
  const glyph = DOMAIN_ICON_MAP[name] || name;
  const aria = label ? { role: "img" as const, "aria-label": label } : { "aria-hidden": true as const };
  return (
    <span
      className={"vn-icon vn-icon--" + glyph + (className ? " " + className : "")}
      style={{ width: size, height: size, ...style }}
      {...aria}
    />
  );
}

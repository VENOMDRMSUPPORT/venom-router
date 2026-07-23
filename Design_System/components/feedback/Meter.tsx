import * as React from "react";

export type MeterState = "normal" | "unknown" | "unavailable";

export interface MeterProps {
  /** Current value. Ignored (and never rendered) when `state` is `unknown`/`unavailable`. */
  value?: number;
  max?: number;
  warnAt?: number;
  criticalAt?: number;
  /** @deprecated use `state="unknown"` — kept for existing callers (e.g. Quota.tsx). */
  unknown?: boolean;
  /**
   * `normal` (default when a value is given) renders a real meter — including the
   * honest edge cases `0` (empty fill, still `role="meter"`) and "exhausted" (`value ===
   * max`, `is-critical`). `unknown` = missing evidence (dashed/hatched, no fabricated
   * number). `unavailable` = there is no meter concept for this metric at all (distinct
   * from "we don't have evidence yet") — flat, non-hatched, no number.
   */
  state?: MeterState;
  label?: string;
  className?: string;
}

/** Meter — a bounded quantity with an honest missing-evidence state. Never fabricates a numeric value for `unknown`/`unavailable`; both keep the accessible name and expose the state as text, not color alone. */
export function Meter(props: MeterProps) {
  const { value, max = 100, warnAt = 0.75, criticalAt = 0.9, unknown = false, state, label, className = "" } = props;
  const resolved: MeterState = state || (unknown ? "unknown" : "normal");

  if (resolved === "unknown" || resolved === "unavailable") {
    const word = resolved === "unknown" ? "Unknown" : "Unavailable";
    const modifierClass = resolved === "unknown" ? "is-unknown" : "is-unavailable";
    return (
      <div
        className={("vn-meter " + modifierClass + " " + className).trim()}
        role="img"
        aria-label={(label ? label + ": " : "") + word}
      >
        <div></div>
      </div>
    );
  }

  const safeValue = Math.max(0, value ?? 0);
  const ratio = Math.max(0, Math.min(1, safeValue / max));
  const stateClass = ratio >= criticalAt ? "is-critical" : ratio >= warnAt ? "is-warning" : "";
  return (
    <div
      className={("vn-meter " + stateClass + " " + className).trim()}
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={max}
      aria-valuenow={safeValue}
      aria-valuetext={safeValue.toLocaleString() + " of " + max.toLocaleString()}
    >
      <div style={{ width: ratio * 100 + "%" }}></div>
    </div>
  );
}

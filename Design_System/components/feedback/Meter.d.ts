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
export declare function Meter(props: MeterProps): React.JSX.Element;

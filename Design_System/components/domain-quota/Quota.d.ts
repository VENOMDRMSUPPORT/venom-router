import * as React from "react";
export type QuotaFreshness = "fresh" | "stale" | "unknown";
export interface QuotaFreshnessBadgeProps {
    state?: QuotaFreshness;
    age?: React.ReactNode;
}
/** QuotaFreshnessBadge — evidence freshness; fresh renders nothing (no noise). */
export declare function QuotaFreshnessBadge(props: QuotaFreshnessBadgeProps): React.JSX.Element;
/** The evidence sources a quota window can be sourced from — never conflated. */
export type QuotaEvidenceSource = "provider_evidence" | "local_safety" | "owner_override";
export interface QuotaResetCountdownProps {
    resetAt?: React.ReactNode;
    remaining?: React.ReactNode;
}
/** QuotaResetCountdown — reset moment + remaining time; absent reset (balance windows) says so. */
export declare function QuotaResetCountdown(props: QuotaResetCountdownProps): React.JSX.Element;
export interface QuotaUnknownStateProps {
    note?: React.ReactNode;
}
/** QuotaUnknownState — the honest no-evidence rendering. Never a number, never a fill. */
export declare function QuotaUnknownState(props: QuotaUnknownStateProps): React.JSX.Element;
export type QuotaWindowState = "available" | "insufficient" | "exhausted" | "unknown" | "stale";
export interface QuotaWindowMeterProps {
    used?: number;
    total?: number | null;
    reserved?: number;
    unit?: string;
    state?: QuotaWindowState;
    label?: string;
}
/** QuotaWindowMeter — one window's meter + figures. state: available|insufficient|exhausted|unknown|stale. */
export declare function QuotaWindowMeter(props: QuotaWindowMeterProps): React.JSX.Element;
/** QuotaMeter — docs/07 inventory alias. */
export declare function QuotaMeter(props: QuotaWindowMeterProps): React.JSX.Element;
export interface QuotaWindowCardProps {
    name: React.ReactNode;
    windowKey: React.ReactNode;
    source?: QuotaEvidenceSource;
    state?: QuotaWindowState;
    used?: number;
    total?: number;
    reserved?: number;
    unit?: string;
    resetAt?: React.ReactNode;
    resetIn?: React.ReactNode;
    freshness?: QuotaFreshness;
    age?: React.ReactNode;
}
/** QuotaWindowCard — one quota window: identity (mono window_key), source, state, meter, reset, freshness. */
export declare function QuotaWindowCard(props: QuotaWindowCardProps): React.JSX.Element;
export interface LocalSafetyBudgetIndicatorProps {
    concurrencyUsed?: number;
    concurrencyCap?: number;
    consumptionUsed?: number;
    consumptionCap?: number;
    consumptionUnit?: string;
}
/** LocalSafetyBudgetIndicator — Venom's own owner-policy budget; explicitly labeled, never presented as provider evidence. */
export declare function LocalSafetyBudgetIndicator(props: LocalSafetyBudgetIndicatorProps): React.JSX.Element;
export type ReservationState = "reserved" | "reconciliation_pending" | "settled" | "released" | "unknown_consumption";
export interface ReservationStateBadgeProps {
    state: ReservationState;
    confidence?: "low";
}
/** ReservationStateBadge — unresolved possible consumption is NEVER neutral/success. */
export declare function ReservationStateBadge(props: ReservationStateBadgeProps): React.JSX.Element;
export interface ReconciliationStatusProps {
    state: ReservationState;
    attemptId?: React.ReactNode;
    windows?: string[];
    nextRetry?: React.ReactNode;
    attempts?: React.ReactNode;
    onResync?: () => void;
    onAcceptEstimate?: () => void;
}
/** ReconciliationStatus — a pending/terminal reconciliation item with owner actions. */
export declare function ReconciliationStatus(props: ReconciliationStatusProps): React.JSX.Element;
export interface QuotaSummaryWindow {
    windowKey: string;
    used?: number;
    total?: number;
    unit?: string;
    state?: QuotaWindowState;
    freshness?: QuotaFreshness;
    age?: React.ReactNode;
    reserved?: number;
}
export interface MultiWindowQuotaSummaryProps {
    windows?: QuotaSummaryWindow[];
}
/** MultiWindowQuotaSummary — the most-restrictive-wins account rollup + per-window mini list. */
export declare function MultiWindowQuotaSummary(props: MultiWindowQuotaSummaryProps): React.JSX.Element;

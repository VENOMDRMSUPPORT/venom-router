import * as React from "react";
export type Tier = "lite" | "pro" | "max";
export interface TierBadgeProps {
    tier: Tier;
    showId?: boolean;
}
/** TierBadge — THE single way a tier is labeled anywhere. */
export declare function TierBadge(props: TierBadgeProps): React.JSX.Element;
export interface TierPolicySummaryProps {
    tier: Tier;
}
/** TierPolicySummary — the tier's policy facts: funding rule, context ceiling, thinking budget, fallback depth. */
export declare function TierPolicySummary(props: TierPolicySummaryProps): React.JSX.Element;
export interface CandidateRejectionReasonProps {
    code: string;
    blocking?: boolean;
    detail?: string;
}
/** CandidateRejectionReason — a typed exclusion code, verbatim, mono. */
export declare function CandidateRejectionReason(props: CandidateRejectionReasonProps): React.JSX.Element;
export declare function ReasonCode(props: CandidateRejectionReasonProps): React.JSX.Element;
export interface ScoreFactor {
    name: string;
    value: number;
    missing?: boolean;
    weight?: number;
}
export interface CandidateScoreBreakdownProps {
    factors?: ScoreFactor[];
    total?: number;
}
/** CandidateScoreBreakdown — Step-5 factor bars (0-1, neutral 0.5 for missing). */
export declare function CandidateScoreBreakdown(props: CandidateScoreBreakdownProps): React.JSX.Element;
export interface CompetitiveBandCandidate {
    name: string;
    quality: number;
}
export interface CompetitiveBandIndicatorProps {
    tier?: Tier;
    top?: number;
    candidates?: CompetitiveBandCandidate[];
}
/** CompetitiveBandIndicator — quality band below the top candidate (Pro <= 0.08, Max <= 0.03); never auto-widened. */
export declare function CompetitiveBandIndicator(props: CompetitiveBandIndicatorProps): React.JSX.Element;
export interface WorkloadProfileBadgeProps {
    properties?: string[];
}
/** WorkloadProfileBadge — deterministic multi-label bucket (normalized → sorted → deduped). */
export declare function WorkloadProfileBadge(props: WorkloadProfileBadgeProps): React.JSX.Element;
export interface CooldownBadgeProps {
    scope?: string;
    retryAfter?: React.ReactNode;
}
/** CooldownBadge — scoped cooldown as an eligibility input (not an error). */
export declare function CooldownBadge(props: CooldownBadgeProps): React.JSX.Element;
export type BreakerState = "closed" | "open" | "half_open";
export interface CircuitBreakerStateProps {
    state?: BreakerState;
    scope?: string;
    reopensIn?: React.ReactNode;
    cycle?: number;
}
/** CircuitBreakerState — scoped breaker with adaptive backoff note. */
export declare function CircuitBreakerState(props: CircuitBreakerStateProps): React.JSX.Element;
export type FallbackOutcome = "succeeded" | "failed" | "skipped";
export interface FallbackAttempt {
    route: React.ReactNode;
    outcome?: FallbackOutcome;
    code?: string;
    detail?: string;
}
export interface FallbackChainProps {
    attempts?: FallbackAttempt[];
    budget?: React.ReactNode;
}
/** FallbackChain — the attempt sequence; funding/capability boundaries are never crossed. */
export declare function FallbackChain(props: FallbackChainProps): React.JSX.Element;
export type RoutingReservationOutcome = "reserved" | "reconciliation_pending" | "settled" | "released" | "unknown_consumption";
export interface RoutingAttempt {
    route: React.ReactNode;
    outcome?: "succeeded" | "failed";
    reservation?: RoutingReservationOutcome;
    code?: React.ReactNode;
    detail?: React.ReactNode;
    latency?: React.ReactNode;
}
export interface RoutingAttemptTimelineProps {
    attempts?: RoutingAttempt[];
}
/** RoutingAttemptTimeline — reserve → execute → settle/release per attempt. */
export declare function RoutingAttemptTimeline(props: RoutingAttemptTimelineProps): React.JSX.Element;
export interface RouteCandidate {
    route: React.ReactNode;
    funding?: "free" | "paid" | "unknown";
    quality?: number;
    score?: number;
    outcome?: "chosen" | "excluded" | "in_band";
    reasons?: string[];
    clamp?: React.ReactNode;
}
export interface RouteDecisionTraceProps {
    candidates?: RouteCandidate[];
    label?: string;
}
/** RouteDecisionTrace — "why this route?": candidates, scores, typed exclusions, chosen row emphasized. */
export declare function RouteDecisionTrace(props: RouteDecisionTraceProps): React.JSX.Element;
/** RouteExplain — docs/07 inventory alias. */
export declare function RouteExplain(props: RouteDecisionTraceProps): React.JSX.Element;
export interface FundingMixIndicatorProps {
    /** 0-1. */
    paidShare: number;
    /** 0-1. */
    target?: number;
    bucket?: React.ReactNode;
    sample?: React.ReactNode;
}
/** FundingMixIndicator — PRO ONLY: realized paid share vs the ~25% target, per workload bucket cell. */
export declare function FundingMixIndicator(props: FundingMixIndicatorProps): React.JSX.Element;
export interface QuotaFairnessAccount {
    name: string;
    /** Capacity weight, 0-1. */
    weight: number;
    /** Realized share, 0-1. */
    realized: number;
    saturated?: boolean;
}
export interface QuotaFairnessIndicatorProps {
    accounts?: QuotaFairnessAccount[];
}
/** QuotaFairnessIndicator — MAX ONLY: DRR capacity weight vs realized share; saturated accounts marked ineligible. No funding target exists. */
export declare function QuotaFairnessIndicator(props: QuotaFairnessIndicatorProps): React.JSX.Element;

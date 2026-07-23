import * as React from "react";
export type CertState = "discovered" | "observed" | "probing" | "certified" | "suspended" | "expired";
export interface CertificationStateBadgeProps {
    state: CertState;
    reason?: React.ReactNode;
}
export declare function CertificationStateBadge(props: CertificationStateBadgeProps): React.JSX.Element;
/** CertificationState — brief alias (docs/07 inventory name). */
export declare function CertificationState(props: CertificationStateBadgeProps): React.JSX.Element;
export type CapabilityTruth = "unknown" | "supported" | "unsupported";
export interface CapabilityTruthBadgeProps {
    /** Falls back to "unknown" when omitted. */
    truth?: CapabilityTruth;
}
/** CapabilityTruthBadge — unknown = missing evidence (dashed); unsupported = confirmed absence (quiet, not an alarm). */
export declare function CapabilityTruthBadge(props: CapabilityTruthBadgeProps): React.JSX.Element;
export interface CapabilityIconProps {
    /** A domain capability concept (chat, tools, vision, …) or custom glyph name. */
    capability: string;
    truth?: CapabilityTruth;
    showLabel?: boolean;
}
/** CapabilityIcon — one capability chip: icon + short label + truth treatment. */
export declare function CapabilityIcon(props: CapabilityIconProps): React.JSX.Element;
export type CapabilityTruths = Record<string, CapabilityTruth>;
export interface ModelCapabilitySetProps {
    /** Pass {chat:"supported", tools:"unknown", ...}. */
    truths?: CapabilityTruths;
    capabilities?: string[];
    showLabels?: boolean;
}
/** ModelCapabilitySet — the offering-operation truth set. Pass {chat:"supported", tools:"unknown", ...}. */
export declare function ModelCapabilitySet(props: ModelCapabilitySetProps): React.JSX.Element;
export declare function CapabilitySet(props: ModelCapabilitySetProps): React.JSX.Element;
export interface RoutableIndicatorProps {
    state: CertState;
    truths?: CapabilityTruths;
    required?: string[];
}
/** RoutableIndicator — the conjunction, made visible:
    routable = certification state certified AND every required truth supported. */
export declare function RoutableIndicator(props: RoutableIndicatorProps): React.JSX.Element;
export interface CertificationTimelineProps {
    state: CertState;
}
/** CertificationTimeline — lifecycle position; suspended/expired render as a blocked branch off their source state. */
export declare function CertificationTimeline(props: CertificationTimelineProps): React.JSX.Element;
export type ProbeExecutionState = "pending" | "running" | "succeeded" | "inconclusive" | "retryable_failure" | "terminal_failure";
export interface ProbeStatusProps {
    state: ProbeExecutionState;
    note?: React.ReactNode;
}
/** ProbeStatus — probe EXECUTION state. Infra failures never flip capability truth. */
export declare function ProbeStatus(props: ProbeStatusProps): React.JSX.Element;
export interface ProbeResultSummaryProps {
    operation: string;
    execution: ProbeExecutionState;
    truth?: CapabilityTruth;
    note?: React.ReactNode;
    at?: React.ReactNode;
}
/** ProbeResultSummary — one probe outcome: operation, execution state, truth effect, evidence note. */
export declare function ProbeResultSummary(props: ProbeResultSummaryProps): React.JSX.Element;
export type MetadataSource = "owner_override" | "probe" | "provider_metadata" | "provider_discovery" | "external_registry" | "heuristic" | "unknown";
export interface MetadataSourceIndicatorProps {
    source: MetadataSource;
}
/** MetadataSourceIndicator — evidence provenance, precedence rank visible (owner override > probe > provider metadata > discovery > registry > heuristic > unknown). */
export declare function MetadataSourceIndicator(props: MetadataSourceIndicatorProps): React.JSX.Element;
export interface MetadataConfidenceIndicatorProps {
    confidence?: number;
    exactMatch?: boolean;
    stale?: boolean;
    observedAt?: React.ReactNode;
}
/** MetadataConfidenceIndicator — confidence + exact-identity + freshness of one fact. */
export declare function MetadataConfidenceIndicator(props: MetadataConfidenceIndicatorProps): React.JSX.Element;
export interface ContextWindowDisplayProps {
    tokens?: number | null;
    verified?: boolean;
    source?: React.ReactNode;
}
/** ContextWindowDisplay — verified context tokens. Unknown renders as the word, never 0; unverified is ineligible. */
export declare function ContextWindowDisplay(props: ContextWindowDisplayProps): React.JSX.Element;
export type ModelAvailability = "available" | "withdrawn" | "catalog_only";
export interface ModelIdentityProps {
    name: React.ReactNode;
    providerModelId?: React.ReactNode;
    availability?: ModelAvailability;
}
/** ModelIdentity — display name + provider-scoped external model id (mono) + availability. */
export declare function ModelIdentity(props: ModelIdentityProps): React.JSX.Element;
export interface ModelOfferingRowProps {
    identity?: React.ReactNode;
    context?: React.ReactNode;
    capabilities?: React.ReactNode;
    certification?: React.ReactNode;
    routable?: React.ReactNode;
    actions?: React.ReactNode;
}
/** ModelOfferingRow — one offering line: identity · context · capability truths · certification · routable. */
export declare function ModelOfferingRow(props: ModelOfferingRowProps): React.JSX.Element;

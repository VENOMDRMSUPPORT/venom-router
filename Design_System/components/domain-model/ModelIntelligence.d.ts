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
/** How a certified+supported capability was earned: "probed" when a real
 * runtime measurement proved it, "declared" when it was certified straight
 * from the provider's own catalog metadata with no probe evidence, and ""
 * when the capability is not certified+supported at all — provenance only
 * qualifies an EARNED certification, so "" carries no provenance treatment.
 * Mirrors the API's OfferingCapability.provenance verbatim (dashboard's
 * controlClient.ts). */
export type CapabilityProvenance = "probed" | "declared" | "";
export interface CapabilityIconProps {
    /** A domain capability concept (chat, tools, vision, …) or custom glyph name. */
    capability: string;
    truth?: CapabilityTruth;
    /** Owner requirement (2026-08-05, restored 2026-08-06 after a wholesale
     * component deletion silently dropped it): a "declared" capability must
     * read apart from a "probed" one WITHOUT hovering — never colour alone.
     * Rendered as `data-provenance` (the CSS hook for the dotted-border
     * treatment on "declared" — see css/components-domain.css, chosen
     * specifically because it does not collide with data-truth="unknown"'s own
     * dashed border) AND as a real accessible carrier (`aria-label` +
     * `tabIndex`, not `title` alone — see the chip's own JSX below), so the
     * distinction survives for keyboard and screen-reader users too, not only
     * sighted mouse-hover ones. Omit (or pass "") for a capability with no
     * provenance to show. */
    provenance?: CapabilityProvenance;
    showLabel?: boolean;
}
/** CapabilityIcon — one capability chip: icon + short label + truth treatment
 * + (when earned) a declared/probed provenance treatment.
 *
 * The truth/provenance distinction (`title`) used to live ONLY on a `title`
 * attribute of this plain `<span>` — no role, no tabindex, no aria-label
 * (whole-branch review, 2026-08-06). A keyboard-only user has no way to
 * reveal a native `title` tooltip, and `title` on a generic element is not
 * reliably exposed to assistive tech at all. `tabIndex={0}` puts the chip in
 * the tab order; `aria-label` gives it a real accessible name carrying the
 * SAME text as `title`, so a screen reader announces it on focus without
 * requiring a hover. `role="img"` (the same role CertificationTimeline and
 * MetadataConfidenceIndicator already use for this exact shape — a small
 * glyph plus a summary that doesn't line up 1:1 with its visible text)
 * matters beyond convention here: a plain `<span>`'s implicit role is
 * "generic", which the ARIA spec marks "name from: prohibited" — `aria-label`
 * on a bare `<span>` can be dropped by the accessibility tree entirely. */
export declare function CapabilityIcon(props: CapabilityIconProps): React.JSX.Element;
export type CapabilityTruths = Record<string, CapabilityTruth>;
export type CapabilityProvenances = Record<string, CapabilityProvenance>;
export interface ModelCapabilitySetProps {
    /** Pass {chat:"supported", tools:"unknown", ...}. */
    truths?: CapabilityTruths;
    /** Pass {tools:"declared", vision:"probed", ...} — same shape as `truths`,
     * looked up by the same capability key. Missing/omitted reads as "" (no
     * provenance treatment), matching CapabilityIcon's own default. */
    provenances?: CapabilityProvenances;
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

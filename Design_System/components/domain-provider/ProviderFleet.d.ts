import * as React from "react";
import { MarkProps } from "../display/Mark";
export type ConnectionState = "connecting" | "connected" | "stopped" | "disconnected";
export type HealthState = "unknown" | "healthy" | "degraded" | "unavailable" | "expired";
export type DisplayStatus = HealthState | "connecting" | "stopped" | "disconnected" | "reauthenticating" | "cooling_down";
export interface ConnectionStateBadgeProps {
    state: ConnectionState;
}
export declare function ConnectionStateBadge(props: ConnectionStateBadgeProps): React.JSX.Element;
export interface HealthStateBadgeProps {
    state: HealthState;
}
export declare function HealthStateBadge(props: HealthStateBadgeProps): React.JSX.Element;
export interface AccountStatusProps {
    status: DisplayStatus;
    retryAfter?: React.ReactNode;
    reason?: string;
}
/** AccountStatus — renders the DERIVED display_status; retryAfter shown for cooling_down. */
export declare function AccountStatus(props: AccountStatusProps): React.JSX.Element;
export type FundingState = "free" | "paid" | "unknown";
/** The canonical 4-value funding evidence source (docs/02 §2). */
export type FundingSource = "provider_policy" | "provider_evidence" | "owner_policy" | "owner_override";
export interface FundingBadgeProps {
    funding?: FundingState;
    plan?: React.ReactNode;
    locked?: boolean;
    source?: FundingSource;
    stale?: boolean;
    conflicting?: boolean;
}
/** FundingBadge — account-scoped funding classification (never provider-level).
    Renders plan string when known; locked / override / stale / conflicting modifiers. */
export declare function FundingBadge(props: FundingBadgeProps): React.JSX.Element;
/** FundingClassBadge — alias of FundingBadge (inventory name). */
export declare function FundingClassBadge(props: FundingBadgeProps): React.JSX.Element;
export interface FundingSourceIndicatorProps {
    source?: FundingSource;
}
/** FundingSourceIndicator — the canonical 4-value evidence source, verbatim mono chip. */
export declare function FundingSourceIndicator(props: FundingSourceIndicatorProps): React.JSX.Element;
export interface FundingEvidenceIndicatorProps {
    funding?: FundingState;
    source?: FundingSource;
    confidence?: number;
    observedAt?: React.ReactNode;
    stale?: boolean;
    locked?: boolean;
}
/** FundingEvidenceIndicator — current evidence row: value + source + confidence + freshness. */
export declare function FundingEvidenceIndicator(props: FundingEvidenceIndicatorProps): React.JSX.Element;
export interface ConfidenceDotsProps {
    /** 0-1. */
    value?: number;
    label?: string;
}
export declare function ConfidenceDots(props: ConfidenceDotsProps): React.JSX.Element;
export type VerificationLevel = "proven" | "partial" | "unknown";
export interface ProviderVerificationConfidenceProps {
    level: VerificationLevel;
}
/** ProviderVerificationConfidence — planning confidence of the wire contract (docs/03): proven | partial | unknown. */
export declare function ProviderVerificationConfidence(props: ProviderVerificationConfidenceProps): React.JSX.Element;
export type CredentialKind = "api_key" | "oauth2" | "github_oauth" | "copilot_service";
export interface CredentialKindBadgeProps {
    kind: CredentialKind;
    state?: "active" | "staged" | "retired";
    expiresAt?: React.ReactNode;
}
/** CredentialKindBadge — one active credential per (account, kind); kinds coexist. */
export declare function CredentialKindBadge(props: CredentialKindBadgeProps): React.JSX.Element;
export type ReauthState = "idle" | "staged" | "validating" | "swapping" | "successful" | "failed" | "rollback" | "interrupted";
export interface ReauthenticationStatusProps {
    state?: ReauthState;
}
export declare function ReauthenticationStatus(props: ReauthenticationStatusProps): React.JSX.Element;
export interface AccountCooldownIndicatorProps {
    scope?: React.ReactNode;
    until?: React.ReactNode;
    retryAfter?: React.ReactNode;
}
/** AccountCooldownIndicator — scoped cooldown with retry-after. Never renders as a permanent failure. */
export declare function AccountCooldownIndicator(props: AccountCooldownIndicatorProps): React.JSX.Element;
export interface AccountIdentityProps {
    name?: React.ReactNode;
    email?: React.ReactNode;
    externalId?: React.ReactNode;
    plan?: React.ReactNode;
}
/** AccountIdentity — display name/email + immutable external id (mono) + plan. */
export declare function AccountIdentity(props: AccountIdentityProps): React.JSX.Element;
export type AuthMode = "oauth2" | "api_key" | "custom_openai";
export interface ProviderBadgeProps {
    authMode?: AuthMode;
}
/** ProviderBadge — the integration's auth mode (a provider is never "free" or "paid"). */
export declare function ProviderBadge(props: ProviderBadgeProps): React.JSX.Element;
export declare function ProviderMark(props: MarkProps): React.JSX.Element;
export interface ProviderSummaryCardProps {
    name: React.ReactNode;
    slug?: string;
    authMode?: AuthMode;
    accountCount?: number;
    healthyCount?: number;
    verification?: VerificationLevel;
    setupRequired?: boolean;
    /** Missing environment variable NAMES only — values are never shown. */
    missingEnv?: string[];
    children?: React.ReactNode;
    actions?: React.ReactNode;
}
/** ProviderSummaryCard — the fleet's provider row header: integration facts +
    aggregate account health. setupRequired lists missing env var NAMES only. */
export declare function ProviderSummaryCard(props: ProviderSummaryCardProps): React.JSX.Element;
export interface ProviderAccountRowProps {
    identity?: React.ReactNode;
    status?: React.ReactNode;
    retryAfter?: React.ReactNode;
    funding?: React.ReactNode;
    quota?: React.ReactNode;
    actions?: React.ReactNode;
}
/** ProviderAccountRow — one connected account under a provider: identity ·
    status · funding · quota summary · actions. Funding lives HERE, never on the provider. */
export declare function ProviderAccountRow(props: ProviderAccountRowProps): React.JSX.Element;

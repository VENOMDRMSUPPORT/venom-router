import * as React from "react";
export type SessionState = "active" | "idle_warning" | "expired" | "absolute_expiry" | "revoked" | "reverification_required" | "reverified" | "unauthenticated" | "locked_out";
export interface OwnerSessionStatusProps {
    state?: SessionState;
    idleIn?: string;
    absoluteIn?: string;
    reverifiedFor?: React.ReactNode;
    retryAfter?: React.ReactNode;
}
/** OwnerSessionStatus — the topbar session pill: state + countdowns. */
export declare function OwnerSessionStatus(props: OwnerSessionStatusProps): React.JSX.Element;
export interface SessionExpiryWarningProps {
    kind?: "idle" | "absolute";
    inTime?: string;
    onContinue?: () => void;
}
/** SessionExpiryWarning — pre-expiry banner with continue action. */
export declare function SessionExpiryWarning(props: SessionExpiryWarningProps): React.JSX.Element;
export interface ReverificationPromptProps {
    open: boolean;
    action?: string;
    error?: React.ReactNode;
    locked?: boolean;
    retryAfter?: React.ReactNode;
    onConfirm?: (password: string) => void;
    onCancel?: () => void;
}
/** ReverificationPrompt — modal password proof gating a sensitive action (5-minute freshness). */
export declare function ReverificationPrompt(props: ReverificationPromptProps): React.JSX.Element;
export interface SecretRevealControlProps {
    masked: React.ReactNode;
    secret: string;
    revealed?: boolean;
    blocked?: boolean;
    onRevealRequest?: () => void;
    onHide?: () => void;
    onCopy?: () => void;
    label?: string;
}
/** SecretRevealControl — masked by default; reveal gated on fresh re-verification; cleared on hide/blur; never persisted in the DOM after hide. */
export declare function SecretRevealControl(props: SecretRevealControlProps): React.JSX.Element;
export interface APIKeyPrefixProps {
    prefix: React.ReactNode;
    label?: string;
}
/** APIKeyPrefix — the only persistent representation of a Venom key: prefix + fingerprint, mono. */
export declare function APIKeyPrefix(props: APIKeyPrefixProps): React.JSX.Element;
export interface APIKeyCreationResultProps {
    rawKey: string;
    keyLabel?: string;
    onDone?: () => void;
}
/** APIKeyCreationResult — the ONE-TIME raw key reveal after POST /keys. */
export declare function APIKeyCreationResult(props: APIKeyCreationResultProps): React.JSX.Element;
export type BackupState = "idle" | "running" | "completed" | "failed";
export interface BackupStatusProps {
    state?: BackupState;
    artifact?: React.ReactNode;
    at?: React.ReactNode;
    code?: React.ReactNode;
}
export declare function BackupStatus(props: BackupStatusProps): React.JSX.Element;
export type RestoreState = "idle" | "validating" | "decrypting" | "verifying" | "swapped" | "failed";
export interface RestoreStatusProps {
    state?: RestoreState;
    code?: React.ReactNode;
}
export declare function RestoreStatus(props: RestoreStatusProps): React.JSX.Element;
export interface DestructiveActionConfirmationProps {
    open: boolean;
    title?: React.ReactNode;
    consequence?: React.ReactNode;
    /** When set, requires typing this exact word before the confirm button arms. */
    confirmWord?: string;
    confirmLabel?: React.ReactNode;
    onConfirm?: () => void;
    onCancel?: () => void;
}
/** DestructiveActionConfirmation — blocking dialog for irreversible operations; optional type-to-confirm. */
export declare function DestructiveActionConfirmation(props: DestructiveActionConfirmationProps): React.JSX.Element;

import * as React from "react";
export interface ErrorStateAction {
    label: React.ReactNode;
    onClick: () => void;
    icon?: string;
}
export interface ErrorStateProps {
    /** Stable, typed error code (e.g. `quota_exhausted`) — rendered verbatim in mono next to the title. Never render raw provider text here. */
    code?: string;
    /** Safe, written title — say specifically what failed, never a vague placeholder. */
    title: React.ReactNode;
    /** Actionable, human sentence — what happened and what the operator can do. */
    description?: React.ReactNode;
    /** `page` renders a centered full-page state (empty view replacement); `inline` renders a compact section-level state. */
    variant?: "page" | "inline";
    /** Reference/trace id for support or cross-referencing diagnostics. Rendered mono + copyable. */
    traceId?: string;
    /** Retry action — omitted when the failure is not retryable. */
    onRetry?: () => void;
    retryLabel?: React.ReactNode;
    /** A secondary, non-destructive escape hatch (e.g. "Go to diagnostics", "Contact support"). */
    secondaryAction?: ErrorStateAction;
    /** Secret-safe technical details (sanitized stack/raw payload). Rendered behind a collapsed disclosure, never auto-expanded, never containing credentials. */
    details?: string;
    icon?: string;
    className?: string;
}
/**
 * ErrorState — the written, actionable failure view (full-page or inline). Pairs with
 * EmptyState (nothing to show) and TypedErrorDisplay (domain-diagnostics' typed error
 * envelope for trace rows) but is the general-purpose "this section/page failed to load"
 * composite named in the component inventory (docs/07 §5).
 */
export declare function ErrorState(props: ErrorStateProps): React.JSX.Element;

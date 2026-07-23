import * as React from "react";
import { Icon } from "../icons/Icon";
import { Button } from "../actions/Button";
import { CopyButton } from "../actions/CopyButton";

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
export function ErrorState(props: ErrorStateProps) {
  const {
    code,
    title,
    description,
    variant = "inline",
    traceId,
    onRetry,
    retryLabel = "Retry",
    secondaryAction,
    details,
    icon = "circle-x",
    className = "",
  } = props;

  const isPage = variant === "page";
  const cls = ["vn-error-state", "vn-error-state--" + variant, className].filter(Boolean).join(" ");

  return (
    <div className={cls} role="alert">
      <Icon name={icon} size={isPage ? 28 : 20} />
      <div className="vn-error-state-title">
        {code ? <span className="vn-reason-code vn-reason-code--blocking">{code}</span> : null}
        <span>{title}</span>
      </div>
      {description ? <div className="vn-error-state-desc">{description}</div> : null}
      {traceId ? (
        <div className="vn-error-state-trace">
          <span className="vn-caption">Reference</span>
          <span className="vn-code-inline">{traceId}</span>
          <CopyButton value={traceId} label="Copy reference id" />
        </div>
      ) : null}
      {onRetry || secondaryAction ? (
        <div className="vn-error-state-actions">
          {onRetry ? (
            <Button variant="primary" icon="refresh-cw" onClick={onRetry}>
              {retryLabel}
            </Button>
          ) : null}
          {secondaryAction ? (
            <Button variant="ghost" icon={secondaryAction.icon} onClick={secondaryAction.onClick}>
              {secondaryAction.label}
            </Button>
          ) : null}
        </div>
      ) : null}
      {details ? (
        <details className="vn-payload vn-error-state-details">
          <summary>
            <Icon name="chevron-right" size={12} />
            Technical details
            <span className="vn-redaction-note" style={{ marginLeft: "auto" }}>
              <Icon name="shield" size={11} />
              sanitized · credentials fully redacted
            </span>
          </summary>
          <pre className="vn-codeblock vn-scroll" style={{ maxHeight: 200 }}>
            <code>{details}</code>
          </pre>
        </details>
      ) : null}
    </div>
  );
}

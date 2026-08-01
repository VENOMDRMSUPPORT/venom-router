import { useEffect, useState } from "react";
import { Badge, Banner, Button } from "@venom/design-system/primitives";
import { ReasonCode } from "@venom/design-system/domain";
import {
  getReviewCensus,
  isSessionExpired,
  toApiError,
  type AuthApiError,
  type ReviewCensus,
} from "../api/controlClient";

export interface ReviewQueueBannerProps {
  onSessionExpired: () => void;
  /** Filters the Models surface down to the offerings that need review. */
  onReviewBacklog: () => void;
}

/**
 * Readable glosses for the admission reason codes this console knows.
 *
 * This map NEVER replaces a code — the typed code is always rendered verbatim
 * beside its gloss. Renaming a reason in the UI would break the operator's
 * ability to match what they see here against the routing diagnostics, the audit
 * log, and 04 §5 itself.
 *
 * A code MISSING from this map is not dropped. It renders with its code and an
 * "unrecognized" marker, because a console older than its server must surface the
 * backlog it does not understand rather than hide it — an omitted row reads as
 * "nothing to see here".
 */
const REASON_LABELS: Record<string, string> = {
  identity_unresolved: "Canonical identity unresolved",
  context_unverified: "Context window unverified",
  capability_not_certified: "Capability not certified (state × truth)",
  funding_unknown: "Funding not explicit",
  no_healthy_account: "No healthy account with valid credentials",
  quota_exhausted: "Quota exhausted",
  quota_insufficient: "Quota insufficient for the request",
  cooling_down: "Cooling down",
};

/**
 * Why a reason could not be evaluated by a STANDING census.
 *
 * `quota_insufficient` gets a specific note because its unevaluability is a fact
 * about the spec, not a gap in the implementation: 04 §5 defines insufficiency
 * relative to "the request's estimated need", and a standing census has no
 * request to measure against. Saying merely "not evaluated" would invite someone
 * to file it as missing work.
 */
const NOT_EVALUATED_NOTES: Record<string, string> = {
  quota_insufficient: "Request-dependent — insufficiency is measured against one request's estimated need, and a standing census has no request.",
  identity_unresolved: "A catalog fact, outside the certification census.",
  context_unverified: "An offering fact, outside the certification census.",
  funding_unknown: "An account fact, outside the certification census.",
  no_healthy_account: "An account fact, outside the certification census.",
  quota_exhausted: "A quota-window fact, outside the certification census.",
  cooling_down: "A cooldown fact, outside the certification census.",
};

function reasonLabel(code: string): { label: string; recognized: boolean } {
  const known = REASON_LABELS[code];
  return known ? { label: known, recognized: true } : { label: "Unrecognized reason code — this console is older than the router", recognized: false };
}

/** One evaluated reason: the verbatim code, its gloss, and its count. */
function EvaluatedReasonRow(props: { reason: string; count: number; truncated: boolean }) {
  const { reason, count, truncated } = props;
  const { label, recognized } = reasonLabel(reason);

  return (
    <div className="flex flex-wrap items-center gap-2" data-testid={`review-reason-${reason}`}>
      <ReasonCode code={reason} blocking />
      <Badge tone={count > 0 ? "warning" : "healthy"} mono icon="hash">
        {/* `truncated` makes every count a FLOOR, not a total — the scan stopped
            at the limit, so saying "3" when it might be 300 is the difference
            between a small chore and an outage. */}
        {truncated ? `at least ${count}` : String(count)}
      </Badge>
      <span className="vn-caption">{label}</span>
      {recognized ? null : (
        <Badge tone="unknown" icon="circle-help">
          Unrecognized
        </Badge>
      )}
    </div>
  );
}

/** One NOT-evaluated reason: the verbatim code and the words "not evaluated" —
 * and deliberately no number of any kind. */
function NotEvaluatedReasonRow(props: { reason: string }) {
  const { reason } = props;
  const { label, recognized } = reasonLabel(reason);
  const note = NOT_EVALUATED_NOTES[reason];

  return (
    <div className="flex flex-wrap items-center gap-2" data-testid={`review-not-evaluated-${reason}`}>
      <ReasonCode code={reason} />
      {/* NOT a count. A `0` here would claim this check ran and found nothing. */}
      <Badge tone="unknown" icon="circle-help">
        Not evaluated
      </Badge>
      <span className="vn-caption">
        {recognized ? label : "Unrecognized reason code — this console is older than the router"}
        {note ? ` — ${note}` : ""}
      </span>
    </div>
  );
}

/**
 * The review-queue banner (P6-UI-012, 04 §5, 07 §5/§6): the certification review
 * backlog, grouped by the typed admission reason the API reported.
 *
 * Its whole job is to keep three different states visibly different:
 *
 *   a backlog          — a reason with a real count,
 *   an evaluated zero  — "we checked and found none" (the honest all-clear),
 *   not evaluated      — "this check did not run".
 *
 * The third is the one everything else gets wrong. Rendering it as `0`, or
 * leaving the row out, both read as the second — an all-clear for a check nobody
 * performed. So an unevaluated reason is named, marked "not evaluated", and
 * carries no number at all; and the all-clear itself is scoped out loud to what
 * was actually evaluated.
 *
 * Exported for reuse: the Overview surface renders this same component.
 */
export default function ReviewQueueBanner(props: ReviewQueueBannerProps) {
  const { onSessionExpired, onReviewBacklog } = props;

  const [census, setCensus] = useState<ReviewCensus | null>(null);
  const [loadError, setLoadError] = useState<AuthApiError | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      try {
        const result = await getReviewCensus();
        if (cancelled) return;
        setCensus(result);
      } catch (err) {
        if (cancelled) return;
        if (isSessionExpired(err)) {
          onSessionExpired();
          return;
        }
        setLoadError(toApiError(err));
      }
    }

    void load();
    return () => {
      cancelled = true;
    };
  }, [onSessionExpired]);

  if (loadError) {
    // Deliberately NOT an all-clear. "We could not ask" and "there is nothing
    // waiting" are different facts, and only one of them is good news.
    return (
      <Banner tone="warning">
        Could not load the review census ({loadError.code}) — the certification backlog is unknown right now.
      </Banner>
    );
  }

  if (census === null) {
    // Silent while loading: a banner is chrome around someone else's page, and
    // a spinner here would push the real content down on every navigation.
    return null;
  }

  const evaluated = census.by_reason;
  const total = evaluated.reduce((sum, r) => sum + r.count, 0);
  const hasBacklog = total > 0;

  return (
    <Banner
      tone={hasBacklog ? "warning" : "info"}
      actions={
        hasBacklog ? (
          <Button size="sm" variant="secondary" onClick={onReviewBacklog}>
            Review the backlog
          </Button>
        ) : undefined
      }
    >
      <div className="flex flex-col gap-2" data-testid="review-queue-banner">
        <span className="vn-title-sub">
          {hasBacklog
            ? `Certification review backlog${census.truncated ? " (at least" : " ("}${census.truncated ? ` ${total})` : `${total})`}`
            : "Certification review: nothing is waiting"}
        </span>
        {census.truncated ? (
          <span className="vn-caption">
            The census stopped at its scan limit of {census.limit}, so every count below is a floor, not a total.
          </span>
        ) : null}

        {evaluated.length === 0 ? (
          <span className="vn-caption">The API evaluated no reason at all.</span>
        ) : (
          evaluated.map((r) => (
            <EvaluatedReasonRow key={r.reason} reason={r.reason} count={r.count} truncated={census.truncated} />
          ))
        )}

        {census.not_evaluated_reasons.length > 0 ? (
          <>
            <span className="vn-caption">
              {/* Scoping the all-clear. Without this line, "nothing is waiting"
                  would read as a statement about routability as a whole, when it
                  only covers the one reason this census can compute. */}
              These blockers were NOT evaluated by this census, so the count above does not account for them:
            </span>
            {census.not_evaluated_reasons.map((reason) => (
              <NotEvaluatedReasonRow key={reason} reason={reason} />
            ))}
          </>
        ) : null}
      </div>
    </Banner>
  );
}

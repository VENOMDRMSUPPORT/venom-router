import { useCallback, useEffect, useState, type ReactNode } from "react";
import { Badge, Button, Card, EmptyState, ErrorState, KeyValueList, Spinner } from "@venom/design-system/primitives";
import { CandidateRejectionReason, TierBadge, type Tier as DSTier } from "@venom/design-system/domain";
import { TraceId } from "@venom/design-system/domain";
import {
  actOnReconciliation,
  getRouteExplanation,
  isSessionExpired,
  listReconciliation,
  listRouteDecisions,
  toApiError,
  type AuthApiError,
  type ReconciliationItem,
  type RouteAttempt,
  type RouteDecisionEntry,
  type RouteExplanation,
} from "../api/controlClient";

export interface DiagnosticsSurfaceProps {
  csrfToken: string;
  onSessionExpired: () => void;
  /** A request id to open on mount — the Overview surface links here as
   * `/diagnostics/routes/{request_id}`, and landing on the bare list would
   * silently discard the operator's intent. */
  deepLinkRequestID?: string;
}

/** How many decisions the list shows. */
const ROUTE_LIMIT = 50;

/** The DS tier vocabulary; anything else renders as itself rather than being
 * force-cast into a badge tone it has no token for. */
const DS_TIERS = new Set<string>(["lite", "pro", "max"]);

/** Reservation states no manual action can recover from (05 §4). */
const TERMINAL_RESERVATION_STATES = new Set<string>(["unknown_consumption", "settled", "released"]);

/** One card's independent fetch state — the containment pattern OverviewSurface
 * established, so one failing read model degrades one card. */
interface CardState<T> {
  data: T | null;
  error: AuthApiError | null;
}

const LOADING: CardState<never> = { data: null, error: null };

function TierChip(props: { tier: string }) {
  const { tier } = props;
  return DS_TIERS.has(tier) ? (
    <TierBadge tier={tier as DSTier} />
  ) : (
    <Badge tone="unknown" icon="circle-help">
      Unrecognized tier · {tier}
    </Badge>
  );
}

/** The rolled-up outcome of a decision's attempts, rendered by the same rules the
 * Overview surface uses — the same P6-CAPI-EXTRA rollup is being displayed.
 *
 * A null terminal status means the decision has NO attempt rows: it made no
 * attempt, which is not a status and is emphatically not success. A null total
 * latency means at least one attempt's latency is unknown, so the total is
 * unknown — "0 ms" would read as an instantaneous response. */
function RouteOutcomeCells(props: { entry: RouteDecisionEntry }) {
  const { entry } = props;
  const { terminal_status: status, total_latency_ms: latency } = entry.outcome;

  return (
    <div className="flex flex-wrap items-center gap-2">
      <span data-testid={`route-status-${entry.request_id}`}>
        {status === null ? (
          <Badge tone="unknown" icon="circle-help">
            No attempt recorded
          </Badge>
        ) : (
          <Badge
            tone={status === "success" ? "healthy" : status === "unknown" ? "unknown" : "warning"}
            icon="activity"
          >
            {status}
          </Badge>
        )}
      </span>
      <span className="vn-caption" data-testid={`route-latency-${entry.request_id}`}>
        {latency === null ? "Latency unknown" : `${latency} ms`}
      </span>
    </div>
  );
}

/** One attempt row inside the explanation. */
function AttemptRow(props: { attempt: RouteAttempt }) {
  const { attempt: a } = props;
  return (
    <div className="flex flex-wrap items-center gap-3" data-testid={`attempt-${a.attempt}`}>
      <Badge tone="info" mono icon="hash">
        #{a.attempt}
      </Badge>
      {/* status is already normalized to the closed vocabulary server-side, so it
          can never carry free provider text. */}
      <Badge tone={a.status === "success" ? "healthy" : a.status === "unknown" ? "unknown" : "warning"} icon="activity">
        {a.status}
      </Badge>
      <span className="vn-mono-sm">
        {a.provider_id} · {a.account_id}
      </span>
      <span className="vn-caption">
        {a.latency_ms === null ? "Latency unknown" : `${a.latency_ms} ms`}
      </span>
      <span className="vn-caption">
        {a.reservation_id === null ? "No reservation recorded" : `Reservation ${a.reservation_id}`}
      </span>
      {a.thinking_clamped ? (
        <Badge tone="info" icon="scissors">
          Thinking clamped
        </Badge>
      ) : null}
    </div>
  );
}

/**
 * One request's full explanation.
 *
 * Exclusion reason codes are rendered VERBATIM through the design system's
 * CandidateRejectionReason, which is defined as "a typed exclusion code, verbatim,
 * mono". A gloss beside a code is fine; rewording or inventing one is not — the
 * operator has to be able to match what they see here against the routing docs,
 * the audit log, and the router's own emitted codes. A code this console has never
 * heard of is rendered as-is rather than dropped, because dropping it would hide a
 * real exclusion.
 */
function ExplanationDetail(props: { state: CardState<RouteExplanation>; notFound: boolean }) {
  const { state, notFound } = props;

  if (notFound) {
    return (
      <div data-testid="route-explanation">
        <EmptyState
          icon="search-x"
          title="No record for this request id"
          description="No routing decision was recorded under that request id. This is not an empty candidate set — nothing was found to explain."
        />
      </div>
    );
  }
  if (state.error) {
    return (
      <div data-testid="route-explanation">
        <ErrorState
          variant="inline"
          code={state.error.code}
          title="Could not load the route explanation"
          description={state.error.message}
        />
      </div>
    );
  }
  if (state.data === null) {
    return (
      <div data-testid="route-explanation">
        <Spinner label="Loading the route explanation…" />
      </div>
    );
  }

  const x = state.data;
  const reasons = Object.entries(x.exclusion_reasons);
  const scores = x.scores === null ? [] : Object.entries(x.scores);

  return (
    <div className="flex flex-col gap-3" data-testid="route-explanation">
      <div className="flex flex-wrap items-center gap-2">
        <TierChip tier={x.tier} />
        <TraceId id={x.request_id} label="Request" />
        <span className="vn-caption">{x.workload_profile_bucket}</span>
      </div>

      <KeyValueList
        items={[
          { key: "Candidates considered", value: String(x.candidates.total), mono: true },
          { key: "Eligible groups", value: String(x.candidates.eligible_groups), mono: true },
          {
            key: "Candidate groups",
            value: x.candidates.group_keys.length === 0 ? "none recorded" : x.candidates.group_keys.join(", "),
            mono: true,
          },
        ]}
      />

      {/* The chosen row, emphasized. A decision that chose nothing says so rather
          than rendering a nameless provider from a null. */}
      <Card selected data-testid="explanation-chosen">
        <div className="flex flex-wrap items-center gap-2">
          {x.chosen.provider_id === null || x.chosen.provider_model_id === null ? (
            <Badge tone="warning" icon="circle-slash">
              No route chosen — every candidate was excluded
            </Badge>
          ) : (
            <>
              <Badge tone="healthy" icon="circle-check">
                Chosen
              </Badge>
              <span className="vn-mono-sm">
                {x.chosen.provider_id} · {x.chosen.provider_model_id}
              </span>
            </>
          )}
          {x.chosen.funding === null ? (
            <Badge tone="unknown" icon="circle-help">
              Funding unknown
            </Badge>
          ) : (
            <Badge tone="info" icon="wallet">
              {x.chosen.funding}
            </Badge>
          )}
        </div>
      </Card>

      <div className="flex flex-col gap-2">
        <span className="vn-caption">Exclusion reasons</span>
        {reasons.length === 0 ? (
          <span className="vn-caption">No candidate was excluded.</span>
        ) : (
          <div className="flex flex-wrap items-center gap-2">
            {reasons.map(([code, count]) => (
              <span key={code} className="flex items-center gap-1" data-testid={`exclusion-${code}`}>
                {/* Verbatim, mono, never reworded. */}
                <CandidateRejectionReason code={code} blocking />
                <Badge tone="warning" mono icon="hash">
                  {String(count)}
                </Badge>
              </span>
            ))}
          </div>
        )}
      </div>

      <div className="flex flex-col gap-2">
        <span className="vn-caption">Scores</span>
        {scores.length === 0 ? (
          // A NULL scores object means "no scores were recorded" — not "scored,
          // with no dimensions". Lite is unscored by policy, so this is normal.
          <span className="vn-caption">No score was recorded for this decision.</span>
        ) : (
          <KeyValueList items={scores.map(([name, value]) => ({ key: name, value: value.toFixed(4), mono: true }))} />
        )}
      </div>

      <div className="flex flex-col gap-1" data-testid="explanation-thinking">
        <span className="vn-caption">Thinking budget</span>
        <div className="flex flex-wrap items-center gap-2">
          <span className="vn-mono-sm">
            requested {x.thinking.requested ?? "unknown"} &rarr; applied {x.thinking.applied ?? "unknown"}
          </span>
          {x.thinking.tier_clamped ? (
            <Badge tone="info" icon="scissors">
              Clamped by the tier ceiling
            </Badge>
          ) : null}
          {x.thinking.certified_clamped ? (
            <Badge tone="info" icon="scissors">
              Clamped by the certified maximum
            </Badge>
          ) : null}
          {!x.thinking.tier_clamped && !x.thinking.certified_clamped ? (
            <span className="vn-caption">No clamp fired.</span>
          ) : null}
        </div>
      </div>

      <div className="flex flex-col gap-2">
        <span className="vn-caption">Attempts</span>
        {x.attempts.length === 0 ? (
          <span className="vn-caption">No attempt was recorded under this decision.</span>
        ) : (
          x.attempts.map((a) => <AttemptRow key={a.attempt} attempt={a} />)
        )}
      </div>
    </div>
  );
}

/** One reconciliation row. */
function ReconciliationRow(props: {
  item: ReconciliationItem;
  busy: boolean;
  onResync: (reservationID: string) => void;
}) {
  const { item, busy, onResync } = props;
  const terminal = TERMINAL_RESERVATION_STATES.has(item.state);

  return (
    <Card data-testid={`reconciliation-${item.reservation_id}`}>
      <div className="flex flex-col gap-2">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <TraceId id={item.reservation_id} label="Reservation" />
            <Badge tone={terminal ? "critical" : "warning"} mono icon="activity">
              {item.state}
            </Badge>
            <span className="vn-caption">
              {item.attempts} reconcile attempt{item.attempts === 1 ? "" : "s"}
            </span>
            {item.rebaseline_flagged ? (
              <Badge tone="warning" icon="triangle-alert">
                Re-baseline flagged
              </Badge>
            ) : null}
          </div>
          {terminal ? (
            // 05 §4: no manual action can un-terminalize this, and the API answers
            // 409. An enabled button would promise a recovery that cannot happen.
            <span className="vn-caption">
              Terminal state — no manual action can change it. Re-baseline the account&rsquo;s quota windows instead.
            </span>
          ) : (
            <Button size="sm" variant="secondary" disabled={busy} onClick={() => onResync(item.reservation_id)}>
              Re-sync
            </Button>
          )}
        </div>

        <span className="vn-caption">
          {item.dispatched_at === null
            ? "Never dispatched"
            : `Dispatched ${new Date(item.dispatched_at * 1000).toLocaleString()}`}
        </span>

        {item.allocations.map((a) => (
          <div
            key={`${item.reservation_id}:${a.window_id}`}
            className="flex flex-wrap items-center gap-3"
            data-testid={`allocation-${item.reservation_id}-${a.window_id}`}
          >
            <span className="vn-mono-sm">{a.window_id}</span>
            <span className="vn-caption">
              estimated {a.estimated_cost} {a.unit}
            </span>
            <span className="vn-caption">
              {/* An unsettled allocation has no actual cost yet. 0 would claim a
                  measured cost of nothing. */}
              {a.actual_cost === null ? "actual unknown — not settled" : `actual ${a.actual_cost} ${a.unit}`}
            </span>
            {a.actual_confidence === null ? null : (
              <Badge tone="info" icon="gauge">
                {a.actual_confidence} confidence
              </Badge>
            )}
            <Badge tone="inactive" mono icon="circle">
              {a.state}
            </Badge>
          </div>
        ))}
      </div>
    </Card>
  );
}

/** A card shell rendering exactly one of loading / error / content. */
function DiagnosticsCard<T>(props: {
  testId: string;
  title: string;
  state: CardState<T>;
  loadingLabel: string;
  errorTitle: string;
  children: (data: T) => ReactNode;
}) {
  const { testId, title, state, loadingLabel, errorTitle, children } = props;
  return (
    <Card data-testid={testId}>
      <div className="flex flex-col gap-3">
        <span className="vn-title-sub">{title}</span>
        {state.error ? (
          <ErrorState variant="inline" code={state.error.code} title={errorTitle} description={state.error.message} />
        ) : state.data === null ? (
          <Spinner label={loadingLabel} />
        ) : (
          children(state.data)
        )}
      </div>
    </Card>
  );
}

/**
 * The Diagnostics surface (P6-UI-008, 07 §5 RouteExplain, 09 §3.9): "why this
 * route?" plus reconciliation.
 *
 * Three commitments.
 *
 * Typed codes are rendered VERBATIM. Exclusion reasons, attempt statuses and
 * reservation states all pass through unchanged, because the operator's whole job
 * here is to correlate what they see with the router's own emitted codes, the audit
 * log, and the docs. A code this console cannot gloss is still shown.
 *
 * The rollup's nulls mean what they mean, exactly as on Overview: no attempt
 * recorded is never success, unknown latency is never 0 ms, no chosen route is
 * never a nameless provider, and an unsettled allocation cost is never 0.
 *
 * A 404 on the detail read renders "no record for this request id" — NOT an empty
 * explanation. An empty candidate table would read as "nothing was considered",
 * which is a different and false claim about a request that may never have existed.
 *
 * Nothing here renders a prompt, a response, or a raw provider error: the payload
 * has no field for any of them (09 §3.9 makes it secret-free by construction) and
 * this surface never reaches for one.
 */
export default function DiagnosticsSurface(props: DiagnosticsSurfaceProps) {
  const { csrfToken, onSessionExpired, deepLinkRequestID } = props;

  const [routes, setRoutes] = useState<CardState<RouteDecisionEntry[]>>(LOADING);
  const [reconciliation, setReconciliation] = useState<CardState<ReconciliationItem[]>>(LOADING);
  const [selected, setSelected] = useState<string | null>(deepLinkRequestID ?? null);
  const [explanation, setExplanation] = useState<CardState<RouteExplanation>>(LOADING);
  const [explanationNotFound, setExplanationNotFound] = useState(false);
  const [actionError, setActionError] = useState<AuthApiError | null>(null);
  const [busy, setBusy] = useState(false);
  const [reloadToken, setReloadToken] = useState(0);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      try {
        const page = await listRouteDecisions({ limit: ROUTE_LIMIT });
        if (!cancelled) setRoutes({ data: page.decisions, error: null });
      } catch (err) {
        if (cancelled) return;
        if (isSessionExpired(err)) {
          onSessionExpired();
          return;
        }
        setRoutes({ data: null, error: toApiError(err) });
      }
    }

    void load();
    return () => {
      cancelled = true;
    };
  }, [onSessionExpired]);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      try {
        const page = await listReconciliation({ limit: ROUTE_LIMIT });
        if (!cancelled) setReconciliation({ data: page.items, error: null });
      } catch (err) {
        if (cancelled) return;
        if (isSessionExpired(err)) {
          onSessionExpired();
          return;
        }
        setReconciliation({ data: null, error: toApiError(err) });
      }
    }

    void load();
    return () => {
      cancelled = true;
    };
  }, [reloadToken, onSessionExpired]);

  // The explanation read, driven by `selected` — which starts at the deep-link
  // request id when Overview sent the operator here for one specific request.
  useEffect(() => {
    if (selected === null) return;
    let cancelled = false;

    async function load(requestID: string) {
      setExplanation(LOADING);
      setExplanationNotFound(false);
      try {
        const x = await getRouteExplanation(requestID);
        if (!cancelled) setExplanation({ data: x, error: null });
      } catch (err) {
        if (cancelled) return;
        if (isSessionExpired(err)) {
          onSessionExpired();
          return;
        }
        const apiError = toApiError(err);
        // A 404 is its OWN state: no record exists. Rendering it as an error, or
        // worse as an empty explanation, both mislead.
        if (apiError.code === "not_found") {
          setExplanationNotFound(true);
          return;
        }
        setExplanation({ data: null, error: apiError });
      }
    }

    void load(selected);
    return () => {
      cancelled = true;
    };
  }, [selected, onSessionExpired]);

  const handleResync = useCallback(
    async (reservationID: string) => {
      setBusy(true);
      setActionError(null);
      try {
        await actOnReconciliation(reservationID, "resync", csrfToken);
        setReloadToken((t) => t + 1);
      } catch (err) {
        if (isSessionExpired(err)) {
          onSessionExpired();
          return;
        }
        setActionError(toApiError(err));
      } finally {
        setBusy(false);
      }
    },
    [csrfToken, onSessionExpired],
  );

  return (
    <div className="flex flex-col gap-4">
      <DiagnosticsCard
        testId="diagnostics-card-routes"
        title="Recent routing decisions"
        state={routes}
        loadingLabel="Loading routing decisions…"
        errorTitle="Could not load routing decisions"
      >
        {(decisions) =>
          decisions.length === 0 ? (
            <EmptyState
              icon="activity"
              title="No requests yet"
              description="Nothing has been routed through this router, so there is no decision to explain."
            />
          ) : (
            <div className="flex flex-col gap-2">
              {decisions.map((d) => (
                <div
                  key={d.decision_id}
                  className="flex flex-wrap items-center justify-between gap-2 border-b border-border-subtle pb-2 last:border-b-0 last:pb-0"
                  data-testid={`route-row-${d.request_id}`}
                >
                  <div className="flex flex-wrap items-center gap-2">
                    <TierChip tier={d.tier} />
                    {d.chosen.provider_model_id === null ? (
                      <Badge tone="warning" icon="circle-slash">
                        No route chosen
                      </Badge>
                    ) : (
                      <span className="vn-mono-sm">
                        {d.chosen.provider_id} · {d.chosen.provider_model_id}
                      </span>
                    )}
                    <RouteOutcomeCells entry={d} />
                    <span className="vn-caption">{new Date(d.created_at).toLocaleString()}</span>
                  </div>
                  <Button
                    size="sm"
                    variant="ghost"
                    data-testid={`route-explain-${d.request_id}`}
                    aria-expanded={selected === d.request_id}
                    onClick={() => setSelected(d.request_id)}
                  >
                    Explain
                  </Button>
                </div>
              ))}
            </div>
          )
        }
      </DiagnosticsCard>

      {selected === null ? null : (
        <Card>
          <div className="flex flex-col gap-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="vn-title-sub">Why this route?</span>
              <Button size="sm" variant="ghost" onClick={() => setSelected(null)}>
                Close
              </Button>
            </div>
            <ExplanationDetail state={explanation} notFound={explanationNotFound} />
          </div>
        </Card>
      )}

      <DiagnosticsCard
        testId="diagnostics-card-reconciliation"
        title="Reconciliation"
        state={reconciliation}
        loadingLabel="Loading reconciliation…"
        errorTitle="Could not load reconciliation"
      >
        {(items) =>
          items.length === 0 ? (
            <EmptyState
              icon="circle-check"
              title="Nothing awaiting reconciliation"
              description="Every reservation has settled or been released — no manual recovery is pending."
            />
          ) : (
            <div className="flex flex-col gap-3">
              {actionError ? (
                <ErrorState
                  variant="inline"
                  code={actionError.code}
                  title="The reconciliation action was refused"
                  description={actionError.message}
                />
              ) : null}
              {items.map((item) => (
                <ReconciliationRow
                  key={item.reservation_id}
                  item={item}
                  busy={busy}
                  onResync={(id) => void handleResync(id)}
                />
              ))}
            </div>
          )
        }
      </DiagnosticsCard>
    </div>
  );
}

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Badge,
  Button,
  Card,
  EmptyState,
  ErrorState,
  IconButton,
  Spinner,
} from "@venom/design-system/primitives";
import {
  CapabilityTruthBadge,
  CertificationStateBadge,
  ContextWindowDisplay,
  ModelIdentity,
  type CapabilityTruth as DSCapabilityTruth,
  type CertState as DSCertState,
} from "@venom/design-system/domain";
import {
  getJob,
  isSessionExpired,
  listModelGroups,
  startBenchmark,
  startDiscovery,
  startProbe,
  toApiError,
  type AuthApiError,
  type EffectiveOffering,
  type ModelGroup,
  type OfferingCapability,
} from "../api/controlClient";
import ReviewQueueBanner from "./ReviewQueueBanner";

export interface ModelsSurfaceProps {
  csrfToken: string;
  onSessionExpired: () => void;
}

/**
 * Fetches every page of GET /models.
 *
 * Following the cursor is not optional politeness. `/models` groups offerings
 * WITHIN a page, so one page is genuinely a window onto the catalog and not the
 * catalog — a surface that rendered page one alone would silently claim the
 * owner has fewer models than they do. The page cap only guards against a
 * pathological cursor loop; an owner console's catalog is small.
 */
async function fetchAllModelGroups(): Promise<ModelGroup[]> {
  const all: ModelGroup[] = [];
  let cursor: string | undefined;
  for (let page = 0; page < 25; page++) {
    const result = await listModelGroups({ cursor, limit: 200 });
    all.push(...result.groups);
    if (!result.nextCursor) break;
    cursor = result.nextCursor;
  }
  return all;
}

/**
 * The routability of one capability, as a fact this surface REPORTS rather than
 * computes.
 *
 * `capability.routable` is the server's answer (intelligence.Project over
 * models.Routable). `state === "certified" && truth === "supported"` is the same
 * conjunction 04 §5 defines, recomputed here for ONE purpose only: to detect
 * disagreement. The word "routable" is shown only when both agree.
 *
 * That is deliberately stricter than trusting either source. If the server said
 * routable for a pair the conjunction rejects, something upstream is wrong, and
 * the honest rendering of "we have two answers and they conflict" is NOT the
 * optimistic one — an operator acting on a false routable waits for traffic that
 * never comes. Fail closed, and say the two disagree so the bug is visible
 * instead of silently resolved in the wrong direction.
 */
function capabilityRoutability(capability: OfferingCapability): {
  routable: boolean;
  inconsistent: boolean;
} {
  const conjunction = capability.state === "certified" && capability.truth === "supported";
  return {
    routable: capability.routable && conjunction,
    inconsistent: capability.routable !== conjunction,
  };
}

/** True when any of this offering's capabilities is not yet CERTIFIED as
 * supported — the certification-review predicate, matching exactly what the
 * review-census banner counts as `capability_not_certified` and what the
 * backlog empty-state promises ("every offering-operation is certified and
 * supported").
 *
 * Deliberately NOT full routability. End-to-end routability additionally
 * requires the capability to be EFFECTIVE (native × provider × transport, 04
 * §3), and this read model hardcodes native/transport as UNKNOWN this phase (no
 * transport registry yet — see internal/httpapi/models.go), so `routable` is
 * always false here. Keying "needs review" off routability therefore flagged
 * EVERY model forever, contradicting the banner's own "nothing is waiting".
 * Certification is the signal a human actually acts on; routability is shown
 * per-capability in the expanded view for what it honestly is. */
function offeringNeedsReview(o: EffectiveOffering): boolean {
  return o.capabilities.some((c) => !(c.state === "certified" && c.truth === "supported"));
}

function groupNeedsReview(g: ModelGroup): boolean {
  return g.offerings.some(offeringNeedsReview);
}

/** The context to show on the collapsed group header: the largest EFFECTIVE
 * context across this model's offerings, plus that offering's provenance. Each
 * offering's effective_context_tokens already falls back to the provider-
 * declared ceiling (04 §3), so this surfaces a real, provider-declared context
 * instead of the canonical native_context_tokens — which stays null until the
 * context probe runs and would otherwise read as "ctx unknown" even when the
 * provider did declare one. Null only when NO offering has any known context. */
function groupContext(g: ModelGroup): { tokens: number | null; provenance?: string } {
  let best: EffectiveOffering | null = null;
  for (const o of g.offerings) {
    if (o.effective_context_tokens == null) continue;
    if (best == null || o.effective_context_tokens > (best.effective_context_tokens ?? 0)) best = o;
  }
  return best
    ? { tokens: best.effective_context_tokens, provenance: best.context_provenance || undefined }
    : { tokens: null };
}

/** The in-flight/finished outcome of an async trigger. `note` carries the
 * honesty caveat a bare status cannot (see benchmarkNote). */
interface JobOutcome {
  label: string;
  note?: string;
  tone: "info" | "healthy" | "warning" | "critical";
}

/** One capability's cell: certification state, capability truth, and the
 * conjunction of the two — all three, always, because a single chip cannot say
 * WHICH half of the conjunction failed. */
function CapabilityCell(props: { offeringKey: string; capability: OfferingCapability }) {
  const { offeringKey, capability } = props;
  const { routable, inconsistent } = capabilityRoutability(capability);

  return (
    <div
      className="flex flex-wrap items-center gap-2"
      data-testid={`capability-${offeringKey}-${capability.operation}`}
    >
      <span className="vn-caption">{capability.operation}</span>
      <CertificationStateBadge state={capability.state as DSCertState} />
      <CapabilityTruthBadge truth={capability.truth as DSCapabilityTruth} />
      <span data-testid={`capability-routable-${offeringKey}-${capability.operation}`}>
        {routable ? (
          <Badge tone="healthy" icon="circle-check">
            Routable
          </Badge>
        ) : (
          <Badge tone="warning" icon="circle-slash">
            Not routable
          </Badge>
        )}
      </span>
      {inconsistent ? (
        <Badge
          tone="warning"
          icon="triangle-alert"
          title="The API's routable flag disagrees with the certification state and capability truth it reported alongside it"
        >
          Inconsistent API answer
        </Badge>
      ) : null}
    </div>
  );
}

/** One offering row: identity, availability, context, quality, capabilities. */
function OfferingRow(props: {
  offering: EffectiveOffering;
  busy: boolean;
  onProbe: (offeringOperationID: string) => void;
}) {
  const { offering: o, busy, onProbe } = props;
  const key = o.provider_model_id;
  const catalogOnly = o.availability === "catalog_only";

  return (
    <Card data-testid={`offering-${key}`}>
      <div className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <ModelIdentity
            name={o.display_name || o.provider_model_id}
            providerModelId={o.provider_model_id}
          />
          <span className="vn-caption">
            {o.provider_id} · {o.account_id}
          </span>
        </div>

        <div className="flex flex-wrap items-center gap-4">
          <span
            data-testid={`offering-availability-${key}`}
            className="flex flex-wrap items-center gap-2"
          >
            {catalogOnly ? (
              // 04 §5: media-only models get a TERMINAL catalog_only state —
              // "visible, never entering the tiers, NOT counted as a failure".
              // The tone is deliberately `info`, never `critical`: this is a
              // correct, settled classification, not something to go fix.
              <>
                <Badge tone="info" icon="book">
                  Catalog only
                </Badge>
                <span className="vn-caption">
                  Never enters a tier — this is a settled classification, not a failure.
                </span>
              </>
            ) : (
              <Badge tone={o.availability === "available" ? "healthy" : "unknown"} icon="box">
                {o.availability}
              </Badge>
            )}
          </span>

          <span data-testid={`offering-context-${key}`} className="flex items-center gap-2">
            <span className="vn-caption">Context</span>
            {/* tokens={null} renders the DS "ctx unknown" badge. An unknown
                context is ineligible for every tier (05 §1 fail-closed) — it is
                never 0 and never a blank cell. */}
            <ContextWindowDisplay
              tokens={o.effective_context_tokens}
              verified={
                o.context_provenance === "probe" || o.context_provenance === "owner_override"
              }
              source={o.context_provenance || undefined}
            />
          </span>

          <span data-testid={`offering-quality-${key}`} className="flex items-center gap-2">
            <span className="vn-caption">Quality</span>
            {o.quality_known ? (
              <Badge tone="info" mono icon="gauge">
                {o.quality_score.toFixed(2)}
              </Badge>
            ) : (
              // quality_known:false means the score carries no information.
              // Rendering quality_score (which is 0 in that case) would invent
              // the worst possible rating out of an absent one.
              <Badge tone="unknown" icon="circle-help">
                Not rated — unknown
              </Badge>
            )}
          </span>
        </div>

        <div className="flex flex-col gap-2">
          {o.capabilities.length === 0 ? (
            <span className="vn-caption">
              No capability has been observed for this offering yet.
            </span>
          ) : (
            o.capabilities.map((c) => {
              // POST /offerings/{id}/probe is keyed by the OFFERING-OPERATION id,
              // which the projection now reports (P6-CAPI-EXTRA-2). It is used
              // VERBATIM: the real ids are minted randomly by DiscoveryRepo, so an
              // id composed from provider_model_id would address a different row
              // or 404.
              //
              // An ABSENT id is not a gap to work around — an operation reachable
              // only through native/transport support has no offering_operations
              // row and therefore nothing to probe. The control stays disabled with
              // the reason stated.
              const probeID = c.offering_operation_id;
              return (
                <div
                  key={c.operation}
                  className="flex flex-wrap items-center justify-between gap-2"
                >
                  <CapabilityCell offeringKey={key} capability={c} />
                  <Button
                    size="sm"
                    variant="secondary"
                    disabled={busy || !probeID}
                    data-testid={`probe-${key}-${c.operation}`}
                    title={
                      probeID
                        ? `Probe this operation's capability truth (${probeID}).`
                        : "This operation has no offering-operation id, so there is nothing to probe."
                    }
                    onClick={probeID ? () => onProbe(probeID) : undefined}
                  >
                    {probeID ? "Probe" : "Probe — not available for this operation"}
                  </Button>
                </div>
              );
            })
          )}
        </div>
      </div>
    </Card>
  );
}

/**
 * The Models surface (P6-UI-002, 04 §3/§5, 07 §5/§5a): the canonical model
 * catalog, its per-account offerings, and the certification conjunction that
 * decides whether any of them can actually be routed to.
 *
 * Three truths this surface refuses to soften:
 *
 *   - Routability is a CONJUNCTION. `certified` alone is not routable; the
 *     capability truth must be `supported` too. See capabilityRoutability.
 *   - An unknown context and an unknown quality rating are rendered as unknown.
 *     Never 0, never blank — 0 is a specific, false claim.
 *   - `catalog_only` is a correct terminal classification, not a fault. It is
 *     shown as "never enters a tier", in an informational tone.
 *
 * And one it refuses to imply: a completed benchmark job is not a new rating.
 * The QualityIndex seam is nil in production, so the job legitimately completes
 * without writing one. See benchmarkOutcome.
 */
export default function ModelsSurface(props: ModelsSurfaceProps) {
  const { csrfToken, onSessionExpired } = props;

  const [groups, setGroups] = useState<ModelGroup[] | null>(null);
  const [loadError, setLoadError] = useState<AuthApiError | null>(null);
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [backlogOnly, setBacklogOnly] = useState(false);
  const [outcome, setOutcome] = useState<JobOutcome | null>(null);
  const [busy, setBusy] = useState(false);
  const [reloadToken, setReloadToken] = useState(0);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      setLoadError(null);
      try {
        const all = await fetchAllModelGroups();
        if (cancelled) return;
        setGroups(all);
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
  }, [reloadToken, onSessionExpired]);

  /** Reports an accepted async trigger HONESTLY: 202 means the job exists, not
   * that the work is done. */
  const runTrigger = useCallback(
    async (
      kind: string,
      trigger: () => Promise<{ job_id: string }>,
      describe: (status: string) => JobOutcome,
    ) => {
      setBusy(true);
      setOutcome({ label: `${kind}: starting…`, tone: "info" });
      try {
        const handle = await trigger();
        // The 202 is an ACCEPTANCE. Report it as in-flight and let the caller's
        // describe() decide what the terminal status is allowed to claim.
        setOutcome(describe("running"));
        const job = await getJob(handle.job_id);
        setOutcome(describe(job.status));
        if (job.status === "completed") setReloadToken((t) => t + 1);
      } catch (err) {
        if (isSessionExpired(err)) {
          onSessionExpired();
          return;
        }
        const apiError = toApiError(err);
        setOutcome({
          label: `${kind} could not start: ${apiError.message}`,
          note:
            apiError.code === "enrichment_disabled"
              ? "Enrichment is disabled — this is a state conflict, not a permission problem. Turn enrichment on in Settings and try again."
              : apiError.code,
          tone: "critical",
        });
      } finally {
        setBusy(false);
      }
    },
    [onSessionExpired],
  );

  const handleDiscover = useCallback(
    (accountId: string) =>
      runTrigger(
        "Discovery",
        () => startDiscovery(accountId, csrfToken),
        (status) => ({
          label: `Discovery job ${status}`,
          note:
            status === "completed"
              ? "The catalog below has been reloaded from the server."
              : "Discovery runs in the background — this is the job's status, not a result.",
          tone: status === "failed" ? "critical" : status === "completed" ? "healthy" : "info",
        }),
      ),
    [csrfToken, runTrigger],
  );

  const handleProbe = useCallback(
    (offeringOperationID: string) =>
      runTrigger(
        "Probe",
        () => startProbe(offeringOperationID, csrfToken),
        (status) => ({
          label: `Probe job ${status}`,
          note: "A probe measures capability truth; an infrastructure failure never flips it.",
          tone: status === "failed" ? "critical" : status === "completed" ? "healthy" : "info",
        }),
      ),
    [csrfToken, runTrigger],
  );

  const handleBenchmark = useCallback(
    (modelId: string) =>
      runTrigger(
        "Benchmark",
        () => startBenchmark(modelId, csrfToken),
        (status) => ({
          label: `Benchmark job ${status}`,
          // THE honesty requirement of this card. A benchmark can reach
          // `completed` having written NO rating: the canonical-quality seam
          // (QualityIndex) is nil in production, so the leaderboard always
          // misses and the handler deliberately completes the job without
          // fabricating a score. "Completed" therefore says nothing about
          // whether a rating changed, and this note says so rather than letting
          // the operator infer the flattering reading.
          note:
            status === "completed"
              ? "Completed with no rating — no canonical quality source is wired yet, so a benchmark cannot produce one. Any rating shown above is unchanged."
              : "Benchmarks run in the background — this is the job's status, not a rating.",
          tone: status === "failed" ? "critical" : "info",
        }),
      ),
    [csrfToken, runTrigger],
  );

  const visibleGroups = useMemo(() => {
    if (!groups) return null;
    return backlogOnly ? groups.filter(groupNeedsReview) : groups;
  }, [groups, backlogOnly]);

  const banner = (
    <ReviewQueueBanner
      onSessionExpired={onSessionExpired}
      onReviewBacklog={() => setBacklogOnly(true)}
    />
  );

  // An error is its OWN state. Falling through to the empty state would tell the
  // owner "you have no models" when the truth is "we could not ask".
  if (loadError) {
    return (
      <div className="flex flex-col gap-4">
        {banner}
        <ErrorState
          code={loadError.code}
          title="Could not load live models"
          description={loadError.message}
          onRetry={() => setReloadToken((t) => t + 1)}
        />
      </div>
    );
  }

  if (visibleGroups === null) {
    return <Spinner label="Loading live models…" />;
  }

  return (
    <div className="flex flex-col gap-4">
      {banner}

      {outcome ? (
        <Card data-testid="job-outcome">
          <div className="flex flex-wrap items-start justify-between gap-2">
            <div className="flex flex-col gap-1">
              <Badge tone={outcome.tone} icon="activity">
                {outcome.label}
              </Badge>
              {outcome.note ? <span className="vn-caption">{outcome.note}</span> : null}
            </div>
            <IconButton
              icon="x"
              label="Dismiss job status"
              variant="ghost"
              onClick={() => setOutcome(null)}
            />
          </div>
        </Card>
      ) : null}

      {backlogOnly ? (
        <div className="flex flex-wrap items-center gap-2">
          <Badge tone="warning" icon="filter">
            Showing the review backlog only
          </Badge>
          <Button size="sm" variant="ghost" onClick={() => setBacklogOnly(false)}>
            Show the whole catalog
          </Button>
        </div>
      ) : null}

      {visibleGroups.length === 0 ? (
        <EmptyState
          icon="box"
          title={backlogOnly ? "No live models need review" : "No live models"}
          description={
            backlogOnly
              ? "Every live offering-operation is certified and supported."
              : "Models appear automatically when a healthy connected provider account is available."
          }
        />
      ) : (
        visibleGroups.map((g) => {
          const isOpen = expanded[g.model_id] ?? false;
          const firstOffering = g.offerings[0];
          const ctx = groupContext(g);
          return (
            <Card key={g.model_id} data-testid={`model-group-${g.model_id}`}>
              <div className="flex flex-col gap-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="flex flex-wrap items-center gap-2">
                    <Button
                      size="sm"
                      variant="ghost"
                      icon={isOpen ? "chevron-down" : "chevron-right"}
                      aria-expanded={isOpen}
                      data-testid={`model-group-toggle-${g.model_id}`}
                      onClick={() => setExpanded((prev) => ({ ...prev, [g.model_id]: !isOpen }))}
                    >
                      {g.display_name || g.model_id}
                    </Button>
                    <span className="vn-caption">
                      {g.offerings.length} offering{g.offerings.length === 1 ? "" : "s"}
                    </span>
                    {groupNeedsReview(g) ? (
                      <Badge tone="warning" icon="triangle-alert">
                        Needs review
                      </Badge>
                    ) : null}
                  </div>
                  <div className="flex flex-wrap items-center gap-2">
                    {firstOffering ? (
                      <Button
                        size="sm"
                        variant="secondary"
                        disabled={busy}
                        onClick={() => void handleDiscover(firstOffering.account_id)}
                      >
                        Discover
                      </Button>
                    ) : null}
                    <Button
                      size="sm"
                      variant="secondary"
                      disabled={busy}
                      onClick={() => void handleBenchmark(g.model_id)}
                    >
                      Benchmark
                    </Button>
                  </div>
                </div>

                <div className="flex flex-wrap items-center gap-4">
                  <span className="flex items-center gap-2">
                    <span className="vn-caption">Context</span>
                    <ContextWindowDisplay
                      tokens={ctx.tokens}
                      verified={ctx.provenance === "probe" || ctx.provenance === "owner_override"}
                      source={ctx.provenance}
                    />
                  </span>
                  <span className="flex items-center gap-2">
                    <span className="vn-caption">Canonical rating</span>
                    {g.quality_rating == null ? (
                      <Badge tone="unknown" icon="circle-help">
                        Not rated — unknown
                      </Badge>
                    ) : (
                      <Badge tone="info" mono icon="gauge">
                        {g.quality_rating.toFixed(2)}
                      </Badge>
                    )}
                  </span>
                </div>

                {isOpen ? (
                  <div
                    className="flex flex-col gap-3"
                    data-testid={`model-offerings-${g.model_id}`}
                  >
                    {g.offerings.length === 0 ? (
                      <span className="vn-caption">
                        This model has no offering on any connected account.
                      </span>
                    ) : (
                      g.offerings.map((o) => (
                        <OfferingRow
                          key={`${o.account_id}:${o.provider_model_id}`}
                          offering={o}
                          busy={busy}
                          onProbe={(id) => void handleProbe(id)}
                        />
                      ))
                    )}
                  </div>
                ) : null}
              </div>
            </Card>
          );
        })
      )}
    </div>
  );
}

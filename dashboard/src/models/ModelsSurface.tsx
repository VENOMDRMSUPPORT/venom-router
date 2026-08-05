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
  type LatestBenchmark,
  type ModelGroup,
  type OfferingCapability,
} from "../api/controlClient";
import ReviewQueueBanner from "./ReviewQueueBanner";
import ProviderLogo from "../fleet/ProviderLogo";
import CapabilityChips from "../fleet/CapabilityChips";

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
 * The negative-badge copy for one capability whose `routable` came back
 * false, chosen from the state/truth pair the server reported ALONGSIDE that
 * flag.
 *
 * `capability.routable` (intelligence.Project over models.Routable) is this
 * surface's single source of truth for routability — it is trusted verbatim,
 * never recomputed or second-guessed against a client-side conjunction. The
 * server's real routable is a THREE-term conjunction (certified ∧ supported ∧
 * EFFECTIVE — 04 §5), and `effective` is hardcoded false for every capability
 * this phase (internal/httpapi/models.go: NativeCapabilities/
 * TransportOperations are nil until the transport-effectiveness registry — a
 * documented future unit — ships). A certified+supported capability
 * therefore ALWAYS comes back not-routable right now, and that is this
 * phase's honest, expected state — not a disagreement between two answers the
 * API gave. The two-term check below exists only to pick the RIGHT honest
 * copy for that state, never to challenge the server's routable verdict.
 */
function notRoutableCopy(capability: OfferingCapability): { label: string; title: string } {
  if (capability.state === "certified" && capability.truth === "supported") {
    return {
      label: "Not yet effective",
      title:
        "Certified and supported; awaiting the transport-effectiveness registry (a future unit) — real routing uses the candidate pool, not this flag.",
    };
  }
  return { label: "Not routable", title: "Not certified as supported yet" };
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

/** The provenance-derived prefix mark on a context-window badge (owner
 * requirement, 2026-08-05c): "≈" when the shown token count came from the
 * provider's own declared cap (`models.ContextProviderCap`, i.e. NOT
 * probe-verified), "✓" when it came from the canonical native fact a context
 * probe wrote back (`models.ContextNative`), and no mark at all when there is
 * no token count to qualify — `ContextWindowDisplay` already renders that case
 * as the word "ctx unknown" on its own, and a ≈/✓ beside it would be a claim
 * about a number that was never shown.
 *
 * This reuses the SAME `tokens`/`provenance` inputs `ContextWindowDisplay`
 * already receives; it never re-implements that component's "200K" token
 * formatting — it only prepends a small marker in front of it. */
function ContextProvenanceMark(props: { tokens: number | null; provenance?: string }) {
  const { tokens, provenance } = props;
  if (tokens == null) return null;

  const title = "≈ declared by provider (not probe-verified) · ✓ verified by a context probe";
  if (provenance === "provider_cap") {
    return (
      <span
        className="vn-caption"
        title={title}
        aria-label="context declared by provider, not probe-verified"
      >
        ≈
      </span>
    );
  }
  if (provenance === "native") {
    return (
      <span className="vn-caption" title={title} aria-label="context verified by context probe">
        ✓
      </span>
    );
  }
  return null;
}

/** The scale `models.quality_rating` is stored on: 0-100 (04 §3, enforced by
 * internal/models.NewCanonicalModel). An offering's `quality_score` is that
 * same rating divided by 100 (internal/models.QualityScore), which is what
 * routing ranks on.
 *
 * Two scales for one fact is a trap — the whole-branch review found the group
 * header printing the raw column ("0.73", when the column held 0.73 by
 * mistake) beside an offering row printing "0.01" for the same model. So this
 * surface commits to ONE scale everywhere: the 0..1 score, two decimals,
 * derived from quality_rating/100 for the group header. The raw 0-100 column
 * value is never rendered. */
const QUALITY_RATING_SCALE = 100;

/** The group header's displayed rating: the canonical 0-100 column expressed on
 * the same 0..1 scale every offering row shows. */
function groupQualityScore(rating: number): number {
  return rating / QUALITY_RATING_SCALE;
}

/** The ISO day (yyyy-mm-dd) of an RFC3339 timestamp, or null when the value is
 * not shaped like one.
 *
 * Deliberately a prefix match rather than `new Date(...).toLocaleDateString()`:
 * the server serializes finished_at in UTC, and a locale/timezone-dependent
 * rendering would show two different days to two owners for the SAME
 * measurement. Null (rather than a guess) is what keeps a malformed value from
 * being displayed as a real date. */
function isoDay(timestamp: string): string | null {
  const match = /^(\d{4}-\d{2}-\d{2})/.exec(timestamp);
  return match ? match[1] : null;
}

/** The provenance tooltip both quality badges carry (spec line ~205: "local
 * benchmark, <date>").
 *
 * Three honest states, never blended:
 *   - no run recorded (or an unparseable timestamp): "Local benchmark" alone —
 *     the source is known, the date is not, and inventing one would be a claim.
 *   - the latest run measured every request: "Local benchmark, <date>" — that
 *     run is what produced the rating being shown.
 *   - the latest run was PARTIAL: the rating on screen is NOT from it. The
 *     local benchmark writes a rating only on a fully successful suite and
 *     leaves the previous rating in place otherwise (see
 *     internal/httpapi/benchmark.go), so the tooltip names the newer run, says
 *     how much of it succeeded, and says the rating predates it. */
function benchmarkProvenanceTitle(latest: LatestBenchmark | null | undefined): string {
  const day = latest ? isoDay(latest.finished_at) : null;
  if (!latest || day === null) return "Local benchmark";
  if (latest.successes < latest.requests) {
    return (
      `Local benchmark — the latest run (${day}) completed only ${latest.successes} of ` +
      `${latest.requests} requests, so it withheld a rating. The rating shown is from an earlier run.`
    );
  }
  return `Local benchmark, ${day}`;
}

/** The in-flight/finished outcome of an async trigger. `note` carries the
 * honesty caveat a bare status cannot (see benchmarkNote). */
interface JobOutcome {
  label: string;
  note?: string;
  tone: "info" | "healthy" | "warning" | "critical";
}

/** One capability's cell: certification state, capability truth, and the
 * server's own routable verdict — trusted verbatim, never recomputed. */
function CapabilityCell(props: { offeringKey: string; capability: OfferingCapability }) {
  const { offeringKey, capability } = props;
  const routable = capability.routable;
  const notRoutable = routable ? null : notRoutableCopy(capability);

  return (
    <div
      className="flex flex-wrap items-center gap-2"
      data-testid={`capability-${offeringKey}-${capability.operation}`}
    >
      {/* Owner requirement (2026-08-05a): capabilities are ALWAYS icon chips
          with tooltips here too, never bare operation words — reuse the ONE
          shared chip renderer rather than a second implementation. */}
      <CapabilityChips capabilities={[capability]} cap={1} />
      <CertificationStateBadge state={capability.state as DSCertState} />
      <CapabilityTruthBadge truth={capability.truth as DSCapabilityTruth} />
      <span data-testid={`capability-routable-${offeringKey}-${capability.operation}`}>
        {routable ? (
          <Badge tone="healthy" icon="circle-check">
            Routable
          </Badge>
        ) : (
          <Badge tone="warning" icon="circle-slash" title={notRoutable?.title}>
            {notRoutable?.label}
          </Badge>
        )}
      </span>
    </div>
  );
}

/** One offering row: identity, availability, context, quality, capabilities. */
function OfferingRow(props: {
  offering: EffectiveOffering;
  /** The group's benchmark provenance line (benchmarkProvenanceTitle). It is
   * passed DOWN rather than recomputed here so this row and its group header
   * can never disagree about when the model was last measured — the run is a
   * per-model fact, not a per-offering one. */
  provenanceTitle: string;
  busy: boolean;
  onProbe: (offeringOperationID: string) => void;
}) {
  const { offering: o, provenanceTitle, busy, onProbe } = props;
  const key = o.provider_model_id;
  const catalogOnly = o.availability === "catalog_only";

  return (
    <Card data-testid={`offering-${key}`}>
      <div className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="vnd-model-name-group">
            <ProviderLogo slug={o.provider_id} name={o.provider_id} size="md" />
            <ModelIdentity
              name={o.display_name || o.provider_model_id}
              providerModelId={o.provider_model_id}
            />
          </div>
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
                never 0 and never a blank cell. `verified` is true ONLY for
                "native" (a context probe wrote this back) — "provider_cap" is
                the provider's own say-so, declared but unverified. */}
            <ContextProvenanceMark
              tokens={o.effective_context_tokens}
              provenance={o.context_provenance}
            />
            <ContextWindowDisplay
              tokens={o.effective_context_tokens}
              verified={o.context_provenance === "native"}
              source={o.context_provenance || undefined}
            />
          </span>

          <span data-testid={`offering-quality-${key}`} className="flex items-center gap-2">
            <span className="vn-caption">Quality</span>
            {o.quality_known ? (
              // Provenance (Plan 3, local-benchmark-rating): the only quality
              // source this build writes is POST /models/{id}/benchmark's real
              // measurement suite (spec D4 — no imported leaderboard numbers).
              // A known score is therefore always a local-benchmark result,
              // and the badge says so rather than leaving the number to imply
              // an external rating it never had, and it carries the run's own
              // DATE (benchmarkProvenanceTitle) rather than an undated claim.
              <Badge tone="info" mono icon="gauge" title={provenanceTitle}>
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
 * And one it refuses to imply: a completed benchmark job is not UNCONDITIONALLY
 * a new rating. The local benchmark (Plan 3 of the local-benchmark-rating
 * design) writes models.quality_rating only when every request in its suite
 * succeeds; a partial failure still completes the job but withholds the
 * rating. GET /jobs/{id}'s result_ref is the single static
 * "/api/control/v1/models" reference either way, so this surface cannot tell
 * the two outcomes apart from the job read alone — see handleBenchmark's
 * completion note, which says so honestly instead of guessing.
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
          // THE honesty requirement of this card. The local benchmark
          // (internal/httpapi/benchmark.go's runBenchmark) writes
          // models.quality_rating ONLY when every request in its fixed suite
          // succeeds; a partial failure still reaches job status `completed`
          // but withholds the rating, leaving any existing one unchanged.
          // GET /jobs/{id}'s result_ref is the single static
          // "/api/control/v1/models" reference (09 §3.12: a reference, never
          // inline content) in BOTH cases — this surface has no way to tell
          // which outcome actually happened from the job read alone, so
          // "completed" must not be narrated as a specific result it cannot
          // verify. The catalog reload triggered on completion (below) shows
          // whatever the real, current rating is either way.
          note:
            status === "completed"
              ? "Completed — the catalog has been reloaded. A rating (Local benchmark) is written only when every request in the run succeeds; a partial failure records the measurement but withholds the rating, so any existing rating stays unchanged."
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
          // ONE provenance line per model, shared by the header badge and
          // every offering row inside this group.
          const provenanceTitle = benchmarkProvenanceTitle(g.latest_benchmark);
          return (
            <Card key={g.model_id} data-testid={`model-group-${g.model_id}`}>
              <div className="flex flex-col gap-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="flex flex-wrap items-center gap-2">
                    {/* Owner requirement (2026-08-05b): every provider's model
                        is its own row/group, never merged — so the group
                        header carries THAT provider's logo + name. This is
                        safe to read off the first offering because the
                        canonical model_id is a provider-scoped hash
                        (models.CanonicalKey(providerID, providerModelID)):
                        every offering inside one group already shares the
                        same provider by construction. */}
                    {firstOffering ? (
                      <div
                        className="vnd-model-name-group"
                        data-testid={`model-group-provider-${g.model_id}`}
                      >
                        <ProviderLogo
                          slug={firstOffering.provider_id}
                          name={firstOffering.provider_id}
                          size="sm"
                        />
                        <span className="vn-caption">{firstOffering.provider_id}</span>
                      </div>
                    ) : null}
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
                  <span
                    className="flex items-center gap-2"
                    data-testid={`model-group-context-${g.model_id}`}
                  >
                    <span className="vn-caption">Context</span>
                    {/* groupContext() picks the single offering with the
                        LARGEST effective context and returns THAT offering's
                        own provenance — not some optimistic blend across the
                        group. So marking ✓ here is honest exactly when the
                        offering behind the shown number is itself
                        native/probe-verified; a larger but merely
                        provider-declared number still marks ≈, even if a
                        smaller native-verified offering also exists in this
                        same group (see the "derives the group header's
                        marker from the source offering" test). */}
                    <ContextProvenanceMark tokens={ctx.tokens} provenance={ctx.provenance} />
                    <ContextWindowDisplay
                      tokens={ctx.tokens}
                      verified={ctx.provenance === "native"}
                      source={ctx.provenance}
                    />
                  </span>
                  <span
                    className="flex items-center gap-2"
                    data-testid={`model-group-rating-${g.model_id}`}
                  >
                    <span className="vn-caption">Canonical rating</span>
                    {g.quality_rating == null ? (
                      <Badge tone="unknown" icon="circle-help">
                        Not rated — unknown
                      </Badge>
                    ) : (
                      // Same provenance note as OfferingRow's quality badge:
                      // the local benchmark is the only source that writes
                      // this field today, so a known value always means one —
                      // and the SAME dated title is used, from the same run.
                      //
                      // The number is derived (groupQualityScore) rather than
                      // printed raw: quality_rating is the 0-100 column and
                      // every offering row below shows rating/100, so
                      // rendering the column here would put two different
                      // numbers for one rating on one card.
                      <Badge tone="info" mono icon="gauge" title={provenanceTitle}>
                        {groupQualityScore(g.quality_rating).toFixed(2)}
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
                          provenanceTitle={provenanceTitle}
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

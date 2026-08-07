import { useEffect, useState } from "react";
import {
  Badge,
  Button,
  Card,
  EmptyState,
  ErrorState,
  Spinner,
} from "@venom/design-system/primitives";
import {
  CapabilityIcon,
  CapabilityTruthBadge,
  CertificationStateBadge,
  ContextWindowDisplay,
  ModelIdentity,
  type CapabilityProvenance as DSCapabilityProvenance,
  type CapabilityTruth as DSCapabilityTruth,
  type CertState as DSCertState,
} from "@venom/design-system/domain";
import {
  isSessionExpired,
  listModelGroups,
  toApiError,
  type AuthApiError,
  type EffectiveOffering,
  type ModelGroup,
  type OfferingCapability,
} from "../api/controlClient";
import { benchmarkProvenanceTitle } from "../fleet/benchmarkProvenance";
import { ContextProvenanceMark } from "../fleet/ContextProvenanceMark";
import ProviderLogo from "../fleet/ProviderLogo";

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
      {/* Owner requirement (2026-08-06, reversing 2026-08-05a): the design
          system's CapabilityIcon renders the icon AND a text label, so
          `vision` and `reasoning` read apart at a glance without a hover.
          `provenance` (fix round 1, restoring a 2026-08-05 requirement that
          the CapabilityChips deletion silently dropped) additionally marks a
          "declared" capability apart from a "probed" one. */}
      <CapabilityIcon
        capability={capability.operation}
        truth={capability.truth as DSCapabilityTruth}
        provenance={capability.provenance as DSCapabilityProvenance}
      />
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
}) {
  const { offering: o, provenanceTitle } = props;
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
                Not rated
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
            o.capabilities.map((c) => (
              <div key={c.operation} className="flex flex-wrap items-center justify-between gap-2">
                <CapabilityCell offeringKey={key} capability={c} />
              </div>
            ))
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
 *   - Routability is the SERVER's verdict, trusted verbatim, never recomputed
 *     client-side. `capability.routable` (intelligence.Project) is a
 *     three-term conjunction (certified ∧ supported ∧ effective), and
 *     `effective` is hardcoded false this phase (no transport-effectiveness
 *     registry yet — a documented future unit). CapabilityCell/
 *     notRoutableCopy render that honestly with a three-state split: green
 *     "Routable" when true; "Not yet effective" when certified+supported but
 *     not yet routable (awaiting the registry); plain "Not routable"
 *     otherwise.
 *   - An unknown context and an unknown quality rating are rendered as unknown.
 *     Never 0, never blank — 0 is a specific, false claim.
 *   - `catalog_only` is a correct terminal classification, not a fault. It is
 *     shown as "never enters a tier", in an informational tone.
 *
 * This surface is DISPLAY ONLY (owner requirement, 2026-08-06): it triggers no
 * discovery, probe, or benchmark job itself. Capability and context facts
 * arrive automatically on discovery; the owner never used the manual controls
 * that used to sit here.
 */
export default function ModelsSurface(props: ModelsSurfaceProps) {
  // csrfToken is kept only to satisfy ModelsSurfaceProps (AppShell.tsx still
  // passes it) — this surface issues no mutating calls, so it goes unused here.
  const { onSessionExpired } = props;

  const [groups, setGroups] = useState<ModelGroup[] | null>(null);
  const [loadError, setLoadError] = useState<AuthApiError | null>(null);
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
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

  // An error is its OWN state. Falling through to the empty state would tell the
  // owner "you have no models" when the truth is "we could not ask".
  if (loadError) {
    return (
      <div className="flex flex-col gap-4">
        <ErrorState
          code={loadError.code}
          title="Could not load live models"
          description={loadError.message}
          onRetry={() => setReloadToken((t) => t + 1)}
        />
      </div>
    );
  }

  if (groups === null) {
    return <Spinner label="Loading live models…" />;
  }

  return (
    <div className="flex flex-col gap-4">
      {groups.length === 0 ? (
        <EmptyState
          icon="box"
          title="No live models"
          description="Models appear automatically when a healthy connected provider account is available."
        />
      ) : (
        groups.map((g) => {
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
                        Not rated
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

import { useEffect, useState } from "react";
import { Badge, Card, ErrorState, KeyValueList, Spinner } from "@venom/design-system/primitives";
import { TierBadge, type Tier as DSTier } from "@venom/design-system/domain";
import {
  getRoutingPolicy,
  isSessionExpired,
  toApiError,
  type AuthApiError,
  type TierPolicy,
  type TierScoreWeights,
} from "../api/controlClient";

export interface RoutingSurfaceProps {
  onSessionExpired: () => void;
}

/** The DS tier vocabulary, so a tier name the router grows later renders as
 * itself rather than being force-cast into a badge tone it has no token for. */
const DS_TIERS = new Set<string>(["lite", "pro", "max"]);

/** Renders an absent value as the word, never as a default.
 *
 * This is the whole discipline of this surface in one function. Every policy
 * value comes from the API response; a field the API omitted is UNKNOWN, and the
 * one thing this component must never do is substitute the number it "knows" from
 * docs/05 §1. A dashboard that displayed the spec while the engine ran something
 * else would be worse than a dashboard that displayed nothing. */
function known<T>(value: T | null | undefined, render: (v: T) => string): string {
  return value === null || value === undefined ? "unknown" : render(value);
}

/** The scoring-weight rows for a scored tier. */
function WeightRows(props: { tier: string; weights: TierScoreWeights }) {
  const { tier, weights } = props;
  const rows: [keyof TierScoreWeights, string][] = [
    ["quality", "Quality"],
    ["reliability", "Reliability"],
    ["quota_headroom", "Quota headroom"],
    ["evidence_confidence", "Evidence confidence"],
    ["cost_class", "Cost class"],
    ["latency", "Latency"],
  ];

  return (
    <div className="flex flex-col gap-1" data-testid={`tier-weights-${tier}`}>
      {rows.map(([key, label]) => (
        <div
          key={key}
          className="flex flex-wrap items-center justify-between gap-2"
          data-testid={`tier-weight-${tier}-${key}`}
        >
          <span className="vn-caption">{label}</span>
          {/* A 0 HERE is a real declared zero — 05 §2's table has explicit "—"
              cells for Pro's evidence-confidence and Max's latency, which
              routing.ScoreWeights carries as a literal 0. That is a scoring
              claim the tier genuinely makes, unlike an unscored tier's zeros
              (handled by the `scored` branch below). */}
          <Badge tone="info" mono icon="gauge">
            {String(weights[key])}
          </Badge>
        </div>
      ))}
    </div>
  );
}

/**
 * How this tier behaves when its first choice is exhausted (05 §1's "fallback on
 * exhaustion" row).
 *
 * The wording is DERIVED FROM the served `funding` rule rather than looked up per
 * tier name: `free_only` is precisely what makes Lite's fallback fail closed
 * instead of reaching for a paid offering. Keying this off the tier name would
 * quietly keep saying "fail closed" for Lite even if the served policy changed.
 */
function fallbackDescription(funding: string | undefined): string {
  if (funding === undefined) return "unknown — the API reported no funding rule";
  if (funding === "free_only") {
    return "Fails closed within the free pool — a paid offering never enters this tier, even under exhaustion.";
  }
  if (funding === "free_and_paid") {
    return "Falls back within the free + paid pool.";
  }
  // An unrecognized funding rule is reported verbatim rather than guessed at.
  return `Unrecognized funding rule "${funding}" — this console cannot describe its fallback pool.`;
}

/** One tier's policy card. */
function TierPolicyCard(props: { policy: TierPolicy }) {
  const { policy: p } = props;
  const tier = p.tier;

  return (
    <Card data-testid={`tier-policy-${tier}`}>
      <div className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center gap-2">
          {/* TierBadge is the SINGLE way a tier is labelled anywhere, and its
              colour comes from the `tier.*` tokens via `vn-badge--tier-*`. No
              colour value is written here — see 07 §8's no-raw-values rule. */}
          {DS_TIERS.has(tier) ? (
            <TierBadge tier={tier as DSTier} showId />
          ) : (
            <Badge tone="unknown" icon="circle-help">
              Unrecognized tier · {tier}
            </Badge>
          )}
          {p.scored === undefined ? null : (
            <Badge tone={p.scored ? "info" : "inactive"} icon={p.scored ? "gauge" : "minus"}>
              {p.scored ? "Scored" : "Not scored — pure hard-eligibility"}
            </Badge>
          )}
        </div>

        <KeyValueList
          items={[
            { key: "Backend funding", value: known(p.funding, (v) => v), mono: true },
            {
              key: "Context ceiling",
              value: known(p.context_ceiling_tokens, (v) => `${v.toLocaleString()} tokens`),
              mono: true,
            },
            { key: "Thinking ceiling", value: known(p.thinking_ceiling, (v) => v), mono: true },
            {
              key: "Attempt budget (fallback depth)",
              value: known(p.attempt_budget, (v) => String(v)),
              mono: true,
            },
            {
              key: "Latency",
              value: known(p.latency_tie_break_only, (v) => (v ? "tie-break only" : "scored factor")),
            },
          ]}
        />

        <div className="flex flex-col gap-1" data-testid={`tier-fallback-${tier}`}>
          <span className="vn-caption">Fallback on exhaustion</span>
          <span className="vn-body-sm">{fallbackDescription(p.funding)}</span>
        </div>

        <div className="flex flex-col gap-2" data-testid={`tier-scoring-${tier}`}>
          <span className="vn-caption">Scoring</span>
          {p.scored === false || p.weights === null || p.weights === undefined ? (
            // An unscored tier's weights and band are NOT APPLICABLE, not zero.
            // routing.TierPolicy's validator requires them to be zero exactly
            // because they carry no meaning here, so rendering those zeros would
            // manufacture two scoring claims the tier never makes.
            <div className="flex flex-wrap items-center gap-2">
              <Badge tone="inactive" icon="minus">
                {p.scored === false ? "Not scored" : "Scoring not reported"}
              </Badge>
              <span className="vn-caption">
                Weights and competitive band are not applicable to a tier that does not score.
              </span>
            </div>
          ) : (
            <>
              <WeightRows tier={tier} weights={p.weights} />
              <div className="flex flex-wrap items-center justify-between gap-2" data-testid={`tier-band-${tier}`}>
                <span className="vn-caption">Competitive band</span>
                <Badge tone="info" mono icon="git-compare">
                  {known(p.competitive_band, (v) => String(v))}
                </Badge>
              </div>
            </>
          )}
        </div>
      </div>
    </Card>
  );
}

/**
 * The Routing surface (P6-UI-003, 05 §1/§8.4, 07 §5): the three tier policies as
 * the ENGINE reports them.
 *
 * Not one policy value is written in this file. Every number, funding rule,
 * thinking ceiling, weight and band width is read from GET /routing/policy, which
 * serves routing.Policies() — the same validated table the tier engine routes
 * with. A component carrying its own copy of docs/05 §1 would keep displaying the
 * spec long after the engine diverged from it, which is strictly worse than
 * displaying nothing: it would look authoritative while being wrong. A field the
 * API omits therefore renders as "unknown", never as the value this file could
 * have guessed.
 *
 * It is READ-ONLY. 05 §8.4 defers owner weight tuning past V1 and there is no PUT
 * server-side, so there is no input, no select, and no save control here — an
 * editing affordance would promise something nothing can deliver.
 */
export default function RoutingSurface(props: RoutingSurfaceProps) {
  const { onSessionExpired } = props;

  const [tiers, setTiers] = useState<TierPolicy[] | null>(null);
  const [loadError, setLoadError] = useState<AuthApiError | null>(null);
  const [reloadToken, setReloadToken] = useState(0);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      setLoadError(null);
      try {
        const result = await getRoutingPolicy();
        if (cancelled) return;
        setTiers(result);
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

  if (loadError) {
    // Deliberately not an empty tier list, which would read as "the router has
    // no tiers" rather than "we could not read its policy".
    return (
      <ErrorState
        code={loadError.code}
        title="Could not load the routing policy"
        description={loadError.message}
        onRetry={() => setReloadToken((t) => t + 1)}
      />
    );
  }

  if (tiers === null) {
    return <Spinner label="Loading the routing policy…" />;
  }

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <div className="flex flex-col gap-1">
          <span className="vn-title-sub">Tier policy</span>
          <span className="vn-caption">
            Read live from the routing engine&rsquo;s own validated policy table. Scoring weights are
            fixed in V1 and read-only here — owner tuning is deferred past V1 (docs/05 §8.4), and no
            endpoint accepts a change.
          </span>
        </div>
      </Card>

      {/* The cards come from the RESPONSE, not from a fixed three-tier layout: a
          hardcoded set of cards would render an empty one for a tier the API did
          not report. */}
      {tiers.map((policy) => (
        <TierPolicyCard key={policy.tier} policy={policy} />
      ))}
    </div>
  );
}

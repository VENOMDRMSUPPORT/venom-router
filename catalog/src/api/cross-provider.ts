/**
 * The same model, offered by more than one provider.
 *
 * Fifteen of the catalog's canonical models are served by two or three
 * providers. Their quality score is identical by construction — it belongs to
 * the model, not the seller — so the whole difference between the offers is
 * operational: speed, headroom, and what it costs here. Kimi K3 is 93.9% at one
 * provider and 91.0% at another; MiMo V2.5 Pro spans 85.9% to 74.6%. Every
 * number needed to see that was already in the API and no screen put two offers
 * of one model side by side.
 *
 * Pure derivation over what the API already returned: no fetching, no fact of
 * its own, nothing rounded or renamed. A group states what it found and, where
 * the offers were not graded the same way, refuses to imply they are comparable.
 */
import type { ApiModel } from './client';

export interface CrossProviderOffer {
  providerId: string;
  model: ApiModel;
}

export interface CrossProviderGroup {
  /** The settled upstream identity every offer in this group resolved to. */
  canonicalId: string;
  displayName: string;
  /** Best overall score first; offers with no score last. */
  offers: CrossProviderOffer[];
  /** The top SCORED offer, or null when nothing here has a score. */
  best: CrossProviderOffer | null;
  /**
   * Points between the best and worst scored offer.
   *
   * `null` when fewer than two offers carry a score: one number cannot span
   * anything, and a zero there would read as "these providers are equivalent".
   */
  spread: number | null;
  /**
   * Whether the scored offers took the same exam.
   *
   * False means at least one was graded on a dimension another was not, so the
   * scores answer slightly different questions and the difference between them
   * is not purely the provider. This is not a data error — a dimension that does
   * not apply is correctly excluded — but comparing across it silently is.
   */
  comparable: boolean;
  /** The dimensions that some scored offers were graded on and others were not. */
  gradedOnDifferentDimensions: string[];
}

const scoreOf = (offer: CrossProviderOffer): number | null => offer.model.overallScore.value;

/**
 * The name to title a group with.
 *
 * A provider that publishes no name for a row leaves `displayName` equal to the
 * model id, and taking the title from the best-scoring offer meant two of
 * fifteen live groups were headed `cline-pass/qwen3.8-max` and
 * `cline-pass/glm-5.3` while a sibling offer published "Qwen3.8 Max". Prefer an
 * offer that actually named the model, best-scored first; fall back to the id
 * only when nobody did, because a group with no heading at all is worse than one
 * headed by an id.
 */
function groupName(offers: CrossProviderOffer[], scored: CrossProviderOffer[]): string {
  const named = [...scored, ...offers].find((o) => o.model.displayName !== o.model.modelId);
  return (named ?? scored[0] ?? offers[0]).model.displayName;
}

/**
 * Group live offers by the upstream model they resolved to.
 *
 * Rows with no settled identity are dropped rather than pooled: `canonicalId` is
 * null precisely because the identity rules refused to guess, and grouping on
 * that null would merge every unidentified row in the catalog into one fictional
 * model — the guess itself, arrived at from the other direction.
 */
export function groupByCanonicalModel(models: ApiModel[]): CrossProviderGroup[] {
  const byCanonical = new Map<string, CrossProviderOffer[]>();
  for (const model of models) {
    if (!model.canonicalId) continue;
    const offers = byCanonical.get(model.canonicalId) ?? [];
    offers.push({ providerId: model.providerId, model });
    byCanonical.set(model.canonicalId, offers);
  }

  const groups: CrossProviderGroup[] = [];
  for (const [canonicalId, unordered] of byCanonical) {
    // One provider is not a comparison. Those models are already fully served by
    // the provider page; repeating them here would bury the fifteen that matter.
    if (unordered.length < 2) continue;

    const offers = [...unordered].sort((a, b) => {
      const sa = scoreOf(a);
      const sb = scoreOf(b);
      if (sa !== null && sb !== null) return sb - sa || a.providerId.localeCompare(b.providerId);
      if (sa !== null) return -1;
      if (sb !== null) return 1;
      return a.providerId.localeCompare(b.providerId);
    });

    const scored = offers.filter((o) => scoreOf(o) !== null);
    const values = scored.map((o) => scoreOf(o) as number);
    const spread = values.length >= 2 ? Math.max(...values) - Math.min(...values) : null;

    // Asked of the scored offers only: "were these graded the same way" has no
    // meaning for a row that carries no grade.
    const dimensionSets = scored.map((o) => new Set(o.model.overallScore.includedDimensions));
    const everyDimension = new Set(dimensionSets.flatMap((set) => [...set]));
    const gradedOnDifferentDimensions = [...everyDimension]
      .filter((dimension) => !dimensionSets.every((set) => set.has(dimension)))
      .sort();

    groups.push({
      canonicalId,
      displayName: groupName(offers, scored),
      offers,
      best: scored[0] ?? null,
      spread,
      comparable: gradedOnDifferentDimensions.length === 0,
      gradedOnDifferentDimensions,
    });
  }

  // Widest spread first: the gap IS the finding, and a group whose providers
  // land within a point of each other is not a decision anybody needs to make.
  // Groups with nothing to span sort last rather than being hidden — an offer
  // pair nobody has scored yet is still a fact about the catalog.
  return groups.sort((a, b) => {
    if (a.spread !== null && b.spread !== null) return b.spread - a.spread || a.canonicalId.localeCompare(b.canonicalId);
    if (a.spread !== null) return -1;
    if (b.spread !== null) return 1;
    return a.canonicalId.localeCompare(b.canonicalId);
  });
}

/**
 * Canonical model identity resolution.
 *
 * A wrong score is worse than no score, because on the page the two are
 * indistinguishable. Everything here is therefore deterministic: a rule either
 * binds an identity on evidence, or it declines. There is no similarity
 * threshold, no edit distance, and no "closest match" — those fail silently,
 * which is the one failure mode this module exists to prevent.
 *
 * Measured during design (2026-08-12), a prefix-similarity matcher bound
 * `gpt-oss:20b` to `gpt-oss-120b` (score 24.1 instead of 15.2) and
 * `qwen3.5:397b` to `qwen3.5-9b` (21.8 instead of 34.3) — with the correct
 * target present in the same list. Hence rule R3 below.
 */

/** Which rule bound an identity. Stored on every score row for auditability. */
export type IdentityRule = 'exact' | 'free-variant' | 'exact-size' | 'overlay';

export interface UpstreamModel {
  /** Upstream id, e.g. "openai/gpt-oss-20b" */
  id: string;
  /** Upstream's own alternate id, when it publishes one. */
  canonicalSlug?: string;
}

export type Resolution =
  | { status: 'resolved'; rule: IdentityRule; target: string; candidates: string[] }
  /** More than one distinct identity matched. Never scored; goes to human review. */
  | { status: 'ambiguous'; candidates: string[] }
  | { status: 'unresolved'; candidates: [] };

/**
 * Plan/serving variants an upstream appends to the SAME weights.
 *
 * Verified on 2026-08-12: OpenRouter publishes identical benchmark values for a
 * model and its `:free` twin — gpt-oss-20b 15.2/15.2, gemma-4-31b-it 29.7/29.7,
 * nemotron-3-ultra-550b-a55b 38.3/38.3. So these carry no identity.
 */
const PLAN_VARIANT = /:(free|beta|extended|batch|thinking|online|nitro|floor)$/;

/** Ollama marks its hosted models with a cloud suffix; the weights are unchanged. */
const CLOUD_SUFFIX = /(:cloud|-cloud)$/;

/**
 * Normalise an id for comparison.
 *
 * Removes only what is provably not identity: the vendor prefix, plan variants
 * and the cloud suffix. Separators (`. - _ :`) collapse to one class because
 * upstreams disagree on them for the same model (`claude-opus-4-8` vs
 * `claude-opus-4.8`).
 *
 * Deliberately NOT removed: size tokens (`20b`, `397b`) and version tokens.
 * Those are the identity. Collapsing them is exactly how a 20B model inherits a
 * 120B model's score.
 */
export function normalizeId(raw: string): string {
  return raw
    .toLowerCase()
    .replace(/^[^/]+\//, '')
    .replace(PLAN_VARIANT, '')
    .replace(CLOUD_SUFFIX, '')
    .replace(/[._\-:]/g, '-');
}

/** A model id of the form `stem:SIZE` or `stem-SIZE`, e.g. `gpt-oss:20b`. */
const SIZED = /^(.+?)[:\-](\d+(?:\.\d+)?[bm])$/;
const FREE_SUFFIX = /^(.*)-free$/;

export interface IdentityIndex {
  byKey: Map<string, UpstreamModel[]>;
  all: UpstreamModel[];
}

export function buildIndex(models: UpstreamModel[]): IdentityIndex {
  const byKey = new Map<string, UpstreamModel[]>();
  for (const m of models) {
    for (const raw of [m.id, m.canonicalSlug].filter((x): x is string => Boolean(x))) {
      const k = normalizeId(raw);
      const bucket = byKey.get(k) ?? [];
      if (!bucket.some((x) => x.id === m.id)) bucket.push(m);
      byKey.set(k, bucket);
    }
  }
  return { byKey, all: models };
}

/**
 * Collapse entries that are the same model wearing different plan variants.
 * `gpt-oss-20b` and `gpt-oss-20b:free` are one identity, not an ambiguity.
 */
function distinctIdentities(hits: UpstreamModel[]): UpstreamModel[] {
  const seen = new Map<string, UpstreamModel>();
  for (const h of hits) if (!seen.has(normalizeId(h.id))) seen.set(normalizeId(h.id), h);
  return [...seen.values()];
}

function decide(rule: IdentityRule, hits: UpstreamModel[]): Resolution | null {
  const distinct = distinctIdentities(hits);
  if (distinct.length === 0) return null;
  if (distinct.length > 1) return { status: 'ambiguous', candidates: distinct.map((d) => d.id) };
  return { status: 'resolved', rule, target: distinct[0].id, candidates: [distinct[0].id] };
}

/**
 * Resolve one catalog model id against an upstream index.
 *
 * Rules are tried in order and the first that matches anything decides —
 * including deciding "ambiguous", which is a decision, not a fallthrough.
 *
 * @param overlay human-reviewed mappings, keyed by the catalog id. Checked
 *   first: a reviewed decision outranks every inferred rule.
 */
export function resolveIdentity(
  rawId: string,
  index: IdentityIndex,
  overlay: Record<string, string> = {},
): Resolution {
  const id = rawId.replace(/^[^/]+\//, '');

  // R4 — a human already answered this one.
  const pinned = overlay[rawId] ?? overlay[id];
  if (pinned) {
    const hit = index.all.find((m) => m.id === pinned);
    return hit
      ? { status: 'resolved', rule: 'overlay', target: hit.id, candidates: [hit.id] }
      : { status: 'unresolved', candidates: [] };
  }

  // R1 — exact, after normalisation.
  const exact = decide('exact', index.byKey.get(normalizeId(id)) ?? []);
  if (exact) return exact;

  // R2 — a trailing `-free` is a plan marker; resolve the base instead.
  const free = FREE_SUFFIX.exec(id);
  if (free) {
    const r = decide('free-variant', index.byKey.get(normalizeId(free[1])) ?? []);
    if (r) return r;
  }

  // R3 — `stem:SIZE` may only bind to a candidate whose OWN size token is
  // exactly SIZE. This is the rule that refuses 20b -> 120b.
  const sized = SIZED.exec(id);
  if (sized) {
    const [, stem, size] = sized;
    const stemKey = normalizeId(stem).replace(/-/g, '');
    const sizeToken = new RegExp(`(^|-)${size.replace('.', '\\.')}(-|$)`);
    const hits = index.all.filter((m) => {
      const k = normalizeId(m.id);
      return sizeToken.test(k) && k.replace(/-/g, '').startsWith(stemKey);
    });
    const r = decide('exact-size', hits);
    if (r) return r;
  }

  return { status: 'unresolved', candidates: [] };
}

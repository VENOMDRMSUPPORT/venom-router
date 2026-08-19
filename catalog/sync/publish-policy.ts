/**
 * Publish policy (failure layer none — it is a projection decision, not a fetch).
 *
 * Some providers publish a roster wider than what the catalog should present.
 * OpenCode Zen is the owner-declared free tier, but its live roster mixes free
 * and paid ids and models.dev prices most of them. Publishing a paid model under
 * a "free" provider would be a price from one seller wearing another's label —
 * the same class of error the cost split exists to prevent.
 *
 * A free tier publishes only what it currently SERVES for free: a model must be
 * both `active` (present in the latest roster) and proven free. A paid model is
 * withheld (`paid`), a model whose price cannot be shown to be zero is withheld
 * (`not_proven_free`), and a model the provider has dropped from its roster —
 * status `missing` — is withheld too (`not_served`), because a free tier must not
 * advertise a model nobody can call. The published roster is therefore exactly
 * the currently-served free set, deterministic and independent of retirement
 * timing.
 *
 * This runs as a pipeline step AFTER the roster is stored and BEFORE enrich/score,
 * so every downstream reader (enrich, scoring, the read model) — all of which
 * already gate on `status IN ('active','missing')` — excludes the withheld rows
 * by construction, while the rows themselves survive (history is never deleted).
 *
 * Why it is NOT done by dropping ids from the roster: a dropped id would look, to
 * the engine, like the provider withdrew the model — it would age through
 * missing → retired, trip the delta gate on a mass exclusion, and be recorded
 * with the wrong reason ("absent upstream"). Exclusion is a publish decision on a
 * model the provider still serves, so it is a distinct status with its own,
 * honest reason.
 *
 * Idempotency is keyed on `exclusion_reason`, not on `status`: the engine resets
 * `status` to 'active' for every rostered model on every run, but never touches
 * `exclusion_reason`. So a paid model already carrying reason 'paid' is silently
 * re-excluded with no event, and only a genuine change (newly paid, or newly
 * proven free) writes an event.
 */

import type { Db } from '../db/index.ts';
import { transaction } from '../db/index.ts';
import type { ProviderAdapter, ModelSpec, SpecLookup } from './engine.ts';

export type ExclusionReason = 'paid' | 'not_proven_free' | 'not_served' | 'plan_required';

export interface PublishPolicyDeps {
  db: Db;
  adapters: ProviderAdapter[];
  lookupSpec: SpecLookup;
  now: () => string;
}

export interface PublishPolicySummary {
  /** Newly excluded this run, by reason. */
  excluded: Record<ExclusionReason, number>;
  /** Rows restored to the published roster because they are now proven free AND served. */
  restored: number;
}

/** A withheld model always carries a reason; a published one carries none. */
const EMPTY_EXCLUDED = (): Record<ExclusionReason, number> => ({
  paid: 0,
  not_proven_free: 0,
  not_served: 0,
  plan_required: 0,
});

/**
 * Whether a spec proves the model is free at this provider.
 *
 * Proven free means BOTH per-token figures are published AND both are zero.
 * Anything else is not published: a positive figure is `paid`, and a missing or
 * partial figure is `not_proven_free` — the conservative default the owner chose,
 * so a model whose price cannot be shown to be zero is never presented as free.
 */
function classify(spec: ModelSpec | null): { free: boolean; reason: ExclusionReason | null } {
  if (!spec) return { free: false, reason: 'not_proven_free' };
  const { costInPerM: ci, costOutPerM: co } = spec;
  if (ci === 0 && co === 0) return { free: true, reason: null };
  if ((typeof ci === 'number' && ci > 0) || (typeof co === 'number' && co > 0)) return { free: false, reason: 'paid' };
  return { free: false, reason: 'not_proven_free' };
}

/** Apply every free-only provider's publish policy to the rows already stored. */
export function applyPublishPolicy(deps: PublishPolicyDeps): PublishPolicySummary {
  const { db, adapters, lookupSpec, now } = deps;
  const summary: PublishPolicySummary = { excluded: EMPTY_EXCLUDED(), restored: 0 };
  const governed = adapters.filter((a) => a.publishPolicy === 'free_only'
    || Object.keys(a.publishExclusions ?? {}).length > 0);
  if (governed.length === 0) return summary;

  transaction(db, () => {
    const at = now();
    const rows = db.prepare(
      // 'excluded' is included so a model that becomes free can be restored.
      `SELECT model_id, status, exclusion_reason FROM models
       WHERE provider_id = ? AND status IN ('active','missing','excluded')`,
    );
    const exclude = db.prepare(
      `UPDATE models SET status = 'excluded', exclusion_reason = ? WHERE provider_id = ? AND model_id = ?`,
    );
    const restore = db.prepare(
      `UPDATE models SET status = 'active', exclusion_reason = NULL WHERE provider_id = ? AND model_id = ?`,
    );
    const event = db.prepare(
      'INSERT INTO model_events (provider_id, model_id, kind, field, old_value, new_value, reason, at) VALUES (?,?,?,?,?,?,?,?)',
    );

    for (const adapter of governed) {
      const explicit = adapter.publishExclusions ?? {};
      const ownedReasons = new Set<ExclusionReason>(Object.values(explicit));
      if (adapter.publishPolicy === 'free_only') {
        ownedReasons.add('paid');
        ownedReasons.add('not_proven_free');
        ownedReasons.add('not_served');
      }
      const stored = rows.all(adapter.id) as unknown as {
        model_id: string; status: string; exclusion_reason: string | null;
      }[];
      for (const m of stored) {
        // A model absent from the latest roster (status 'missing') is not served,
        // so a free tier withholds it whatever its price. Otherwise the price
        // decides: free publishes, paid / unproven is withheld.
        const reason: ExclusionReason | null = explicit[m.model_id]
          ?? (adapter.publishPolicy === 'free_only'
            ? (m.status === 'missing' ? 'not_served' : classify(lookupSpec(adapter.feedKey, m.model_id)).reason)
            : null);
        if (reason) {
          // Only a change of the policy verdict writes an event; a re-assert with
          // the same reason is silent (the engine flipped status back to active,
          // but the reason — the real marker — is unchanged).
          if (m.exclusion_reason !== reason) {
            exclude.run(reason, adapter.id, m.model_id);
            event.run(adapter.id, m.model_id, 'excluded', 'status', m.status, 'excluded', reason, at);
            summary.excluded[reason]++;
          } else if (m.status !== 'excluded') {
            exclude.run(reason, adapter.id, m.model_id);
          }
        } else if (m.exclusion_reason !== null && ownedReasons.has(m.exclusion_reason as ExclusionReason)) {
          // Was withheld, now proven free AND served — bring it back to the roster.
          restore.run(adapter.id, m.model_id);
          event.run(adapter.id, m.model_id, 'readded', 'status', 'excluded', 'active', 'now proven free and served', at);
          summary.restored++;
        }
      }
    }
  });

  return summary;
}

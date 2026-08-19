/**
 * Ranking and comparison contract.
 *
 * Uncertainty that lives only in a metadata field is decoration. This module is
 * where it changes what the reader sees: what sorts above what, what is
 * declared a tie, and what a filter is allowed to include.
 *
 * Three rules, in force everywhere a list is ordered:
 *
 *  1. A quality ranking uses quality evidence only. VO can never lift a model
 *     in a VQ ordering, so "cheap and long-context" cannot masquerade as
 *     "good".
 *  2. A model with no quality evidence is not last — it is *unplaced*. It sorts
 *     into a separate section, never interleaved as though its quality were
 *     known to be low.
 *  3. Two values whose uncertainty intervals overlap are tied. A 3-point gap
 *     between two +/-5.7 estimates is not a ranking, and rendering it as one
 *     invents precision the evidence does not carry.
 */

import type { VQ, VO } from './venom-score.ts';
import { comparable } from './venom-score.ts';

export interface ScoredModel {
  providerId: string;
  modelId: string;
  vq: VQ;
  vo: VO;
}

/** A group of models the evidence cannot separate. Rendered as one rank. */
export interface RankGroup {
  rank: number;
  members: ScoredModel[];
  /** True when members differ numerically but their intervals overlap. */
  tiedByUncertainty: boolean;
}

export interface VQRanking {
  /** Ranked groups, best first. Only models carrying quality evidence appear. */
  ranked: RankGroup[];
  /**
   * Models with no quality evidence. Deliberately a separate list rather than a
   * tail of `ranked`: appending them would imply they were measured and found
   * worst.
   */
  unplaced: ScoredModel[];
}

/**
 * Rank by quality.
 *
 * Models are walked best-first and each is compared against the open group. If
 * the evidence cannot separate it from that group, it joins the group instead
 * of taking its own rank.
 */
export function rankByVQ(models: ScoredModel[]): VQRanking {
  const rated = models.filter((m) => m.vq.value !== null);
  const unplaced = models.filter((m) => m.vq.value === null);

  const sorted = [...rated].sort((a, b) => b.vq.value! - a.vq.value!);
  const ranked: RankGroup[] = [];

  for (const model of sorted) {
    const open = ranked[ranked.length - 1];
    // `comparable` is false when the intervals overlap, which is exactly the
    // condition for "the evidence does not separate these".
    if (open && !comparable(open.members[0].vq, model.vq)) {
      open.members.push(model);
      open.tiedByUncertainty = open.members.some((m) => m.vq.value !== model.vq.value);
      continue;
    }
    ranked.push({ rank: ranked.length + 1, members: [model], tiedByUncertainty: false });
  }

  return { ranked, unplaced };
}

/**
 * Does this ordering claim more than the evidence supports?
 *
 * Used as a guard in tests and by the API before it serves an ordered list: if
 * two adjacent entries are not comparable, the list must present them as tied.
 */
export function orderingIsHonest(order: ScoredModel[]): boolean {
  for (let i = 0; i + 1 < order.length; i++) {
    if (order[i].vq.value === null && order[i + 1].vq.value !== null) return false;
  }
  return true;
}

export type SortKey = 'vq' | 'vo';

/**
 * The contract a client must follow to sort a list.
 *
 * Returned rather than applied, so the API can hand the same policy to the SPA
 * and both stay in step. This legacy base-score contract never blends VQ and
 * VO; the composite ordering is declared separately in `model-score.ts`.
 */
export interface SortContract {
  key: SortKey;
  /** Field the client may order on. */
  field: 'vq.value' | 'vo.value';
  /** Rows with a null value here are shown in a separate, labelled section. */
  unplacedLabel: string;
  /** Adjacent rows within this distance must render as tied. */
  tieRule: string;
}

export function sortContract(key: SortKey): SortContract {
  return key === 'vq'
    ? {
        key,
        field: 'vq.value',
        unplacedLabel: 'No quality evidence',
        tieRule: 'tied when |a - b| <= uncertainty(a) + uncertainty(b)',
      }
    : {
        key,
        field: 'vo.value',
        unplacedLabel: 'No operational data',
        // VO is computed from published facts, so two VO values differ only if
        // the facts differ. There is no measurement error to absorb.
        tieRule: 'exact equality',
      };
}

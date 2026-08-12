/**
 * OpenRouter — the benchmark source.
 *
 * Republishes Artificial Analysis indices and design_arena Elo without
 * authentication. AA's own API returns 401, so this is the only open route to
 * those figures; the values are AA's, and are attributed as such.
 *
 * Only the `models/*` sub-arena of design_arena is read. Measured 2026-08-12:
 * `models/*` tracks the target index at rho 0.922 while `agents/*` manages
 * 0.66, and mixing them dragged the combined fit to 0.876. Averaging noise into
 * signal is not neutrality.
 */

import type { FetchJson } from '../http.ts';
import { buildIndex, type IdentityIndex, type UpstreamModel } from '../identity.ts';

export const OPENROUTER_URL = 'https://openrouter.ai/api/v1/models';

export interface BenchmarkRecord extends UpstreamModel {
  /** Artificial Analysis, when published for this model. */
  intelligence?: number;
  coding?: number;
  agentic?: number;
  /** Mean Elo over the design_arena `models/*` categories. */
  designElo?: number;
  vendor: string;
  contextLength?: number;
  costOutPerM?: number;
}

interface RawModel {
  id: string;
  canonical_slug?: string;
  context_length?: number;
  pricing?: { completion?: string | number };
  benchmarks?: {
    artificial_analysis?: { intelligence_index?: number; coding_index?: number; agentic_index?: number };
    design_arena?: { arena?: string; category?: string; elo?: number }[];
  };
}

export interface BenchmarkSource {
  index: IdentityIndex;
  byId: Map<string, BenchmarkRecord>;
  count: number;
}

export async function loadBenchmarks(fetchJson: FetchJson): Promise<BenchmarkSource> {
  const res = await fetchJson(OPENROUTER_URL);
  const body = res.body as { data?: RawModel[] };
  if (!Array.isArray(body?.data)) throw new Error('openrouter: expected {data:[...]}');

  const records: BenchmarkRecord[] = body.data.map((m) => {
    const aa = m.benchmarks?.artificial_analysis;
    const arena = (m.benchmarks?.design_arena ?? []).filter(
      (r) => r.arena === 'models' && typeof r.elo === 'number',
    );
    const price = Number(m.pricing?.completion ?? NaN);
    return {
      id: m.id,
      canonicalSlug: m.canonical_slug,
      vendor: m.id.split('/')[0],
      intelligence: aa?.intelligence_index,
      coding: aa?.coding_index,
      agentic: aa?.agentic_index,
      designElo: arena.length ? arena.reduce((s, r) => s + r.elo!, 0) / arena.length : undefined,
      contextLength: m.context_length,
      // OpenRouter prices per token; the catalog works in USD per million.
      costOutPerM: Number.isFinite(price) ? price * 1_000_000 : undefined,
    };
  });

  return {
    index: buildIndex(records),
    byId: new Map(records.map((r) => [r.id, r])),
    count: records.length,
  };
}

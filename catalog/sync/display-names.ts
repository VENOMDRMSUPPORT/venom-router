/**
 * Reviewed display-name overrides.
 *
 * The display name a row carries is otherwise whatever models.dev transcribed.
 * When the provider's own documentation spells the offering differently, the
 * provider is the first party and wins — the same precedence the fact resolvers
 * apply to operational fields. Entries are corrections, not pins: a name that
 * already agrees stays out of the overlay, so an upstream fix is never shadowed
 * by a stale copy.
 *
 * Applied inside the roster engine (the one place `models.display_name` is
 * written), so both entry points — CLI and service — carry it by construction.
 */
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import type { ReviewedFact } from './reviewed-facts.ts';

const HERE = dirname(fileURLToPath(import.meta.url));

/** Reuses the reviewed-fact shape on purpose: one provenance contract, not two. */
export type DisplayNameOverlay = Record<string, ReviewedFact<string>>;

const isObject = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

/** Validate the display-name overlay at its trust boundary. */
export function parseDisplayNames(raw: unknown): DisplayNameOverlay {
  if (!isObject(raw) || !isObject(raw.names)) throw new Error('display names must contain a names object');
  const parsed: DisplayNameOverlay = {};
  for (const [key, entry] of Object.entries(raw.names)) {
    if (!key.includes('/')) throw new Error(`invalid display name key (expected provider/model): ${key}`);
    if (!isObject(entry)) throw new Error(`${key} must be an object`);
    if (typeof entry.value !== 'string' || !entry.value.trim()) {
      throw new Error(`${key}.value must be a non-empty string`);
    }
    if (typeof entry.ref !== 'string' || !entry.ref.trim()) throw new Error(`${key}.ref is required`);
    if (typeof entry.sourceUrl !== 'string' || !/^https?:\/\//.test(entry.sourceUrl)) {
      throw new Error(`${key}.sourceUrl must be an HTTP URL`);
    }
    if (
      !Array.isArray(entry.evidence) || entry.evidence.length === 0 ||
      entry.evidence.some((line) => typeof line !== 'string' || !line.trim())
    ) {
      throw new Error(`${key}.evidence must contain at least one line`);
    }
    if (typeof entry.reviewedAt !== 'string' || !entry.reviewedAt.trim()) {
      throw new Error(`${key}.reviewedAt is required`);
    }
    parsed[key] = {
      value: entry.value,
      ref: entry.ref,
      sourceUrl: entry.sourceUrl,
      evidence: entry.evidence,
      reviewedAt: entry.reviewedAt,
    };
  }
  return parsed;
}

export function loadDisplayNames(): DisplayNameOverlay {
  return parseDisplayNames(JSON.parse(readFileSync(join(HERE, '..', 'overlays', 'display-names.json'), 'utf8')));
}

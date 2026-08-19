/**
 * Load the reviewed quality bounds.
 *
 * One module, imported by both entry points, for the reason `sync/pipeline.ts`
 * spells out: a source configured in the CLI and not in the service is a
 * difference that nothing fails on — the six-hourly run would simply drop every
 * bound and the figure would vanish from the catalog with no error anywhere.
 *
 * Keyed by catalog model id, exactly like the identity overlay, so the same id
 * sold by two providers gets the same reviewed bound rather than one of them
 * silently going unrated.
 */

import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));

export interface ReviewedBound {
  value: number;
  side: 'lower' | 'upper';
  /** Why this relation holds, in a form a later reader can argue with. */
  reason: string;
  /** The measured model the bound is relative to. NEVER used as an identity. */
  referenceModel: string;
  evidence: string[];
  sourceUrl?: string;
}

export function loadQualityBounds(): Record<string, ReviewedBound> {
  return JSON.parse(readFileSync(join(HERE, '..', 'overlays', 'quality-bounds.json'), 'utf8')).bounds ?? {};
}

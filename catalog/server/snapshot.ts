/**
 * The SPA's offline fallback.
 *
 * One rule, and the whole file exists to hold it: **the snapshot is the API
 * answer, serialised.** Not a dump of the rows behind it, not a summary of it —
 * the same objects `/v1/models` and `/v1/providers` put on the wire, produced by
 * the same read-model, so the offline page can be stale but never *different*.
 *
 * It used to be the other thing. Two entry points each wrote their own SELECT of
 * raw database columns, and the SPA rebuilt an API payload from those columns by
 * hand. A row dump has no identity state, no conflict rows and no completeness
 * verdict to read, so that reconstruction had two options for every counter it
 * could not answer, and it took both: three dashboard tiles rendered "MISSING",
 * and two rendered fabrications — `catalogReady` set to every model in the file
 * and `needsVerification` to zero, which is the claim "we checked, nothing is
 * incomplete" made by code that never checked. Against a catalog with nine
 * incomplete rows, that is the failure mode this design forecloses: there is now
 * no derivation to disagree with, because there is no second derivation.
 */

import { writeFileSync, mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import type { Db } from '../db/index.ts';
import { loadModels, loadProviders, loadMeta, type ApiModel, type ApiProvider, type CatalogMeta } from './read-model.ts';

const HERE = dirname(fileURLToPath(import.meta.url));

/**
 * `public/snapshot/`, because that is the one directory Vite serves in dev and
 * copies into `dist/` on build. The previous target, `data/snapshot/`, is read
 * by nothing: the file the browser actually fetched had to be copied across by
 * hand, and so sat six days and one methodology version behind the database it
 * claimed to mirror.
 */
export const SNAPSHOT_DIR = join(HERE, '..', 'public', 'snapshot');

export interface Snapshot {
  generatedAt: string;
  models: ApiModel[];
  providers: ApiProvider[];
  meta: CatalogMeta;
}

/** @param now injected so a test can assert the stamp instead of tolerating it. */
export function buildSnapshot(db: Db, now: () => Date = () => new Date()): Snapshot {
  const at = now();
  const models = loadModels(db);
  return {
    generatedAt: at.toISOString(),
    models,
    providers: loadProviders(db, models, at),
    meta: loadMeta(db, models),
  };
}

export function writeSnapshot(db: Db, dir: string = SNAPSHOT_DIR, now?: () => Date): void {
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, 'catalog.json'), JSON.stringify(buildSnapshot(db, now), null, 1));
}

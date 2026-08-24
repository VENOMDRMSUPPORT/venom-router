#!/usr/bin/env node
/**
 * Re-derive only offerings that already have a recorded conflict.
 *
 * This is a maintenance command for P1/K3. It refreshes model facts and conflict
 * rows from the current shared source feeds, but never fetches provider rosters,
 * calls provider detail endpoints, re-probes models, evaluates models, or scores.
 * The database remains single-writer through openBatchDb.
 *
 * Usage:
 *   npm run rederive:conflicts
 *   npm run rederive:conflicts -- --db=:memory:
 */

import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { openBatchDb } from './batch-db.ts';
import type { Db } from '../db/index.ts';
import { createFetchJson } from '../sync/http.ts';
import { BILLING } from '../sync/providers/index.ts';
import { loadSpecs } from '../sync/sources/models-dev.ts';
import { loadVendors } from '../sync/vendor-registry.ts';
import { loadBenchmarks } from '../sync/sources/openrouter.ts';
import { canonicalFromBenchmarks, enrich } from '../sync/enrich/enrich.ts';
import { loadReviewedFacts } from '../sync/reviewed-facts.ts';
import { loadEvaluationIdentities } from '../sync/evaluation/identity.ts';

const HERE = dirname(fileURLToPath(import.meta.url));
const arg = (name: string) => process.argv.find((value) => value.startsWith(`--${name}=`))?.split('=').slice(1).join('=');

function loadIdentityOverlay(): Record<string, string> {
  const raw = JSON.parse(readFileSync(join(HERE, '..', 'overlays', 'identity.json'), 'utf8')) as {
    mappings?: Record<string, string>;
  };
  return raw.mappings ?? {};
}

function affectedOfferings(db: Db): Set<string> {
  const rows = db.prepare(
    `SELECT DISTINCT c.provider_id, c.model_id
     FROM model_conflicts c
     JOIN models m ON m.provider_id = c.provider_id AND m.model_id = c.model_id
     WHERE m.status IN ('active', 'missing')
     ORDER BY c.provider_id, c.model_id`,
  ).all() as unknown as { provider_id: string; model_id: string }[];
  return new Set(rows.map((row) => `${row.provider_id}/${row.model_id}`));
}

export async function main(): Promise<number> {
  const db = await openBatchDb(arg('db'));
  try {
    const targets = affectedOfferings(db);
    console.log(`affected offerings: ${targets.size}`);
    if (targets.size === 0) {
      console.log('nothing to re-derive');
      return 0;
    }

    console.log('loading shared source feeds (no provider roster sync)...');
    const fetchJson = createFetchJson();
    const [specs, benchmarks] = await Promise.all([
      loadSpecs(fetchJson, loadVendors()),
      loadBenchmarks(fetchJson),
    ]);

    const summary = enrich({
      db,
      canonical: canonicalFromBenchmarks(benchmarks),
      overlay: loadIdentityOverlay(),
      billing: BILLING,
      lookupSpec: specs.lookup,
      intrinsic: specs.intrinsic,
      firstPartyLimits: specs.firstPartyLimits,
      vendorIdentity: specs.vendorIdentity,
      reviewedFacts: loadReviewedFacts(),
      evaluationIdentities: loadEvaluationIdentities(),
      targets,
      now: () => new Date().toISOString(),
    });

    const format = (values: Record<string, number>) =>
      Object.entries(values).map(([key, value]) => `${key}=${value}`).join(' ') || 'none';
    console.log(`rows re-derived   : ${summary.rows}`);
    console.log(`conflicts found   : ${format(summary.conflicts)}`);
    console.log(`facts still missing: ${format(summary.stillMissing)}`);
    console.log(`facts retired     : ${format(summary.retired)}`);
    return 0;
  } finally {
    db.close();
  }
}

if (import.meta.filename === process.argv[1]) {
  main().then((code) => process.exit(code), (error) => {
    console.error(error);
    process.exit(1);
  });
}

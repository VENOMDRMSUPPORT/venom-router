#!/usr/bin/env node
/**
 * `npm run sync` — refresh every provider, then rescore.
 *
 * Usage:
 *   npm run sync                       all providers
 *   npm run sync -- --provider=zen     one provider (substring match on the id)
 *   npm run sync -- --db=:memory:      run against a throwaway database
 *   npm run sync -- --profile=coding   score with a different published profile
 */

import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { openDb } from '../db/index.ts';
import { createFetchJson } from './http.ts';
import { syncProvider } from './engine.ts';
import { ADAPTERS, BILLING } from './providers/index.ts';
import { loadSpecs } from './sources/models-dev.ts';
import { loadBenchmarks } from './sources/openrouter.ts';
import { scoreAll } from './score/pipeline.ts';
import { enrich, canonicalFromBenchmarks } from './enrich/enrich.ts';
import type { ScoreProfile } from './score/venom-score.ts';

const HERE = dirname(fileURLToPath(import.meta.url));
const arg = (name: string) => process.argv.find((a) => a.startsWith(`--${name}=`))?.split('=').slice(1).join('=');

function loadProfiles(): { methodologyVersion: string; profiles: ScoreProfile[] } {
  const raw = JSON.parse(readFileSync(join(HERE, '..', 'overlays', 'score-profiles.json'), 'utf8'));
  return { methodologyVersion: raw.methodologyVersion, profiles: raw.profiles };
}

function loadIdentityOverlay(): Record<string, string> {
  const raw = JSON.parse(readFileSync(join(HERE, '..', 'overlays', 'identity.json'), 'utf8'));
  return raw.mappings ?? {};
}

export async function main(): Promise<number> {
  const db = openDb(arg('db'));
  const fetchJson = createFetchJson();
  const only = arg('provider');
  const adapters = only ? ADAPTERS.filter((a) => a.id.includes(only)) : ADAPTERS;
  if (adapters.length === 0) {
    console.error(`no provider matches "${only}". Known: ${ADAPTERS.map((a) => a.id).join(', ')}`);
    return 2;
  }

  console.log('fetching shared sources...');
  const sourceFetchedAt = new Date().toISOString();
  const [specs, benchmarks] = await Promise.all([loadSpecs(fetchJson), loadBenchmarks(fetchJson)]);
  console.log(`  models.dev : ${specs.providerCount} providers`);
  console.log(`  openrouter : ${benchmarks.count} models`);

  console.log('\nsyncing rosters...');
  // Providers run independently: one failing must not stop the others (layer 1).
  const results = [];
  for (const adapter of adapters) {
    const r = await syncProvider(adapter, { db, fetchJson, now: () => new Date().toISOString(), lookupSpec: specs.lookup });
    results.push(r);
    const detail =
      r.outcome === 'ok' ? `${r.rosterCount} models  +${r.added.length} -${r.removed.length} ~${r.changed}`
      : r.outcome === 'quarantined' ? `QUARANTINED — ${r.quarantineReason}`
      : `FAILED — ${r.error}`;
    console.log(`  ${adapter.id.padEnd(14)} ${detail}`);
  }

  const { methodologyVersion, profiles } = loadProfiles();
  const profile = profiles.find((p) => p.id === (arg('profile') ?? 'balanced'));
  if (!profile) {
    console.error(`unknown profile. Known: ${profiles.map((p) => p.id).join(', ')}`);
    return 2;
  }

  // Enrichment runs BEFORE scoring: VO is derived from operational facts, so
  // resolving those facts first is what lets a previously factless model score.
  console.log('\nenriching operational metadata...');
  const overlay = loadIdentityOverlay();
  const en = enrich({
    db, canonical: canonicalFromBenchmarks(benchmarks), overlay, billing: BILLING,
    now: () => new Date().toISOString(),
  });
  const fmt = (o: Record<string, number>) => Object.entries(o).map(([k, v]) => `${k}=${v}`).join(' ') || 'none';
  console.log(`  rows              : ${en.rows}`);
  console.log(`  filled by fallback: ${fmt(en.filled)}`);
  console.log(`  still unresolved  : ${fmt(en.stillMissing)}`);
  console.log(`  cost semantics    : ${fmt(en.costKinds)}`);

  console.log('\nscoring...');
  const summary = scoreAll({
    db, benchmarks, overlay, profile, methodologyVersion, sourceFetchedAt,
    now: () => new Date().toISOString(),
  });

  if (summary.calibration) {
    const c = summary.calibration;
    console.log(`  calibration : n=${c.n} rho=${c.rho.toFixed(3)} LOO-RMSE=${c.looRmse.toFixed(2)} (sd ${c.baselineSd.toFixed(2)}) ${summary.calibrationAccepted ? 'ACCEPTED' : 'WITHHELD'}`);
    if (c.excludedGroups.length) console.log(`  excluded    : ${c.excludedGroups.join(', ')} (group bias over threshold)`);
  } else {
    console.log('  calibration : not fitted — insufficient overlap');
  }
  const { measured, calibrated, bounded, unrated } = summary.levels as Record<string, number>;
  const withEvidence = measured + calibrated + bounded;
  console.log(`  VQ levels   : measured=${measured} calibrated=${calibrated} bounded=${bounded} unrated=${unrated}`);
  console.log(`  VQ coverage : ${withEvidence}/${summary.total} (${Math.round((withEvidence / summary.total) * 100)}%) with quality evidence`);
  if (summary.reviewQueue) console.log(`  review queue: ${summary.reviewQueue} ambiguous identities awaiting a human`);

  writeSnapshot(db);
  return results.some((r) => r.outcome === 'failed') ? 1 : 0;
}

/** JSON mirror of the DB: the SPA's offline fallback and a readable git diff. */
function writeSnapshot(db: ReturnType<typeof openDb>): void {
  const dir = join(HERE, '..', 'data', 'snapshot');
  mkdirSync(dir, { recursive: true });
  const providers = db.prepare('SELECT * FROM providers ORDER BY id').all();
  const models = db
    .prepare(
      `SELECT m.*,
              vq.value vq_value, vq.uncertainty vq_uncertainty, vq.bound vq_bound,
              vq.evidence_level vq_level, vq.source vq_source, vq.source_model_id vq_source_id,
              vq.identity_rule vq_rule, vq.precision_dp vq_precision,
              vo.value vo_value, vo.dimensions vo_dimensions
       FROM models m
       LEFT JOIN model_scores vq ON vq.provider_id=m.provider_id AND vq.model_id=m.model_id AND vq.kind='VQ'
       LEFT JOIN model_scores vo ON vo.provider_id=m.provider_id AND vo.model_id=m.model_id AND vo.kind='VO'
       WHERE m.status != 'retired' ORDER BY m.provider_id, m.model_id`,
    )
    .all();
  writeFileSync(
    join(dir, 'catalog.json'),
    JSON.stringify({ generatedAt: new Date().toISOString(), providers, models }, null, 1),
  );
  console.log(`\nsnapshot   : data/snapshot/catalog.json (${models.length} rows)`);
}

if (import.meta.filename === process.argv[1]) {
  main().then((code) => process.exit(code), (err) => { console.error(err); process.exit(1); });
}

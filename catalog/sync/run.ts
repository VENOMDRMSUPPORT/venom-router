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

import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { openBatchDb } from '../scripts/batch-db.ts';
import { createFetchJson } from './http.ts';
import { ADAPTERS, BILLING } from './providers/index.ts';
import { loadSpecs } from './sources/models-dev.ts';
import { loadVendors } from './vendor-registry.ts';
import { loadQualityBounds } from './quality-bounds.ts';
import { loadReviewedFacts } from './reviewed-facts.ts';
import { loadDisplayNames } from './display-names.ts';
import { loadEvaluationIdentities } from './evaluation/identity.ts';
import { loadBenchmarks } from './sources/openrouter.ts';
import { runSyncPipeline } from './pipeline.ts';
import { writeSnapshot, SNAPSHOT_DIR } from '../server/snapshot.ts';
import type { RejectionOverlay } from './identity-rejections.ts';
import type { ScoreProfile } from './score/venom-score.ts';

const HERE = dirname(fileURLToPath(import.meta.url));
const arg = (name: string) => process.argv.find((a) => a.startsWith(`--${name}=`))?.split('=').slice(1).join('=');

function loadProfiles(): { methodologyVersion: string; profiles: ScoreProfile[] } {
  const raw = JSON.parse(readFileSync(join(HERE, '..', 'overlays', 'score-profiles.json'), 'utf8'));
  return { methodologyVersion: raw.methodologyVersion, profiles: raw.profiles };
}

function loadIdentityOverlay(): { mappings: Record<string, string>; rejected: RejectionOverlay } {
  const raw = JSON.parse(readFileSync(join(HERE, '..', 'overlays', 'identity.json'), 'utf8'));
  return { mappings: raw.mappings ?? {}, rejected: raw.rejected ?? { entries: {} } };
}

export async function main(): Promise<number> {
  const db = await openBatchDb(arg('db'));
  const fetchJson = createFetchJson();
  const only = arg('provider');
  const adapters = only ? ADAPTERS.filter((a) => a.id.includes(only)) : ADAPTERS;
  if (adapters.length === 0) {
    console.error(`no provider matches "${only}". Known: ${ADAPTERS.map((a) => a.id).join(', ')}`);
    return 2;
  }

  console.log('fetching shared sources...');
  const sourceFetchedAt = new Date().toISOString();
  const [specs, benchmarks] = await Promise.all([loadSpecs(fetchJson, loadVendors()), loadBenchmarks(fetchJson)]);
  console.log(`  models.dev : ${specs.providerCount} providers`);
  console.log(`  openrouter : ${benchmarks.count} models`);

  const { methodologyVersion, profiles } = loadProfiles();
  const profile = profiles.find((p) => p.id === (arg('profile') ?? 'balanced'));
  if (!profile) {
    console.error(`unknown profile. Known: ${profiles.map((p) => p.id).join(', ')}`);
    return 2;
  }
  const { mappings: overlay, rejected } = loadIdentityOverlay();

  console.log('\nsyncing rosters, enriching, and scoring...');
  const result = await runSyncPipeline({
    db, fetchJson, adapters, specs, benchmarks, billing: BILLING, overlay,
    rejections: rejected, bounds: loadQualityBounds(), reviewedFacts: loadReviewedFacts(), displayNames: loadDisplayNames(), evaluationIdentities: loadEvaluationIdentities(), profile, methodologyVersion, sourceFetchedAt,
    now: () => new Date().toISOString(),
  });

  // Providers run independently: one failing must not stop the others (layer 1).
  for (const r of result.providers) {
    const detail =
      r.outcome === 'ok' ? `${r.rosterCount} models  +${r.added.length} -${r.removed.length} ~${r.changed}`
      : r.outcome === 'quarantined' ? `QUARANTINED — ${r.quarantineReason}`
      : `FAILED — ${r.error}`;
    console.log(`  ${r.provider.padEnd(14)} ${detail}`);
  }

  if (result.detail.asked) console.log(`  provider detail   : asked ${result.detail.asked}, answered ${result.detail.answered}`);
  const pub = result.publish;
  if (pub.excluded.paid || pub.excluded.not_proven_free || pub.excluded.plan_required || pub.restored)
    console.log(`  publish policy    : excluded paid=${pub.excluded.paid} not_proven_free=${pub.excluded.not_proven_free} plan_required=${pub.excluded.plan_required}, restored ${pub.restored}`);

  const rej = result.rejections;
  if (rej) {
    if (rej.records) console.log(`  identity rejections: ${rej.records} record(s) across ${rej.offerings} offering(s)`);
    if (rej.skipped.length) console.log(`  rejections skipped (no offering serves them): ${rej.skipped.join(', ')}`);
  }
  const fmt = (o: Record<string, number>) => Object.entries(o).map(([k, v]) => `${k}=${v}`).join(' ') || 'none';
  const en = result.enrich;
  console.log(`  rows              : ${en.rows}`);
  console.log(`  filled by fallback: ${fmt(en.filled)}`);
  console.log(`  still unresolved  : ${fmt(en.stillMissing)}`);
  console.log(`  cost semantics    : ${fmt(en.costKinds)}`);
  if (Object.keys(en.retired).length) console.log(`  retired (stale)   : ${fmt(en.retired)}`);
  if (Object.keys(en.protectedStale).length) console.log(`  kept (unverified) : ${fmt(en.protectedStale)}`);

  console.log('\nscoring...');
  const summary = result.scoring;
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
  console.log('');
  console.log('snapshot   : ' + SNAPSHOT_DIR + '/catalog.json');

  return result.providers.some((r) => r.outcome === 'failed') ? 1 : 0;
}

if (import.meta.filename === process.argv[1]) {
  main().then((code) => process.exit(code), (err) => { console.error(err); process.exit(1); });
}

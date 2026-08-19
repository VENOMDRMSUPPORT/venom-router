#!/usr/bin/env node
import { openDb } from '../db/index.ts';
import { buildEvaluationFixtures, fixtureDigest } from '../sync/evaluation/fixtures.ts';
import { createEvaluationTransport, resolveEvaluationCredential } from '../sync/evaluation/provider-transport.ts';
import { recalculatePublishedOffers } from '../sync/evaluation/recalculate.ts';
import { planEvaluation } from '../sync/evaluation/plan.ts';
import { persistDimensionEvaluation } from '../sync/evaluation/runner.ts';
import { QUALITY_DIMENSIONS, type QualityDimension } from '../sync/evaluation/score.ts';
import { assertServiceNotListening } from './service-guard.ts';

interface OfferRow {
  provider_id: string;
  model_id: string;
}

const valueOf = (name: string): string | null => {
  const prefix = `--${name}=`;
  return process.argv.find((arg) => arg.startsWith(prefix))?.slice(prefix.length) ?? null;
};

const providerFilter = valueOf('providers')?.split(',').filter(Boolean) ?? ['clinepass', 'ollama-cloud'];
const modelFilter = valueOf('models')?.split(',').filter(Boolean) ?? [];
const dimensionFilter = (valueOf('dimensions')?.split(',').filter(Boolean) ?? [...QUALITY_DIMENSIONS]) as QualityDimension[];
const force = process.argv.includes('--force');
for (const dimension of dimensionFilter) {
  if (!QUALITY_DIMENSIONS.includes(dimension)) throw new Error(`unknown_dimension:${dimension}`);
}

// A refusal here is an expected outcome, not a crash: print the reason and
// leave, rather than burying it under a stack trace.
try {
  await assertServiceNotListening();
} catch (error) {
  console.error(error instanceof Error ? error.message : String(error));
  process.exit(1);
}

const db = openDb(process.env.CATALOG_DB);
const fixtures = buildEvaluationFixtures();
const testSetHash = fixtureDigest(fixtures);
const now = () => new Date().toISOString();


try {
  recalculatePublishedOffers(db, now());
  const placeholders = providerFilter.map(() => '?').join(',');
  // The roster only. Identity resolution belongs to planEvaluation, which both
  // this batch and the service call, so it is not re-derived here.
  const rows = db.prepare(`
    SELECT m.provider_id, m.model_id
    FROM models m
    WHERE m.status IN ('active','missing') AND m.provider_id IN (${placeholders})
    ORDER BY m.provider_id, m.model_id
  `).all(...providerFilter) as unknown as OfferRow[];
  const selected = rows.filter((row) => modelFilter.length === 0 || modelFilter.includes(row.model_id));
  const seenIdentities = new Set<string>();
  let completed = 0;
  let skipped = 0;
  let failed = 0;
  for (const offer of selected) {
    // One rule decides what runs, shared with the service. See sync/evaluation/plan.ts.
    const plan = planEvaluation(db, {
      providerId: offer.provider_id,
      modelId: offer.model_id,
      testSetHash,
      force,
    });
    if (plan.blocked) {
      console.log(JSON.stringify({ event: 'skip', providerId: offer.provider_id, modelId: offer.model_id, reason: plan.blocked }));
      skipped++;
      continue;
    }
    const identityId = plan.identityId!;
    if (seenIdentities.has(identityId)) continue;
    seenIdentities.add(identityId);
    for (const entry of plan.skipped) {
      if (!dimensionFilter.includes(entry.dimension)) continue;
      console.log(JSON.stringify({ event: 'skip', identityId, dimension: entry.dimension, reason: entry.reason }));
      skipped++;
    }
    const credential = resolveEvaluationCredential(offer.provider_id)!;
    const transport = createEvaluationTransport({
      providerId: offer.provider_id,
      modelId: offer.model_id,
      credential,
    });
    for (const dimension of plan.dimensions.filter((entry) => dimensionFilter.includes(entry))) {
      console.log(JSON.stringify({ event: 'start', providerId: offer.provider_id, modelId: offer.model_id, identityId, dimension, testSetHash }));
      const result = await persistDimensionEvaluation({
        db,
        providerId: offer.provider_id,
        modelId: offer.model_id,
        identityId,
        dimension,
        scenarios: fixtures[dimension],
        transport,
        credential,
        testSetHash,
        now,
        // --force means "re-evaluate", so it must not inherit samples an earlier
        // grader scored; a plain re-run still resumes whatever this leaves behind.
        fresh: force,
      });
      if (result.status === 'complete') {
        completed++;
        console.log(JSON.stringify({ event: 'complete', providerId: offer.provider_id, modelId: offer.model_id, identityId, dimension, score: result.score.score, samples: result.samples.length }));
      } else {
        failed++;
        console.log(JSON.stringify({ event: 'incomplete', providerId: offer.provider_id, modelId: offer.model_id, identityId, dimension, reason: result.reason, samples: result.samples.length }));
      }
      const overall = recalculatePublishedOffers(db, now());
      console.log(JSON.stringify({ event: 'recalculated', ...overall }));
    }
  }
  console.log(JSON.stringify({ event: 'done', completed, skipped, failed, selectedOffers: selected.length, uniqueIdentities: seenIdentities.size }));
} finally {
  db.close();
}

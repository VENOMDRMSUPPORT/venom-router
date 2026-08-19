#!/usr/bin/env node
import { openDb } from '../db/index.ts';
import { buildEvaluationFixtures, fixtureDigest } from '../sync/evaluation/fixtures.ts';
import { createEvaluationTransport, resolveEvaluationCredential } from '../sync/evaluation/provider-transport.ts';
import { recalculatePublishedOffers } from '../sync/evaluation/recalculate.ts';
import { createEvaluationRepository } from '../sync/evaluation/repository.ts';
import { persistDimensionEvaluation } from '../sync/evaluation/runner.ts';
import { QUALITY_DIMENSIONS, type QualityDimension } from '../sync/evaluation/score.ts';
import { shouldSkipDimension } from './evaluation-selection.ts';

interface OfferRow {
  provider_id: string;
  model_id: string;
  canonical_id: string | null;
  vendor_identity_json: string | null;
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

const db = openDb(process.env.CATALOG_DB);
const fixtures = buildEvaluationFixtures();
const testSetHash = fixtureDigest(fixtures);
const now = () => new Date().toISOString();

function identityOf(row: OfferRow): string | null {
  if (row.canonical_id) return row.canonical_id;
  if (!row.vendor_identity_json) return null;
  try {
    const parsed = JSON.parse(row.vendor_identity_json) as unknown;
    return typeof parsed === 'string' ? parsed : null;
  } catch {
    return null;
  }
}

try {
  recalculatePublishedOffers(db, now());
  const placeholders = providerFilter.map(() => '?').join(',');
  const rows = db.prepare(`
    SELECT m.provider_id, m.model_id,
      (SELECT source_model_id FROM model_scores s
        WHERE s.provider_id=m.provider_id AND s.model_id=m.model_id AND s.kind='VQ') canonical_id,
      (SELECT value FROM model_facts f
        WHERE f.provider_id=m.provider_id AND f.model_id=m.model_id AND f.field='vendorIdentity') vendor_identity_json
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
    const identityId = identityOf(offer);
    if (!identityId) {
      console.log(JSON.stringify({ event: 'skip', providerId: offer.provider_id, modelId: offer.model_id, reason: 'identity_unresolved' }));
      skipped++;
      continue;
    }
    if (seenIdentities.has(identityId)) continue;
    seenIdentities.add(identityId);
    const credential = resolveEvaluationCredential(offer.provider_id);
    if (!credential) {
      console.log(JSON.stringify({ event: 'skip', providerId: offer.provider_id, modelId: offer.model_id, identityId, reason: 'missing_credentials' }));
      skipped++;
      continue;
    }
    const repository = createEvaluationRepository(db);
    const existing = repository.identityDimensions(identityId)
      .map((row) => ({ dimension: row.dimension, status: row.status, testSetHash: row.testSetHash }));
    const applicability = new Map(repository.offerDimensions(offer.provider_id, offer.model_id)
      .map((row) => [row.dimension, row.status]));
    const transport = createEvaluationTransport({
      providerId: offer.provider_id,
      modelId: offer.model_id,
      credential,
    });
    for (const dimension of dimensionFilter) {
      if (shouldSkipDimension(existing, dimension, testSetHash, force)) {
        console.log(JSON.stringify({ event: 'skip', identityId, dimension, reason: 'already_scored' }));
        skipped++;
        continue;
      }
      if (applicability.get(dimension) === 'unsupported') {
        console.log(JSON.stringify({ event: 'skip', identityId, dimension, reason: 'unsupported' }));
        skipped++;
        continue;
      }
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

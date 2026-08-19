#!/usr/bin/env node
import { openDb } from '../db/index.ts';
import { resolveEvaluationCredential } from '../sync/evaluation/provider-transport.ts';
import { recalculatePublishedOffers } from '../sync/evaluation/recalculate.ts';
import { createEvaluationRepository } from '../sync/evaluation/repository.ts';
import { createStreamingSpeedProbe } from '../sync/evaluation/speed-probe.ts';
import { persistSpeedEvaluation } from '../sync/evaluation/speed-runner.ts';

interface OfferRow { provider_id: string; model_id: string }
const valueOf = (name: string): string | null => {
  const prefix = `--${name}=`;
  return process.argv.find((arg) => arg.startsWith(prefix))?.slice(prefix.length) ?? null;
};
const providers = valueOf('providers')?.split(',').filter(Boolean) ?? ['clinepass', 'ollama-cloud'];
const models = valueOf('models')?.split(',').filter(Boolean) ?? [];
const force = process.argv.includes('--force');
const db = openDb(process.env.CATALOG_DB);
const now = () => new Date().toISOString();

try {
  const placeholders = providers.map(() => '?').join(',');
  const offers = (db.prepare(`SELECT provider_id,model_id FROM models
    WHERE status IN ('active','missing') AND provider_id IN (${placeholders})
    ORDER BY provider_id,model_id`).all(...providers) as unknown as OfferRow[])
    .filter((offer) => models.length === 0 || models.includes(offer.model_id));
  let completed = 0;
  let skipped = 0;
  let failed = 0;
  for (const offer of offers) {
    const existing = createEvaluationRepository(db).offerDimensions(offer.provider_id, offer.model_id)
      .find((row) => row.dimension === 'speed');
    if (!force && existing?.status === 'scored') {
      console.log(JSON.stringify({ event: 'skip', ...offer, reason: 'already_scored' }));
      skipped++;
      continue;
    }
    const credential = resolveEvaluationCredential(offer.provider_id);
    if (!credential) {
      console.log(JSON.stringify({ event: 'skip', ...offer, reason: 'missing_credentials' }));
      skipped++;
      continue;
    }
    console.log(JSON.stringify({ event: 'start', providerId: offer.provider_id, modelId: offer.model_id, dimension: 'speed' }));
    const result = await persistSpeedEvaluation({
      db,
      providerId: offer.provider_id,
      modelId: offer.model_id,
      probe: createStreamingSpeedProbe({ providerId: offer.provider_id, modelId: offer.model_id, credential }),
      now,
    });
    if (result.status === 'complete') completed++;
    else failed++;
    console.log(JSON.stringify({ event: result.status, providerId: offer.provider_id, modelId: offer.model_id, reason: result.reason, samples: result.samples.length }));
    console.log(JSON.stringify({ event: 'recalculated', ...recalculatePublishedOffers(db, now()) }));
  }
  console.log(JSON.stringify({ event: 'done', completed, skipped, failed, selectedOffers: offers.length }));
} finally {
  db.close();
}

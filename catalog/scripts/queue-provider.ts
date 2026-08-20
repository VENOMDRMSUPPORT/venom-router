#!/usr/bin/env node
/**
 * Queue every model of one provider that still needs evidence.
 *
 * Goes through the running service rather than opening its own connection to the
 * database: the service is the single writer, and its queue already carries the
 * things that make a run across a usage window cheap — sample-level resume, the
 * abandon-on-first-unrecoverable-failure rule, and the speed preflight.
 *
 * It asks for the plan before queueing anything, so what a run will cost is
 * printed before a single request is spent, and an offer the catalog cannot
 * evaluate at all — no resolved identity, no credential — is named and skipped
 * rather than queued to fail.
 *
 *   node scripts/queue-provider.ts opencode-zen
 *   node scripts/queue-provider.ts opencode-zen --dry-run
 *
 * Safe to re-run. A model that finished is planned at zero cost and skipped, so
 * running it again after a usage window reopens picks up exactly what is left.
 */
import { BIND_HOST, DEFAULT_PORT } from '../server/index.ts';

const providerId = process.argv[2];
const dryRun = process.argv.includes('--dry-run');
const base = `http://${BIND_HOST}:${DEFAULT_PORT}`;

if (!providerId || providerId.startsWith('--')) {
  console.error('usage: node scripts/queue-provider.ts <providerId> [--dry-run]');
  process.exit(2);
}

interface Plan {
  dimensions: string[];
  speed: 'missing' | 'scored';
  blocked: string | null;
  estimatedRequests: number;
}

async function getJson<T>(path: string): Promise<T> {
  const response = await fetch(`${base}${path}`);
  if (!response.ok) throw new Error(`${path} answered ${response.status}`);
  return (await response.json()) as T;
}

try {
  await getJson('/v1/evaluations');
} catch {
  console.error(
    `the catalog service is not answering on ${base}.\n`
    + 'Start it with `npm run serve` and try again — queueing goes through the service '
    + 'so that it stays the only writer.',
  );
  process.exit(1);
}

const { models } = await getJson<{ models: { modelId: string }[] }>(`/v1/models?provider=${encodeURIComponent(providerId)}`);
if (models.length === 0) {
  console.error(`no published models for provider "${providerId}".`);
  process.exit(1);
}

let queued = 0;
let cost = 0;
const skipped: string[] = [];

for (const { modelId } of models) {
  const { plan } = await getJson<{ plan: Plan }>(
    `/v1/models/${encodeURIComponent(providerId)}/${encodeURIComponent(modelId)}/evaluation`,
  );
  const needs = plan.dimensions.length + (plan.speed === 'missing' ? 1 : 0);

  if (plan.blocked) {
    skipped.push(`${modelId} — ${plan.blocked}`);
    continue;
  }
  if (needs === 0) continue;

  if (dryRun) {
    console.log(`would queue ${modelId} — ${needs} dimension(s), ${plan.estimatedRequests} requests`);
    queued++;
    cost += plan.estimatedRequests;
    continue;
  }

  const response = await fetch(`${base}/v1/evaluations`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ providerId, modelId }),
  });
  if (response.ok) {
    const { position } = (await response.json()) as { position: number };
    console.log(`queued #${position} ${modelId} — ${needs} dimension(s), ${plan.estimatedRequests} requests`);
    queued++;
    cost += plan.estimatedRequests;
  } else {
    const body = (await response.json().catch(() => ({}))) as { reason?: string; error?: string };
    skipped.push(`${modelId} — ${body.reason ?? body.error ?? `http_${response.status}`}`);
  }
}

console.log(`\n${dryRun ? 'would queue' : 'queued'} ${queued} model(s), about ${cost} requests.`);
if (skipped.length > 0) {
  console.log('not queued:');
  for (const line of skipped) console.log(`  ${line}`);
}
if (!dryRun && queued > 0) {
  console.log('\nWatch it from the dashboard, or:  curl -s http://127.0.0.1:8791/v1/evaluations');
  console.log('Stop everything at any time:      curl -s -X DELETE http://127.0.0.1:8791/v1/evaluations');
}

#!/usr/bin/env node
import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';
import { openDb } from '../db/index.ts';
import { importInspectEvaluation, type InspectEvaluationLog } from '../sync/evaluation/inspect-import.ts';
import { recalculatePublishedOffers } from '../sync/evaluation/recalculate.ts';
import type { QualityDimension } from '../sync/evaluation/score.ts';

const [logPath, providerId, modelId, identityId, dimension] = process.argv.slice(2);
if (!logPath || !providerId || !modelId || !identityId || !dimension) {
  throw new Error('usage: import-inspect-log <log> <provider> <model> <identity> <dimension>');
}
const artifact = resolve(logPath);
const raw = execFileSync('uv', [
  'run', '--with', 'inspect-evals', '--with', 'openai',
  'inspect', 'log', 'dump', artifact,
], { encoding: 'utf8', maxBuffer: 256 * 1024 * 1024 });
const log = JSON.parse(raw) as InspectEvaluationLog;
const db = openDb(process.env.CATALOG_DB);
try {
  const imported = importInspectEvaluation(db, log, {
    providerId, modelId, identityId, dimension: dimension as QualityDimension, artifactRef: artifact,
  });
  const overall = recalculatePublishedOffers(db, new Date().toISOString());
  console.log(JSON.stringify({ imported, overall }, null, 2));
} finally {
  db.close();
}

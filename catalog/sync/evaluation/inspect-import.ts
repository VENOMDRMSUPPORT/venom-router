import { createHash } from 'node:crypto';
import type { Db } from '../../db/index.ts';
import { benchmarkCrosswalkDigest, loadBenchmarkCrosswalk } from './benchmark-crosswalk.ts';
import { createEvaluationRepository } from './repository.ts';
import { OVERALL_SCORE_POLICY, smoothCriterionScore, type QualityDimension } from './score.ts';

export interface InspectEvaluationLog {
  status: string;
  eval: {
    task: string;
    model: string;
    config: { limit?: number; epochs?: number };
    packages?: Record<string, string>;
  };
  stats: { started_at: string; completed_at: string };
  samples: Array<{
    id: string | number;
    epoch: number;
    scores: Record<string, { value: unknown }>;
  }>;
}

export interface InspectImportTarget {
  providerId: string;
  modelId: string;
  identityId: string;
  dimension: QualityDimension;
  artifactRef?: string | null;
}

function sampleSuccess(sample: InspectEvaluationLog['samples'][number]): number {
  const score = Object.values(sample.scores)[0]?.value;
  if (typeof score === 'number' && Number.isFinite(score) && score >= 0 && score <= 1) return score;
  if (score === true || score === 'C' || score === 'correct') return 1;
  if (score === false || score === 'I' || score === 'incorrect') return 0;
  throw new Error(`unsupported Inspect score value for sample ${sample.id}`);
}

export function importInspectEvaluation(
  db: Db,
  log: InspectEvaluationLog,
  target: InspectImportTarget,
): { status: 'imported'; runId: number; samples: number; score: number } {
  const crosswalk = loadBenchmarkCrosswalk();
  const entry = crosswalk.dimensions[target.dimension];
  if (log.status !== 'success') throw new Error('Inspect evaluation must have status success');
  if (log.eval.task !== entry.task) throw new Error(`Inspect task does not match ${target.dimension} crosswalk`);
  if (log.eval.config.limit !== 20 || log.eval.config.epochs !== 3 || log.samples.length !== 60) {
    throw new Error('Inspect import requires exactly 20 samples x 3 epochs');
  }
  const epochs = new Map<string, Set<number>>();
  for (const sample of log.samples) {
    const id = String(sample.id);
    const seen = epochs.get(id) ?? new Set<number>();
    if (seen.has(sample.epoch) || sample.epoch < 1 || sample.epoch > 3) {
      throw new Error(`invalid Inspect epoch for sample ${id}`);
    }
    seen.add(sample.epoch);
    epochs.set(id, seen);
  }
  if (epochs.size !== 20 || [...epochs.values()].some((items) => items.size !== 3)) {
    throw new Error('Inspect import requires exactly 20 samples x 3 epochs');
  }

  const scored = log.samples.map((sample) => ({ sample, success: sampleSuccess(sample) }));
  const successes = scored.reduce((sum, item) => sum + item.success, 0);
  const result = smoothCriterionScore(successes, scored.length);
  const repository = createEvaluationRepository(db);
  const testSetHash = benchmarkCrosswalkDigest(crosswalk);
  const runKey = createHash('sha256').update(JSON.stringify({
    task: log.eval.task,
    model: log.eval.model,
    samples: scored.map(({ sample, success }) => [sample.id, sample.epoch, success]),
  })).digest('hex');
  const runId = repository.createRun({
    providerId: target.providerId,
    modelId: target.modelId,
    identityId: target.identityId,
    dimension: target.dimension,
    runKind: 'external',
    status: 'complete',
    evaluatorVersion: OVERALL_SCORE_POLICY.evaluatorVersion,
    rubricVersion: OVERALL_SCORE_POLICY.rubricVersion,
    testSetVersion: OVERALL_SCORE_POLICY.testSetVersion,
    testSetHash,
    methodologyVersion: OVERALL_SCORE_POLICY.methodologyVersion,
    region: OVERALL_SCORE_POLICY.region,
    independentRunKey: runKey,
    errorCode: null,
    startedAt: log.stats.started_at,
    finishedAt: log.stats.completed_at,
  });
  for (const { sample, success } of scored) {
    repository.appendSample({
      runId,
      scenarioId: String(sample.id),
      repetition: sample.epoch,
      outcome: success === 1 ? 'passed' : 'failed',
      weightedSuccesses: success,
      weightedCriteria: 1,
      metrics: null,
      artifactRef: target.artifactRef ?? null,
      errorCode: null,
      recordedAt: log.stats.completed_at,
    });
  }
  repository.saveIdentityDimension({
    identityId: target.identityId,
    dimension: target.dimension,
    score: result.score,
    rawRate: result.rawRate,
    uncertainty: result.uncertainty,
    confidence: result.confidence,
    sampleCount: result.sampleCount,
    status: 'scored',
    rubricVersion: OVERALL_SCORE_POLICY.rubricVersion,
    testSetHash,
    evidence: [`evaluation-run:${runId}`, `crosswalk:${crosswalk.version}`],
    evaluatedAt: log.stats.completed_at,
    methodologyVersion: OVERALL_SCORE_POLICY.methodologyVersion,
  });
  return { status: 'imported', runId, samples: scored.length, score: result.score };
}

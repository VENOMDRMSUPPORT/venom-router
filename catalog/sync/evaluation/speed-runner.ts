import type { Db } from '../../db/index.ts';
import { transaction } from '../../db/index.ts';
import { createEvaluationRepository } from './repository.ts';
import { runPool, runSpeedEvaluation, type SpeedRequestSample } from './runtime.ts';
import { OVERALL_SCORE_POLICY } from './score.ts';

export interface SpeedProbeResult extends SpeedRequestSample {
  errorCode: string | null;
}

export type SpeedProbe = () => Promise<SpeedProbeResult>;

export interface PersistSpeedEvaluationInput {
  db: Db;
  providerId: string;
  modelId: string;
  probe: SpeedProbe;
  now: () => string;
  /** Reports requests issued, warmups included, so the caller can show progress. */
  onProbe?: (completed: number, total: number) => void;
  /** Asked between samples so a stop costs at most the request in flight. */
  shouldStop?: () => boolean;
}

/** Warmups plus one request per sample: everything a speed run sends. */
export const SPEED_REQUESTS_PER_RUN =
  OVERALL_SCORE_POLICY.warmupRequests + OVERALL_SCORE_POLICY.scenarioCount;


export async function persistSpeedEvaluation(input: PersistSpeedEvaluationInput): Promise<{
  status: 'complete' | 'insufficient_evidence';
  reason: string | null;
  samples: SpeedProbeResult[];
}> {
  const startedAt = input.now();
  let issued = 0;
  const probe: SpeedProbe = async () => {
    const result = await input.probe();
    input.onProbe?.(Math.min(++issued, SPEED_REQUESTS_PER_RUN), SPEED_REQUESTS_PER_RUN);
    return result;
  };
  for (let warmup = 0; warmup < OVERALL_SCORE_POLICY.warmupRequests; warmup++) {
    if (input.shouldStop?.()) break;
    await probe();
  }
  const samples = await runPool(
    Array.from({ length: OVERALL_SCORE_POLICY.scenarioCount }, () => probe),
    OVERALL_SCORE_POLICY.speedProviderConcurrency,
    input.shouldStop,
  );
  const successful = samples.filter((sample) => sample.success && sample.ttftSeconds !== null
    && sample.outputTokensPerSecond !== null && sample.endToEndSeconds !== null);
  const scored = successful.length > 0 ? runSpeedEvaluation(samples) : null;
  const finishedAt = input.now();

  transaction(input.db, () => {
    const repository = createEvaluationRepository(input.db);
    const runId = repository.createRun({
      providerId: input.providerId,
      modelId: input.modelId,
      identityId: null,
      dimension: 'speed',
      runKind: 'speed',
      status: scored ? 'complete' : 'insufficient_evidence',
      evaluatorVersion: OVERALL_SCORE_POLICY.evaluatorVersion,
      rubricVersion: OVERALL_SCORE_POLICY.rubricVersion,
      testSetVersion: OVERALL_SCORE_POLICY.testSetVersion,
      testSetHash: null,
      methodologyVersion: OVERALL_SCORE_POLICY.methodologyVersion,
      region: OVERALL_SCORE_POLICY.region,
      independentRunKey: `${input.providerId}/${input.modelId}/speed/${startedAt}`,
      errorCode: scored ? null : 'no_successful_speed_samples',
      startedAt,
      finishedAt,
    });
    samples.forEach((sample, index) => repository.appendSample({
      runId,
      scenarioId: `speed-${String(index + 1).padStart(2, '0')}`,
      repetition: 1,
      outcome: sample.success ? 'passed' : 'provider_failure',
      weightedSuccesses: null,
      weightedCriteria: null,
      metrics: {
        ttftSeconds: sample.ttftSeconds,
        outputTokensPerSecond: sample.outputTokensPerSecond,
        endToEndSeconds: sample.endToEndSeconds,
        success: sample.success,
      },
      artifactRef: null,
      errorCode: sample.errorCode,
      recordedAt: finishedAt,
    }));
    if (scored) {
      repository.saveOfferDimension({
        providerId: input.providerId,
        modelId: input.modelId,
        dimension: 'speed',
        score: scored.score.score,
        rawRate: scored.score.rawRate,
        uncertainty: scored.score.uncertainty,
        confidence: scored.score.confidence,
        sampleCount: scored.score.sampleCount,
        status: 'scored',
        evidence: [`runtime:${input.providerId}/${input.modelId}`, `run:${runId}`, 'speed-fixture:512-output-tokens'],
        evaluatedAt: finishedAt,
        methodologyVersion: OVERALL_SCORE_POLICY.methodologyVersion,
      });
    }
  });

  return scored
    ? { status: 'complete', reason: null, samples }
    : { status: 'insufficient_evidence', reason: 'no_successful_speed_samples', samples };
}

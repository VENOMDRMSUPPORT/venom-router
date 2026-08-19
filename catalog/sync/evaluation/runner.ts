import type { Db } from '../../db/index.ts';
import { transaction } from '../../db/index.ts';
import { createEvaluationRepository } from './repository.ts';
import { runDimensionEvaluation, type DimensionEvaluationResult, type RuntimeScenario } from './runtime.ts';
import { OVERALL_SCORE_POLICY, type QualityDimension } from './score.ts';
import type { EvaluationTransport } from './transport.ts';

export interface PersistDimensionEvaluationInput {
  db: Db;
  providerId: string;
  modelId: string;
  identityId: string;
  dimension: QualityDimension;
  scenarios: RuntimeScenario[];
  transport: EvaluationTransport;
  credential: string | null;
  testSetHash: string;
  now: () => string;
}

export async function persistDimensionEvaluation(
  input: PersistDimensionEvaluationInput,
): Promise<DimensionEvaluationResult> {
  const startedAt = input.now();
  const result = await runDimensionEvaluation({
    providerId: input.providerId,
    modelId: input.modelId,
    dimension: input.dimension,
    scenarios: input.scenarios,
    transport: input.transport,
    credential: input.credential,
    now: input.now,
  });

  transaction(input.db, () => {
    const repository = createEvaluationRepository(input.db);
    const runId = repository.createRun({
      providerId: input.providerId,
      modelId: input.modelId,
      identityId: input.identityId,
      dimension: input.dimension,
      runKind: 'runtime',
      status: result.status === 'complete' ? 'complete' : 'insufficient_evidence',
      evaluatorVersion: OVERALL_SCORE_POLICY.evaluatorVersion,
      rubricVersion: OVERALL_SCORE_POLICY.rubricVersion,
      testSetVersion: OVERALL_SCORE_POLICY.testSetVersion,
      testSetHash: input.testSetHash,
      methodologyVersion: OVERALL_SCORE_POLICY.methodologyVersion,
      region: OVERALL_SCORE_POLICY.region,
      independentRunKey: `${input.providerId}/${input.modelId}/${input.dimension}/${startedAt}`,
      errorCode: result.status === 'complete' ? null : result.reason,
      startedAt,
      finishedAt: result.evaluatedAt,
    });
    for (const sample of result.samples) {
      repository.appendSample({
        runId,
        scenarioId: sample.scenarioId,
        repetition: sample.repetition,
        outcome: sample.outcome,
        weightedSuccesses: sample.weightedSuccesses,
        weightedCriteria: sample.weightedCriteria,
        metrics: null,
        artifactRef: `fixture:${input.testSetHash}#${sample.scenarioId}`,
        errorCode: sample.errorCode,
        recordedAt: result.evaluatedAt,
      });
    }
    if (result.status === 'complete') {
      repository.saveIdentityDimension({
        identityId: input.identityId,
        dimension: input.dimension,
        score: result.score.score,
        rawRate: result.score.rawRate,
        uncertainty: result.score.uncertainty,
        confidence: result.score.confidence,
        sampleCount: result.score.sampleCount,
        status: 'scored',
        rubricVersion: OVERALL_SCORE_POLICY.rubricVersion,
        testSetHash: input.testSetHash,
        evidence: [`runtime:${input.providerId}/${input.modelId}`, `run:${runId}`, `fixture:${input.testSetHash}`],
        evaluatedAt: result.evaluatedAt,
        methodologyVersion: OVERALL_SCORE_POLICY.methodologyVersion,
      });
    }
  });

  return result;
}

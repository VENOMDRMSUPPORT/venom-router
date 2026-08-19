import type { Db } from '../../db/index.ts';
import { transaction } from '../../db/index.ts';
import { createEvaluationRepository } from './repository.ts';
import { runDimensionEvaluation, type DimensionEvaluationResult, type RuntimeScenario } from './runtime.ts';
import { OVERALL_SCORE_POLICY, type QualityDimension } from './score.ts';
import { redactSecrets, type EvaluationTransport } from './transport.ts';

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
  /**
   * Start a new run instead of resuming an existing one.
   *
   * Resume reuses STORED GRADES, not stored responses, so after a grader repair
   * an inherited sample carries a verdict the current grader never produced.
   * A re-evaluation must therefore refuse to inherit. A later plain resume still
   * attaches to this fresh run, which is the newest resumable one.
   */
  fresh?: boolean;
}

export async function persistDimensionEvaluation(
  input: PersistDimensionEvaluationInput,
): Promise<DimensionEvaluationResult> {
  const startedAt = input.now();
  const repository = createEvaluationRepository(input.db);
  const existingRunId = input.fresh ? null : repository.findResumableRun({
    providerId: input.providerId,
    modelId: input.modelId,
    identityId: input.identityId,
    dimension: input.dimension,
    testSetHash: input.testSetHash,
  });
  const runId = existingRunId ?? transaction(input.db, () => repository.createRun({
    providerId: input.providerId,
    modelId: input.modelId,
    identityId: input.identityId,
    dimension: input.dimension,
    runKind: 'runtime',
    status: 'running',
    evaluatorVersion: OVERALL_SCORE_POLICY.evaluatorVersion,
    rubricVersion: OVERALL_SCORE_POLICY.rubricVersion,
    testSetVersion: OVERALL_SCORE_POLICY.testSetVersion,
    testSetHash: input.testSetHash,
    methodologyVersion: OVERALL_SCORE_POLICY.methodologyVersion,
    region: OVERALL_SCORE_POLICY.region,
    independentRunKey: `${input.providerId}/${input.modelId}/${input.dimension}/${startedAt}`,
    errorCode: null,
    startedAt,
    finishedAt: null,
  }));
  const existingSamples = repository.runSamples(runId).map((sample) => ({
    scenarioId: sample.scenarioId,
    repetition: sample.repetition,
    outcome: sample.outcome,
    weightedSuccesses: sample.weightedSuccesses,
    weightedCriteria: sample.weightedCriteria,
    errorCode: sample.errorCode,
  }));
  const result = await runDimensionEvaluation({
    providerId: input.providerId,
    modelId: input.modelId,
    dimension: input.dimension,
    scenarios: input.scenarios,
    transport: input.transport,
    credential: input.credential,
    now: input.now,
    existingSamples,
    onSample: (sample) => {
      repository.upsertSample({
        runId,
        scenarioId: sample.scenarioId,
        repetition: sample.repetition,
        outcome: sample.outcome,
        weightedSuccesses: sample.weightedSuccesses,
        weightedCriteria: sample.weightedCriteria,
        metrics: null,
        artifactRef: `fixture:${input.testSetHash}#${sample.scenarioId}`,
        response: sample.response === undefined
          ? undefined
          : redactSecrets(sample.response, [input.credential ?? '']),
        errorCode: sample.errorCode,
        recordedAt: input.now(),
      });
    },
  });

  transaction(input.db, () => {
    const repository = createEvaluationRepository(input.db);
    repository.updateRun(
      runId,
      result.status === 'complete' ? 'complete' : 'insufficient_evidence',
      result.status === 'complete' ? null : result.reason,
      result.evaluatedAt,
    );
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

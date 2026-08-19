import { OVERALL_SCORE_POLICY, scoreSpeed, smoothCriterionScore, type CriterionScore, type QualityDimension, type SpeedScore } from './score.ts';
import type { EvaluationTransport, TransportOutcome } from './transport.ts';

export interface RuntimeScenario {
  id: string;
  payload: unknown;
  grade(response: unknown): { weightedSuccesses: number; weightedCriteria: number };
}

export interface RuntimeSample {
  scenarioId: string;
  repetition: number;
  outcome: 'passed' | 'failed' | 'provider_failure' | 'evaluator_failure';
  weightedSuccesses: number | null;
  weightedCriteria: number | null;
  errorCode: string | null;
}

export interface DimensionEvaluationRequest {
  providerId: string;
  modelId: string;
  dimension: QualityDimension;
  scenarios: RuntimeScenario[];
  transport: EvaluationTransport;
  credential: string | null;
  now: () => string;
}

export type DimensionEvaluationResult =
  | { status: 'complete'; reason: null; score: CriterionScore; samples: RuntimeSample[]; evaluatedAt: string }
  | { status: 'insufficient_evidence'; reason: string; score: null; samples: RuntimeSample[]; evaluatedAt: string };

async function runPool<T>(tasks: (() => Promise<T>)[], concurrency: number): Promise<T[]> {
  const results = new Array<T>(tasks.length);
  let next = 0;
  const worker = async () => {
    while (true) {
      const index = next++;
      if (index >= tasks.length) return;
      results[index] = await tasks[index]();
    }
  };
  await Promise.all(Array.from({ length: Math.min(concurrency, tasks.length) }, worker));
  return results;
}

function outcomeSample(scenario: RuntimeScenario, repetition: number, outcome: TransportOutcome): RuntimeSample {
  if (outcome.kind === 'success') {
    const graded = scenario.grade(outcome.response.body);
    return {
      scenarioId: scenario.id, repetition,
      outcome: graded.weightedSuccesses >= graded.weightedCriteria ? 'passed' : 'failed',
      weightedSuccesses: graded.weightedSuccesses, weightedCriteria: graded.weightedCriteria, errorCode: null,
    };
  }
  return {
    scenarioId: scenario.id, repetition,
    outcome: outcome.kind === 'evaluator_failure' ? 'evaluator_failure'
      : outcome.kind === 'provider_failure' ? 'provider_failure' : 'failed',
    weightedSuccesses: outcome.kind === 'model_failure' ? 0 : null,
    weightedCriteria: outcome.kind === 'model_failure' ? 5 : null,
    errorCode: outcome.errorCode,
  };
}

export async function runDimensionEvaluation(request: DimensionEvaluationRequest): Promise<DimensionEvaluationResult> {
  const evaluatedAt = request.now();
  if (!request.credential) {
    return { status: 'insufficient_evidence', reason: 'missing_evaluation_credentials', score: null, samples: [], evaluatedAt };
  }
  if (request.scenarios.length !== OVERALL_SCORE_POLICY.scenarioCount) {
    return { status: 'insufficient_evidence', reason: 'invalid_scenario_count', score: null, samples: [], evaluatedAt };
  }
  for (let warmup = 0; warmup < OVERALL_SCORE_POLICY.warmupRequests; warmup++) {
    await request.transport(request.scenarios[warmup % request.scenarios.length].payload, request.credential);
  }
  const tasks: (() => Promise<RuntimeSample>)[] = [];
  for (const scenario of request.scenarios) {
    for (let repetition = 1; repetition <= OVERALL_SCORE_POLICY.repetitions; repetition++) {
      tasks.push(async () => outcomeSample(
        scenario, repetition, await request.transport(scenario.payload, request.credential!),
      ));
    }
  }
  const samples = await runPool(tasks, OVERALL_SCORE_POLICY.providerConcurrency);
  const evaluatorFailures = samples.filter((sample) => sample.outcome === 'evaluator_failure' || sample.outcome === 'provider_failure');
  if (evaluatorFailures.length > 0) {
    return { status: 'insufficient_evidence', reason: 'incomplete_valid_scenarios', score: null, samples, evaluatedAt };
  }
  const successes = samples.reduce((sum, sample) => sum + (sample.weightedSuccesses ?? 0), 0);
  const criteria = samples.reduce((sum, sample) => sum + (sample.weightedCriteria ?? 0), 0);
  return { status: 'complete', reason: null, score: smoothCriterionScore(successes, criteria), samples, evaluatedAt };
}

export interface SpeedRequestSample {
  ttftSeconds: number | null;
  outputTokensPerSecond: number | null;
  endToEndSeconds: number | null;
  success: boolean;
}

const percentile = (values: number[], fraction: number): number => {
  const sorted = [...values].sort((a, b) => a - b);
  if (!sorted.length) throw new Error('speed metric requires one successful request');
  const position = (sorted.length - 1) * fraction;
  const lower = Math.floor(position);
  const upper = Math.ceil(position);
  if (lower === upper) return sorted[lower];
  return sorted[lower] + (sorted[upper] - sorted[lower]) * (position - lower);
};

const nearestRankPercentile = (values: number[], fraction: number): number => {
  const sorted = [...values].sort((a, b) => a - b);
  if (!sorted.length) throw new Error('speed metric requires one successful request');
  return sorted[Math.max(0, Math.ceil(fraction * sorted.length) - 1)];
};

export function runSpeedEvaluation(samples: SpeedRequestSample[]): { metrics: {
  ttftMedianSeconds: number;
  outputTokensPerSecondMedian: number;
  endToEndP95Seconds: number;
  successRate: number;
}; score: SpeedScore } {
  if (!samples.length) throw new Error('speed evaluation requires samples');
  const successful = samples.filter((sample) => sample.success && sample.ttftSeconds !== null
    && sample.outputTokensPerSecond !== null && sample.endToEndSeconds !== null);
  const metrics = {
    ttftMedianSeconds: percentile(successful.map((sample) => sample.ttftSeconds!), 0.5),
    outputTokensPerSecondMedian: percentile(successful.map((sample) => sample.outputTokensPerSecond!), 0.5),
    endToEndP95Seconds: nearestRankPercentile(successful.map((sample) => sample.endToEndSeconds!), 0.95),
    successRate: successful.length / samples.length,
  };
  return { metrics, score: scoreSpeed({ ...metrics, retainedRequests: samples.length }) };
}

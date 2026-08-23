import { OVERALL_SCORE_POLICY, scoreSpeed, smoothCriterionScore, type CriterionScore, type QualityDimension, type SpeedScore } from './score.ts';
import { ranOutOfRoom } from './fixtures.ts';
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
  /**
   * What the provider actually answered, when it answered at all.
   *
   * Kept beside the verdict so a repaired grader can be replayed against the
   * stored corpus. Absent on samples reloaded from the database, which predate
   * retention or were resumed.
   */
  response?: unknown;
}

export interface DimensionEvaluationRequest {
  providerId: string;
  modelId: string;
  dimension: QualityDimension;
  scenarios: RuntimeScenario[];
  transport: EvaluationTransport;
  credential: string | null;
  now: () => string;
  existingSamples?: RuntimeSample[];
  onSample?: (sample: RuntimeSample) => void | Promise<void>;
  /**
   * Asked between samples, so a stop costs at most the requests already in
   * flight. Checking only between dimensions would let a cancelled run keep
   * spending for another sixty requests, which is the whole reason a stop
   * button exists.
   */
  shouldStop?: () => boolean;
}

export type DimensionEvaluationResult =
  | { status: 'complete'; reason: null; score: CriterionScore; samples: RuntimeSample[]; evaluatedAt: string }
  | { status: 'insufficient_evidence'; reason: string; score: null; samples: RuntimeSample[]; evaluatedAt: string };

export async function runPool<T>(
  tasks: (() => Promise<T>)[],
  concurrency: number,
  shouldStop?: () => boolean,
): Promise<T[]> {
  const results = new Array<T | undefined>(tasks.length);
  let next = 0;
  const worker = async () => {
    while (true) {
      if (shouldStop?.()) return;
      const index = next++;
      if (index >= tasks.length) return;
      results[index] = await tasks[index]();
    }
  };
  await Promise.all(Array.from({ length: Math.min(concurrency, tasks.length) }, worker));
  // A stop leaves holes where tasks were never started; the caller must see the
  // samples that were actually paid for and nothing else.
  return results.filter((entry): entry is T => entry !== undefined);
}

function outcomeSample(scenario: RuntimeScenario, repetition: number, outcome: TransportOutcome): RuntimeSample {
  if (outcome.kind === 'success') {
    const graded = scenario.grade(outcome.response.body);
    const fullMarks = graded.weightedSuccesses >= graded.weightedCriteria;
    // A response cut off mid-answer is evidence only if it scored full marks.
    //
    // The transport retries when a cut-off response carries no answer at all,
    // but it cannot see a PARTIAL one: `gpt-oss:120b` began emitting its
    // long-context JSON, was cut at the cap mid-object, and the fragment was
    // graded 1 of 5 as though the model had answered wrongly. Nothing in that
    // fragment distinguishes a wrong answer from an unfinished one, so it is
    // recorded as a provider failure — retained, not final, and re-run rather
    // than published. Full marks despite the cut mean the answer was all there.
    if (!fullMarks && ranOutOfRoom(outcome.response.body)) {
      return {
        scenarioId: scenario.id, repetition, outcome: 'provider_failure',
        weightedSuccesses: null, weightedCriteria: null, errorCode: 'answer_truncated',
        response: outcome.response.body,
      };
    }
    return {
      scenarioId: scenario.id, repetition,
      outcome: fullMarks ? 'passed' : 'failed',
      weightedSuccesses: graded.weightedSuccesses, weightedCriteria: graded.weightedCriteria, errorCode: null,
      response: outcome.response.body,
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

function sampleKey(sample: Pick<RuntimeSample, 'scenarioId' | 'repetition'>): string {
  return `${sample.scenarioId}:${sample.repetition}`;
}

function isFinalSample(sample: RuntimeSample): boolean {
  return sample.outcome === 'passed' || sample.outcome === 'failed';
}

// A temporary gateway/rate-limit failure must not stop the whole dimension.
// The sample is retained as pending and will be retried by the next resumable
// run. Permanent evaluator failures still stop immediately because continuing
// would only buy requests that cannot be graded correctly.
function stopsDimension(outcome: TransportOutcome): boolean {
  if (outcome.kind !== 'provider_failure') return outcome.kind === 'evaluator_failure';
  return ![
    'retry_after_too_long',
    // Both proxy-pool failures belong here. `proxy_list_unavailable` is one
    // fetch of a third-party list URL failing, which is if anything MORE
    // transient than an exhausted pool — treating it as permanent stopped a
    // whole dimension over a blip on a host that is not even the provider's.
    'proxy_pool_exhausted',
    'proxy_list_unavailable',
    'http_503',
    'network_transient',
    'evaluation_request_timeout',
  ].includes(outcome.errorCode);
}

function stopsSample(sample: RuntimeSample): boolean {
  if (sample.outcome === 'evaluator_failure') return true;
  if (sample.outcome !== 'provider_failure') return false;
  return stopsDimension({
    kind: 'provider_failure', status: null, attempts: 1,
    errorCode: sample.errorCode ?? 'provider_failure',
  });
}

export async function runDimensionEvaluation(request: DimensionEvaluationRequest): Promise<DimensionEvaluationResult> {
  const evaluatedAt = request.now();
  if (!request.credential) {
    return { status: 'insufficient_evidence', reason: 'missing_evaluation_credentials', score: null, samples: [], evaluatedAt };
  }
  if (request.scenarios.length !== OVERALL_SCORE_POLICY.scenarioCount) {
    return { status: 'insufficient_evidence', reason: 'invalid_scenario_count', score: null, samples: [], evaluatedAt };
  }
  const prior = new Map((request.existingSamples ?? [])
    .filter(isFinalSample)
    .map((sample) => [sampleKey(sample), sample]));
  // Every sample must succeed for a dimension to be scored, so the first
  // unrecoverable failure already decides the outcome. Everything issued after
  // it is paid for and thrown away.
  let doomed = false;
  const halt = () => doomed || (request.shouldStop?.() ?? false);
  const tasks: (() => Promise<RuntimeSample>)[] = [];
  for (const scenario of request.scenarios) {
    for (let repetition = 1; repetition <= OVERALL_SCORE_POLICY.repetitions; repetition++) {
      const key = sampleKey({ scenarioId: scenario.id, repetition });
      if (prior.has(key)) continue;
      tasks.push(async () => {
        const sample = outcomeSample(
          scenario, repetition, await request.transport(scenario.payload, request.credential!),
        );
        if (stopsSample(sample)) doomed = true;
        await request.onSample?.(sample);
        return sample;
      });
    }
  }
  if (tasks.length > 0) {
    for (let warmup = 0; warmup < OVERALL_SCORE_POLICY.warmupRequests; warmup++) {
      if (halt()) break;
      await request.transport(request.scenarios[warmup % request.scenarios.length].payload, request.credential);
    }
  }
  const fresh = await runPool(tasks, OVERALL_SCORE_POLICY.qualityProviderConcurrency, halt);
  const samples = [...prior.values(), ...fresh]
    .sort((left, right) => left.scenarioId.localeCompare(right.scenarioId) || left.repetition - right.repetition);
  // Checked before anything else: a stopped run is incomplete by construction,
  // and scoring the fraction it managed would publish a number nobody asked for.
  if (request.shouldStop?.()) {
    return { status: 'insufficient_evidence', reason: 'stopped', score: null, samples, evaluatedAt };
  }
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

import type { Db } from '../db/index.ts';
import { runSpeedEvaluation, type SpeedRequestSample } from '../sync/evaluation/runtime.ts';

export type PerformanceStatus = 'measured' | 'not_measured';

export interface ModelPerformance {
  status: PerformanceStatus;
  runId: number | null;
  evaluatedAt: string | null;
  sampleCount: number;
  successfulSamples: number;
  ttftMedianSeconds: number | null;
  outputTokensPerSecondMedian: number | null;
  endToEndP95Seconds: number | null;
  successRate: number | null;
  speedScore: number | null;
}

type RunRow = {
  id: number;
  provider_id: string;
  model_id: string;
  status: string;
  finished_at: string | null;
  started_at: string;
};

type SampleRow = {
  run_id: number;
  metrics_json: string | null;
};

type ScoreRow = {
  provider_id: string;
  model_id: string;
  score: number | null;
};

const emptyPerformance = (): ModelPerformance => ({
  status: 'not_measured',
  runId: null,
  evaluatedAt: null,
  sampleCount: 0,
  successfulSamples: 0,
  ttftMedianSeconds: null,
  outputTokensPerSecondMedian: null,
  endToEndP95Seconds: null,
  successRate: null,
  speedScore: null,
});

const keyOf = (providerId: string, modelId: string) => `${providerId}\u0000${modelId}`;

function parsedSample(value: string | null): SpeedRequestSample | null {
  if (!value) return null;
  try {
    const parsed = JSON.parse(value) as Partial<SpeedRequestSample>;
    if (typeof parsed.success !== 'boolean') return null;
    return {
      success: parsed.success,
      ttftSeconds: typeof parsed.ttftSeconds === 'number' ? parsed.ttftSeconds : null,
      outputTokensPerSecond: typeof parsed.outputTokensPerSecond === 'number' ? parsed.outputTokensPerSecond : null,
      endToEndSeconds: typeof parsed.endToEndSeconds === 'number' ? parsed.endToEndSeconds : null,
    };
  } catch {
    return null;
  }
}

/**
 * Reads the latest server-owned speed run per model. The client receives the
 * aggregates, never the raw samples and never calculates these metrics itself.
 */
export function loadPerformanceByModel(db: Db): Map<string, ModelPerformance> {
  const result = new Map<string, ModelPerformance>();
  const runs = db.prepare(`SELECT id, provider_id, model_id, status, finished_at, started_at
    FROM evaluation_runs WHERE run_kind = 'speed'
    ORDER BY started_at DESC, id DESC`).all() as unknown as RunRow[];
  const latestRunByModel = new Map<string, RunRow>();
  for (const run of runs) {
    const key = keyOf(run.provider_id, run.model_id);
    if (!latestRunByModel.has(key)) latestRunByModel.set(key, run);
  }

  const runIds = [...latestRunByModel.values()].map((run) => run.id);
  if (!runIds.length) return result;
  const placeholders = runIds.map(() => '?').join(',');
  const samplesByRun = new Map<number, SpeedRequestSample[]>();
  const samples = db.prepare(`SELECT run_id, metrics_json FROM evaluation_samples WHERE run_id IN (${placeholders}) ORDER BY run_id, scenario_id, repetition`).all(...runIds) as unknown as SampleRow[];
  for (const sample of samples) {
    const parsed = parsedSample(sample.metrics_json);
    if (!parsed) continue;
    const list = samplesByRun.get(sample.run_id) ?? [];
    list.push(parsed);
    samplesByRun.set(sample.run_id, list);
  }

  const speedScores = new Map<string, number | null>();
  const scoreRows = db.prepare(`SELECT provider_id, model_id, score
    FROM provider_model_scores WHERE dimension = 'speed'`).all() as unknown as ScoreRow[];
  for (const row of scoreRows) speedScores.set(keyOf(row.provider_id, row.model_id), row.score);

  for (const [key, run] of latestRunByModel) {
    const modelSamples = samplesByRun.get(run.id) ?? [];
    const successful = modelSamples.filter((sample) => sample.success && sample.ttftSeconds !== null
      && sample.outputTokensPerSecond !== null && sample.endToEndSeconds !== null);
    if (!successful.length) {
      result.set(key, { ...emptyPerformance(), runId: run.id, evaluatedAt: run.finished_at ?? run.started_at });
      continue;
    }
    const metrics = runSpeedEvaluation(modelSamples).metrics;
    result.set(key, {
      status: run.status === 'complete' ? 'measured' : 'not_measured',
      runId: run.id,
      evaluatedAt: run.finished_at ?? run.started_at,
      sampleCount: modelSamples.length,
      successfulSamples: successful.length,
      ttftMedianSeconds: metrics.ttftMedianSeconds,
      outputTokensPerSecondMedian: metrics.outputTokensPerSecondMedian,
      endToEndP95Seconds: metrics.endToEndP95Seconds,
      successRate: metrics.successRate,
      speedScore: speedScores.get(key) ?? null,
    });
  }
  return result;
}

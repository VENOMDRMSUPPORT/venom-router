import type { ApiModel, ModelPerformance } from './client';

export interface PerformanceRow {
  model: ApiModel;
  performance: ModelPerformance;
}

export interface PerformanceSummary {
  totalModels: number;
  measuredModels: number;
  sampleCount: number;
  successfulSamples: number;
  medianTtftSeconds: number | null;
  medianOutputTokensPerSecond: number | null;
  medianEndToEndP95Seconds: number | null;
  averageSuccessRate: number | null;
  bestThroughput: PerformanceRow | null;
  fastestTtft: PerformanceRow | null;
  highestSuccessRate: PerformanceRow | null;
}

const measured = (model: ApiModel): model is ApiModel & { performance: ModelPerformance } => {
  const performance = model.performance;
  if (!performance) return false;
  return performance.status === 'measured'
    && performance.sampleCount > 0
    && performance.ttftMedianSeconds !== null
    && performance.outputTokensPerSecondMedian !== null
    && performance.endToEndP95Seconds !== null
    && performance.successRate !== null;
};

export function performanceRows(models: ApiModel[]): PerformanceRow[] {
  return models.filter(measured)
    .map((model) => ({ model, performance: model.performance }))
    .sort((left, right) => {
      const throughput = right.performance.outputTokensPerSecondMedian! - left.performance.outputTokensPerSecondMedian!;
      return throughput || left.model.displayName.localeCompare(right.model.displayName);
    });
}

function median(values: number[]): number | null {
  if (!values.length) return null;
  const sorted = [...values].sort((left, right) => left - right);
  const middle = Math.floor(sorted.length / 2);
  return sorted.length % 2 === 0 ? (sorted[middle - 1] + sorted[middle]) / 2 : sorted[middle];
}

export function summarizePerformance(models: ApiModel[]): PerformanceSummary {
  const rows = performanceRows(models);
  const best = (compare: (left: PerformanceRow, right: PerformanceRow) => number): PerformanceRow | null =>
    rows.reduce<PerformanceRow | null>((winner, row) => !winner || compare(row, winner) < 0 ? row : winner, null);
  return {
    totalModels: models.length,
    measuredModels: rows.length,
    sampleCount: rows.reduce((sum, row) => sum + row.performance.sampleCount, 0),
    successfulSamples: rows.reduce((sum, row) => sum + row.performance.successfulSamples, 0),
    medianTtftSeconds: median(rows.map((row) => row.performance.ttftMedianSeconds!)),
    medianOutputTokensPerSecond: median(rows.map((row) => row.performance.outputTokensPerSecondMedian!)),
    medianEndToEndP95Seconds: median(rows.map((row) => row.performance.endToEndP95Seconds!)),
    averageSuccessRate: rows.length ? rows.reduce((sum, row) => sum + row.performance.successRate!, 0) / rows.length : null,
    bestThroughput: best((left, right) => right.performance.outputTokensPerSecondMedian! - left.performance.outputTokensPerSecondMedian!),
    fastestTtft: best((left, right) => left.performance.ttftMedianSeconds! - right.performance.ttftMedianSeconds!),
    highestSuccessRate: best((left, right) => right.performance.successRate! - left.performance.successRate!),
  };
}

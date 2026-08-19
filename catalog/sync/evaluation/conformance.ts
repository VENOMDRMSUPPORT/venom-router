export interface ConfidenceInterval95 {
  lower: number;
  upper: number;
}

export interface ConformanceRun {
  independentRunKey: string;
  identityScore: number;
  offerScore: number;
  identityInterval95: ConfidenceInterval95;
  offerInterval95: ConfidenceInterval95;
  contractBreak: boolean;
  runId: number;
}

export interface ConformanceDecision {
  state: 'conformant' | 'provisional' | 'override';
  reason: 'contract_break' | 'quality_divergence' | null;
  runIds: number[];
}

const intervalsOverlap = (a: ConfidenceInterval95, b: ConfidenceInterval95): boolean =>
  a.lower <= b.upper && b.lower <= a.upper;

const divergent = (run: ConformanceRun): boolean =>
  Math.abs(run.identityScore - run.offerScore) > 8
  && !intervalsOverlap(run.identityInterval95, run.offerInterval95);

export function assessConformance(runs: ConformanceRun[]): ConformanceDecision {
  const contract = runs.find((run) => run.contractBreak);
  if (contract) return { state: 'override', reason: 'contract_break', runIds: [contract.runId] };
  const distinct = new Map<string, ConformanceRun>();
  for (const run of runs.filter(divergent)) distinct.set(run.independentRunKey, run);
  const divergentRuns = [...distinct.values()];
  if (divergentRuns.length >= 2) {
    return { state: 'override', reason: 'quality_divergence', runIds: divergentRuns.map((run) => run.runId) };
  }
  if (divergentRuns.length === 1) {
    return { state: 'provisional', reason: 'quality_divergence', runIds: [divergentRuns[0].runId] };
  }
  return { state: 'conformant', reason: null, runIds: runs.map((run) => run.runId) };
}

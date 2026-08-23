/**
 * The service's evaluation queue.
 *
 * Shaped after SyncRunner deliberately: the service should own one concurrency
 * pattern, not two. One worker, a FIFO queue held in memory, and a cooperative
 * stop.
 *
 * The queue is not persisted. A restart mid-job loses the queue but no
 * evidence: samples are written as they complete, so the next job for that
 * model resumes the run. A persistent queue would add a schema and a recovery
 * path for a case that costs one click to redo.
 */
import type { Db } from '../db/index.ts';
import { planEvaluation, type EvaluationPlan } from '../sync/evaluation/plan.ts';
import type { QualityDimension } from '../sync/evaluation/score.ts';

export interface EvaluationJobExecutor {
  runDimension(input: {
    providerId: string;
    modelId: string;
    identityId: string;
    dimension: QualityDimension;
    onSample: (completed: number, total: number) => void;
    /** Asked between samples: a stop must not cost another full dimension. */
    shouldStop: () => boolean;
  }): Promise<{ status: 'complete' | 'insufficient_evidence'; score: number | null }>;
  runSpeed(input: {
    providerId: string;
    modelId: string;
    onSample: (completed: number, total: number) => void;
    shouldStop: () => boolean;
  }): Promise<{ status: 'complete' | 'insufficient_evidence' }>;
  recalculate(): void;
}

export interface EvaluationCurrent {
  providerId: string;
  modelId: string;
  identityId: string;
  /** The dimension in flight, or null between dimensions. */
  dimension: QualityDimension | 'speed' | null;
  samplesCompleted: number;
  samplesTotal: number;
  dimensionsCompleted: Array<{ dimension: string; score: number | null; status: string }>;
  dimensionsRemaining: string[];
  startedAt: string;
}

export interface EvaluationState {
  state: 'idle' | 'running' | 'stopping';
  current: EvaluationCurrent | null;
  queue: Array<{ providerId: string; modelId: string }>;
  recent: Array<{ providerId: string; modelId: string; finishedAt: string; outcome: string }>;
}

export type EnqueueOutcome =
  | { accepted: true; position: number; plan: EvaluationPlan }
  | { accepted: false; reason: 'already_queued' | 'blocked'; plan: EvaluationPlan };

const RECENT_LIMIT = 10;

export class EvaluationRunner {
  private readonly db: Db;
  private readonly executor: EvaluationJobExecutor;
  private readonly testSetHash: string;
  private readonly clock: () => Date;
  /** Injected so tests never read the ambient environment. */
  private readonly hasCredential?: (providerId: string) => boolean;
  private queue: Array<{ providerId: string; modelId: string }> = [];
  private current: EvaluationCurrent | null = null;
  private recent: EvaluationState['recent'] = [];
  private stopping = false;
  private working: Promise<void> | null = null;

  constructor(config: {
    db: Db;
    executor: EvaluationJobExecutor;
    testSetHash: string;
    now?: () => Date;
    hasCredential?: (providerId: string) => boolean;
  }) {
    this.db = config.db;
    this.executor = config.executor;
    this.testSetHash = config.testSetHash;
    this.clock = config.now ?? (() => new Date());
    this.hasCredential = config.hasCredential;
  }

  private plan(providerId: string, modelId: string): EvaluationPlan {
    return planEvaluation(this.db, {
      providerId,
      modelId,
      testSetHash: this.testSetHash,
      hasCredential: this.hasCredential,
    });
  }

  get state(): EvaluationState {
    return {
      state: this.stopping ? 'stopping' : this.current ? 'running' : 'idle',
      current: this.current,
      queue: [...this.queue],
      recent: [...this.recent],
    };
  }

  /** Resolves when the queue drains. Tests await it; the service does not. */
  get idle(): Promise<void> {
    return this.working ?? Promise.resolve();
  }

  /**
   * A blocked plan is refused here rather than queued, so an offer that cannot
   * be evaluated never reaches a provider and the caller gets the typed reason.
   */
  enqueue(providerId: string, modelId: string): EnqueueOutcome {
    const plan = this.plan(providerId, modelId);
    if (plan.blocked) return { accepted: false, reason: 'blocked', plan };

    const queued = this.queue.some((job) => job.providerId === providerId && job.modelId === modelId);
    const active = this.current?.providerId === providerId && this.current?.modelId === modelId;
    if (queued || active) return { accepted: false, reason: 'already_queued', plan };

    this.queue.push({ providerId, modelId });
    // Position counts the job in flight too: the first caller is told 1 while it
    // runs, not 0. `drain` shifts the queue synchronously, so queue length alone
    // would report every caller as first.
    const position = this.queue.length + (this.current ? 1 : 0);
    if (!this.working) {
      // The catch is the point, not decoration. Without it a throw anywhere
      // below leaves this promise rejected with nobody holding it, and Node ends
      // the process — so one evaluation sample takes down `/v1/models` and
      // `/v1/health` with it. That is exactly what happened on 2026-08-23, and
      // it is why `sync-runner.ts` puts a `.catch` on every background promise.
      this.working = this.drain()
        .catch((error: unknown) => { console.error('[evaluations] worker stopped unexpectedly:', error); })
        .finally(() => {
          this.working = null;
        });
    }
    return { accepted: true, position, plan };
  }

  stop(): { stopped: boolean; cleared: number } {
    const cleared = this.queue.length;
    this.queue = [];
    const stopped = this.current !== null;
    if (stopped) this.stopping = true;
    return { stopped, cleared };
  }

  private async drain(): Promise<void> {
    while (this.queue.length > 0) {
      const job = this.queue.shift()!;
      // One job that throws must not cancel the twenty behind it, the same way
      // `sync-runner.ts` catches per provider rather than per pass. The failure
      // is recorded under this offer so it is visible in `recent`, and the
      // worker moves on — a queue that dies on its first surprise is a queue
      // that has to be refilled by hand.
      try {
        await this.runJob(job.providerId, job.modelId);
      } catch (error) {
        console.error(`[evaluations] ${job.providerId}/${job.modelId} threw:`, error);
        this.remember(job.providerId, job.modelId, `error: ${error instanceof Error ? error.message : String(error)}`);
      }
      if (this.stopping) {
        // A stop cancels the pipeline, including anything enqueued during the
        // window between the request and the worker noticing it. Breaking
        // without clearing strands those jobs: the worker exits and nothing
        // starts another one until an unrelated enqueue happens to arrive.
        this.queue = [];
        break;
      }
    }
    this.stopping = false;
    this.current = null;
  }

  private async runJob(providerId: string, modelId: string): Promise<void> {
    // Re-planned at the moment it starts: a sibling offer sharing this identity
    // may have been scored while this job waited in the queue.
    const plan = this.plan(providerId, modelId);
    if (plan.blocked) {
      this.remember(providerId, modelId, plan.blocked);
      return;
    }

    const current: EvaluationCurrent = {
      providerId,
      modelId,
      identityId: plan.identityId!,
      dimension: null,
      samplesCompleted: 0,
      samplesTotal: 0,
      dimensionsCompleted: [],
      dimensionsRemaining: [...plan.dimensions, ...(plan.speed === 'missing' ? ['speed'] : [])],
      startedAt: this.clock().toISOString(),
    };
    this.current = current;

    for (const dimension of plan.dimensions) {
      if (this.stopping) {
        this.remember(providerId, modelId, 'stopped');
        return;
      }
      current.dimension = dimension;
      current.samplesCompleted = 0;
      current.samplesTotal = 0;
      const result = await this.executor.runDimension({
        providerId,
        modelId,
        identityId: plan.identityId!,
        dimension,
        onSample: (completed, total) => {
          current.samplesCompleted = completed;
          current.samplesTotal = total;
        },
        shouldStop: () => this.stopping,
      });
      current.dimensionsCompleted.push({ dimension, score: result.score, status: result.status });
      current.dimensionsRemaining = current.dimensionsRemaining.filter((entry) => entry !== dimension);
    }

    if (this.stopping) {
      this.remember(providerId, modelId, 'stopped');
      return;
    }

    if (plan.speed === 'missing') {
      // Last, and alone. Speed measures time-to-first-token and throughput, so
      // it must never share the connection with quality traffic.
      current.dimension = 'speed';
      current.samplesCompleted = 0;
      current.samplesTotal = 0;
      const result = await this.executor.runSpeed({
        providerId,
        modelId,
        onSample: (completed, total) => {
          current.samplesCompleted = completed;
          current.samplesTotal = total;
        },
        shouldStop: () => this.stopping,
      });
      current.dimensionsCompleted.push({ dimension: 'speed', score: null, status: result.status });
      current.dimensionsRemaining = current.dimensionsRemaining.filter((entry) => entry !== 'speed');
    }

    this.executor.recalculate();
    // "complete" means the job ran to the end, which is not the same as the
    // evidence being complete. A dimension that came back short is named, so a
    // finished job never implies a scored one.
    const short = current.dimensionsCompleted.filter((entry) => entry.status !== 'complete');
    this.remember(
      providerId,
      modelId,
      short.length === 0 ? 'complete' : `incomplete: ${short.map((entry) => entry.dimension).join(', ')}`,
    );
  }

  private remember(providerId: string, modelId: string, outcome: string): void {
    this.recent.unshift({ providerId, modelId, finishedAt: this.clock().toISOString(), outcome });
    this.recent = this.recent.slice(0, RECENT_LIMIT);
    this.current = null;
  }
}

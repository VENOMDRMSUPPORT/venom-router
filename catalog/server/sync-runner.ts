/**
 * Sync orchestration for the service: one runner, one lock, one schedule.
 *
 * The lock exists because the scheduler and `POST /v1/sync` can fire at the same
 * moment. Two concurrent runs would interleave their transactions and, worse,
 * both compute a delta against the same "before" state — so the second would see
 * the first's additions as unexplained and could trip the removal gate.
 */

import type { Db } from '../db/index.ts';
import { createFetchJson } from '../sync/http.ts';
import { syncProvider, type RunResult } from '../sync/engine.ts';
import { ADAPTERS, BILLING } from '../sync/providers/index.ts';
import { loadSpecs } from '../sync/sources/models-dev.ts';
import { loadBenchmarks } from '../sync/sources/openrouter.ts';
import { scoreAll, type ScoringSummary } from '../sync/score/pipeline.ts';
import { enrich, canonicalFromBenchmarks } from '../sync/enrich/enrich.ts';
import type { ScoreProfile } from '../sync/score/venom-score.ts';

export interface SyncOutcome {
  startedAt: string;
  finishedAt: string;
  providers: RunResult[];
  scoring: ScoringSummary | null;
  /** Set when the shared sources could not be fetched; nothing was written. */
  aborted?: string;
}

export interface RunnerConfig {
  db: Db;
  profile: ScoreProfile;
  methodologyVersion: string;
  identityOverlay: Record<string, string>;
  onSnapshot?: (db: Db) => void;
}

export class SyncRunner {
  readonly config: RunnerConfig;
  private running = false;
  private last: SyncOutcome | null = null;
  private startedAt: string | null = null;

  constructor(config: RunnerConfig) {
    this.config = config;
  }

  get isRunning(): boolean {
    return this.running;
  }

  get lastOutcome(): SyncOutcome | null {
    return this.last;
  }

  get currentRunStartedAt(): string | null {
    return this.startedAt;
  }

  /**
   * Run a full sync. Returns `null` immediately if one is already in flight —
   * the caller reports 409 rather than queueing, because a second run started
   * seconds later would fetch the same upstream state anyway.
   */
  async run(): Promise<SyncOutcome | null> {
    if (this.running) return null;
    this.running = true;
    const startedAt = new Date().toISOString();
    this.startedAt = startedAt;

    try {
      const { db, profile, methodologyVersion, identityOverlay } = this.config;
      const fetchJson = createFetchJson();
      const sourceFetchedAt = new Date().toISOString();

      let specs, benchmarks;
      try {
        [specs, benchmarks] = await Promise.all([loadSpecs(fetchJson), loadBenchmarks(fetchJson)]);
      } catch (err) {
        // The shared sources are the specs and the benchmarks. Without them a
        // run would rewrite every row with nulls, so it does not start at all
        // and the previous catalog stands untouched.
        const outcome: SyncOutcome = {
          startedAt, finishedAt: new Date().toISOString(), providers: [], scoring: null,
          aborted: err instanceof Error ? err.message : String(err),
        };
        this.last = outcome;
        return outcome;
      }

      const providers: RunResult[] = [];
      for (const adapter of ADAPTERS) {
        providers.push(
          await syncProvider(adapter, {
            db, fetchJson, now: () => new Date().toISOString(), lookupSpec: specs.lookup,
          }),
        );
      }

      // Facts first, then the score derived from them.
      enrich({
        db, canonical: canonicalFromBenchmarks(benchmarks), overlay: identityOverlay,
        billing: BILLING, intrinsic: specs.intrinsic, now: () => new Date().toISOString(),
      });

      const scoring = scoreAll({
        db, benchmarks, overlay: identityOverlay, profile, methodologyVersion,
        sourceFetchedAt, now: () => new Date().toISOString(),
      });

      this.config.onSnapshot?.(db);
      const outcome: SyncOutcome = { startedAt, finishedAt: new Date().toISOString(), providers, scoring };
      this.last = outcome;
      return outcome;
    } finally {
      this.running = false;
      this.startedAt = null;
    }
  }
}

export interface SchedulerHandle {
  stop(): void;
  readonly intervalMs: number;
  nextRunAt(): string;
}

/** Six hours: often enough to catch a same-day launch, rare enough to be polite. */
export const SCHEDULE_INTERVAL_MS = 6 * 60 * 60 * 1000;

export function startScheduler(
  runner: SyncRunner,
  { intervalMs = SCHEDULE_INTERVAL_MS, runOnStart = false }: { intervalMs?: number; runOnStart?: boolean } = {},
): SchedulerHandle {
  let nextAt = Date.now() + intervalMs;
  const timer = setInterval(() => {
    nextAt = Date.now() + intervalMs;
    // A skipped tick is correct behaviour, not an error: the lock means a long
    // run simply absorbs the next slot.
    void runner.run().catch((err) => console.error('[scheduler] sync failed:', err));
  }, intervalMs);
  timer.unref?.();
  if (runOnStart) void runner.run().catch((err) => console.error('[scheduler] initial sync failed:', err));
  return {
    stop: () => clearInterval(timer),
    intervalMs,
    nextRunAt: () => new Date(nextAt).toISOString(),
  };
}

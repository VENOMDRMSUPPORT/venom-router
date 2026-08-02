// Shared async-job helpers for the Providers page (09 §3.12): a 202 is an
// ACCEPTANCE, not a result, so every trigger polls GET /jobs/{job_id} to a
// terminal status before claiming anything happened.

import { getJob, type JobRead } from "../api/controlClient";

const TERMINAL: ReadonlySet<JobRead["status"]> = new Set(["completed", "failed", "expired"]);

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export interface PollOptions {
  intervalMs?: number;
  /** Upper bound on GET /jobs polls — a stuck job returns its last
   * NON-terminal read rather than spinning forever; the caller renders
   * that status honestly instead of assuming completion. */
  maxAttempts?: number;
}

/** Polls one job to a terminal status (completed/failed/expired). The
 * first read happens immediately, so an already-terminal job costs one
 * request and no timer. */
export async function pollJobToTerminal(jobId: string, options: PollOptions = {}): Promise<JobRead> {
  const { intervalMs = 1000, maxAttempts = 120 } = options;
  let job = await getJob(jobId);
  for (let attempt = 1; attempt < maxAttempts && !TERMINAL.has(job.status); attempt++) {
    await sleep(intervalMs);
    job = await getJob(jobId);
  }
  return job;
}

/**
 * Runs `worker` over `items` with at most `limit` in flight (the Model
 * Test Report's "Test All"). Failures are captured per item, never thrown
 * mid-run — one failed probe must not strand the remaining models
 * untested. `onSettled` reports progress (settled count so far).
 */
export async function runWithConcurrency<T, R>(
  items: readonly T[],
  limit: number,
  worker: (item: T) => Promise<R>,
  onSettled?: (settledCount: number) => void,
): Promise<PromiseSettledResult<R>[]> {
  const results: PromiseSettledResult<R>[] = new Array(items.length);
  let next = 0;
  let settled = 0;

  async function lane(): Promise<void> {
    for (;;) {
      const index = next++;
      if (index >= items.length) return;
      try {
        results[index] = { status: "fulfilled", value: await worker(items[index]) };
      } catch (reason) {
        results[index] = { status: "rejected", reason };
      }
      settled++;
      onSettled?.(settled);
    }
  }

  const lanes = Array.from({ length: Math.max(1, Math.min(limit, items.length)) }, lane);
  await Promise.all(lanes);
  return results;
}

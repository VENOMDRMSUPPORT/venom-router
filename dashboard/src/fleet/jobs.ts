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

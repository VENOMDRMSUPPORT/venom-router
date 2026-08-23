#!/usr/bin/env node
/**
 * Re-score every dimension whose responses were retained, using today's grader.
 *
 * A grader repair leaves every already-scored dimension carrying a verdict the
 * current grader would not give. Running the corpus again pays a second time for
 * responses the provider already sent; this replays what was kept instead.
 *
 *   node scripts/regrade-retained.ts                 # every quality dimension
 *   node scripts/regrade-retained.ts --dimension=reasoning
 *   node scripts/regrade-retained.ts --dry-run
 *
 * Refuses any run it cannot replay in full and names it, because re-scoring on a
 * subset would publish a different measurement under the same name. Those need a
 * real re-run.
 *
 * Writes to the database, so the service must be stopped: it is the single
 * writer, and this is the same guard the evaluation batch uses.
 */
import { openBatchDb } from './batch-db.ts';
import { recalculatePublishedOffers } from '../sync/evaluation/recalculate.ts';
import { regradeFromRetainedResponses } from '../sync/evaluation/regrade.ts';
import type { QualityDimension } from '../sync/evaluation/score.ts';

const dimension = process.argv.find((a) => a.startsWith('--dimension='))?.split('=')[1] as QualityDimension | undefined;
const dryRun = process.argv.includes('--dry-run');

const db = await openBatchDb(process.env.CATALOG_DB);
const now = () => new Date().toISOString();

try {
  const summary = regradeFromRetainedResponses({ db, now, dimension, dryRun });

  const moved = summary.rescored.filter((r) => r.before === null || Math.abs(r.after - r.before) >= 0.005);
  for (const row of moved.sort((a, b) => (b.after - (b.before ?? 0)) - (a.after - (a.before ?? 0)))) {
    const before = row.before === null ? '—' : row.before.toFixed(2);
    console.log(`  ${row.identityId.padEnd(36)} ${row.dimension.padEnd(17)} ${before.padStart(7)} -> ${row.after.toFixed(2).padStart(7)}`);
  }

  console.log(`\nreplayed ${summary.rescored.length} dimension(s) from retained responses; ${moved.length} changed.`);
  if (summary.unreplayable.length > 0) {
    console.log(`${summary.unreplayable.length} dimension(s) could not be replayed in full and were left alone:`);
    for (const row of summary.unreplayable.slice(0, 10)) {
      // The reason decides what to do next, so it is printed rather than left
      // to be inferred from the retained count: `answer_truncated` cannot be
      // fixed by any replay, because the provider never finished an answer.
      const outcome = row.demoted ? 'score withdrawn' : 'score left as it was';
      console.log(`  ${row.identityId.padEnd(36)} ${row.dimension.padEnd(17)} ${String(`${row.retained}/${row.samples}`).padStart(7)} retained  ${row.reason.padEnd(23)} ${outcome}`);
    }
    if (summary.unreplayable.length > 10) console.log(`  … and ${summary.unreplayable.length - 10} more`);
    const withdrawn = summary.unreplayable.filter((row) => row.demoted).length;
    if (withdrawn > 0) {
      console.log(`${withdrawn} published score(s) withdrawn: the provider never finished an answer, so there was `
        + 'nothing to re-read. Those dimensions now read as unknown and will be planned again.');
    }
    console.log('Those need a real re-run: `npm run queue -- <providerId>` with --force on the terminal batch.');
  }

  if (dryRun) {
    console.log('\n--dry-run: nothing was written.');
    process.exit(0);
  }

  recalculatePublishedOffers(db, now());
  console.log('published offers recalculated.');
} finally {
  db.close();
}

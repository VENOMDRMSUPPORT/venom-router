import type { LatestBenchmark } from "../api/controlClient";

/** The ISO day (yyyy-mm-dd) of an RFC3339 timestamp, or null when the value is
 * not shaped like one.
 *
 * Deliberately a prefix match rather than `new Date(...).toLocaleDateString()`:
 * the server serializes finished_at in UTC, and a locale/timezone-dependent
 * rendering would show two different days to two owners for the SAME
 * measurement. Null (rather than a guess) is what keeps a malformed value from
 * being displayed as a real date. */
export function isoDay(timestamp: string): string | null {
  const match = /^(\d{4}-\d{2}-\d{2})/.exec(timestamp);
  return match ? match[1] : null;
}

/** The provenance tooltip both surfaces' quality badges carry (spec line
 * ~205: "local benchmark, <date>").
 *
 * Three honest states, never blended:
 *   - no run recorded (or an unparseable timestamp): "Local benchmark" alone —
 *     the source is known, the date is not, and inventing one would be a claim.
 *   - the latest run measured every request: "Local benchmark, <date>" — that
 *     run is what produced the rating being shown.
 *   - the latest run was PARTIAL: the rating on screen is NOT from it. The
 *     local benchmark writes a rating only on a fully successful suite and
 *     leaves the previous rating in place otherwise (see
 *     internal/httpapi/benchmark.go), so the tooltip names the newer run, says
 *     how much of it succeeded, and says the rating predates it.
 *
 * The Model Report modal receives `EffectiveOffering[]`, which carries no
 * `latest_benchmark` — that field lives on `ModelGroup`, which the modal
 * never receives. So the modal always calls this with `null`, which is the
 * first honest state above ("Local benchmark", undated) rather than
 * inventing a date it cannot know. */
export function benchmarkProvenanceTitle(latest: LatestBenchmark | null | undefined): string {
  const day = latest ? isoDay(latest.finished_at) : null;
  if (!latest || day === null) return "Local benchmark";
  if (latest.successes < latest.requests) {
    return (
      `Local benchmark — the latest run (${day}) completed only ${latest.successes} of ` +
      `${latest.requests} requests, so it withheld a rating. The rating shown is from an earlier run.`
    );
  }
  return `Local benchmark, ${day}`;
}

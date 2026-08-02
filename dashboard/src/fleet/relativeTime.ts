// Small relative-time formatter for the Providers page's account meta line
// ("Quota: less than a minute ago · Checked: 1 minute ago") and the quota
// windows' "Resets in 2 hr 21 min".
//
// Pure and clock-injected (nowMs parameter) so it is unit-testable without
// timers, and honest at the edges: an unknown instant is the CALLER's
// problem (render "—" there) — these functions never receive null.

const MINUTE_MS = 60_000;

/** A non-negative duration in the target UI's own vocabulary:
 * "less than a minute", "1 minute", "42 minutes", "2 hr 21 min",
 * "3 day 4 hr". */
export function formatDuration(ms: number): string {
  const clamped = Math.max(0, ms);
  if (clamped < MINUTE_MS) return "less than a minute";
  const totalMinutes = Math.floor(clamped / MINUTE_MS);
  if (totalMinutes < 60) return `${totalMinutes} minute${totalMinutes === 1 ? "" : "s"}`;
  const totalHours = Math.floor(totalMinutes / 60);
  if (totalHours < 24) {
    const minutes = totalMinutes % 60;
    return minutes === 0 ? `${totalHours} hr` : `${totalHours} hr ${minutes} min`;
  }
  const days = Math.floor(totalHours / 24);
  const hours = totalHours % 24;
  return hours === 0 ? `${days} day${days === 1 ? "" : "s"}` : `${days} day${days === 1 ? "" : "s"} ${hours} hr`;
}

/**
 * A relative rendering of `target` against `nowMs`:
 *
 *   past   -> "less than a minute ago" / "1 minute ago" / "2 hr 21 min ago"
 *   future -> "in less than a minute" / "in 2 hr 21 min"
 *
 * `target` is an ISO-8601 string or epoch MILLISECONDS. Returns "—" for an
 * unparseable string rather than a fabricated instant.
 */
export function relativeTime(target: string | number, nowMs: number = Date.now()): string {
  const targetMs = typeof target === "number" ? target : Date.parse(target);
  if (Number.isNaN(targetMs)) return "—";
  const diff = nowMs - targetMs;
  if (diff >= 0) return `${formatDuration(diff)} ago`;
  return `in ${formatDuration(-diff)}`;
}

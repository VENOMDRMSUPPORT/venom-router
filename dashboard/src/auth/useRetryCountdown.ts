import { useEffect, useState } from "react";

/**
 * Ticks a `locked_out` response's `retry_after` (seconds) down to zero,
 * once a second, purely client-side (09 §5.6 — the server is still the
 * source of truth on the next actual request; this only drives the UI's
 * disabled state so the owner isn't stuck guessing when to retry).
 *
 * Pass `null` when there is no active lockout. Passing a new number
 * (a fresh 429) restarts the countdown from that value.
 */
export function useRetryCountdown(retryAfterSeconds: number | null): number {
  const [remaining, setRemaining] = useState(retryAfterSeconds ?? 0);

  useEffect(() => {
    if (retryAfterSeconds == null) {
      setRemaining(0);
      return;
    }

    setRemaining(retryAfterSeconds);
    const startedAt = Date.now();
    const id = setInterval(() => {
      const elapsedSeconds = Math.floor((Date.now() - startedAt) / 1000);
      setRemaining(Math.max(0, retryAfterSeconds - elapsedSeconds));
    }, 1000);

    return () => clearInterval(id);
  }, [retryAfterSeconds]);

  return remaining;
}

/** Renders a countdown as the plain `Ns` text the OwnerSessionStatus /
 * TypedErrorDisplay `retryAfter` slot expects — never a raw styled value,
 * just content. */
export function formatRetryAfter(seconds: number): string {
  return seconds + "s";
}

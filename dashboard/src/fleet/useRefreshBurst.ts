import { useCallback, useEffect, useRef, useState } from "react";

/** Gap between refreshes during a post-connect burst. */
export const REFRESH_BURST_INTERVAL_MS = 4000;

/**
 * How many refreshes one burst fires. Discovery + chat certification for a
 * freshly connected account lands in ~30s (fetch models.dev ∩ provider, then
 * the 30s usability sweep certifies each chat op); REFRESH_BURST_TICKS ×
 * REFRESH_BURST_INTERVAL_MS gives a comfortable margin past that so the
 * "{working} / {discovered}" counts and the account's health dot fill in on
 * their own — no manual page refresh.
 */
export const REFRESH_BURST_TICKS = 15;

/**
 * useRefreshBurst returns a `start` function that fires `onRefresh` once per
 * REFRESH_BURST_INTERVAL_MS, REFRESH_BURST_TICKS times, then stops on its own.
 *
 * It exists because connect is only the FIRST step of the add-account
 * workflow: the backend then discovers models and certifies which ones
 * actually work, asynchronously, over the following ~30s. The fleet view
 * fetches offerings once per load, so without this the freshly connected
 * provider stays stuck at "0 / 0" until the owner refreshes by hand. Calling
 * `start` again restarts the countdown from the top (a second account added
 * mid-burst gets its own full window).
 *
 * onRefresh is read through a ref so a new closure each render never reschedules
 * or restarts the in-flight burst — only an explicit `start()` does.
 */
export function useRefreshBurst(onRefresh: () => void): () => void {
  const [ticksLeft, setTicksLeft] = useState(0);
  const onRefreshRef = useRef(onRefresh);
  onRefreshRef.current = onRefresh;

  useEffect(() => {
    if (ticksLeft <= 0) return;
    const id = setTimeout(() => {
      onRefreshRef.current();
      setTicksLeft((n) => n - 1);
    }, REFRESH_BURST_INTERVAL_MS);
    return () => clearTimeout(id);
  }, [ticksLeft]);

  return useCallback(() => setTicksLeft(REFRESH_BURST_TICKS), []);
}

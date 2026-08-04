import { useEffect, useRef } from "react";

/** Steady fleet-poll cadence. The backend's own loops move on 30s ticks
 * (usability certification), 5min (health) and 15min (quota), so a 10s poll
 * keeps the page visibly live without meaningful load — the three list
 * endpoints are cheap local reads. */
export const FLEET_POLL_INTERVAL_MS = 10_000;

/**
 * usePollingRefresh fires `onRefresh` every `intervalMs` for as long as the
 * component is mounted AND the document is visible — the fleet page's live
 * heartbeat, so working counts, health dots, quota bars and balances update
 * on their own (the owner never has to hand-refresh to see progress).
 *
 * Hidden tabs pause the loop (visibilitychange) and refresh IMMEDIATELY on
 * return, so a backgrounded dashboard neither wastes requests nor shows
 * stale data when the owner comes back.
 *
 * onRefresh is read through a ref so a new closure each render never
 * restarts the interval.
 */
export function usePollingRefresh(
  onRefresh: () => void,
  intervalMs: number = FLEET_POLL_INTERVAL_MS,
): void {
  const onRefreshRef = useRef(onRefresh);
  onRefreshRef.current = onRefresh;

  useEffect(() => {
    let id: number | null = null;

    function start() {
      if (id != null) return;
      id = window.setInterval(() => onRefreshRef.current(), intervalMs);
    }
    function stop() {
      if (id == null) return;
      window.clearInterval(id);
      id = null;
    }
    function handleVisibility() {
      if (document.visibilityState === "hidden") {
        stop();
      } else {
        onRefreshRef.current();
        start();
      }
    }

    if (document.visibilityState !== "hidden") start();
    document.addEventListener("visibilitychange", handleVisibility);
    return () => {
      stop();
      document.removeEventListener("visibilitychange", handleVisibility);
    };
  }, [intervalMs]);
}

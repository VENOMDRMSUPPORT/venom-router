import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react';
import { fetchCatalog, fetchChangeCursor, fetchHealth, type CatalogData, type HealthResponse } from '../api/client';

/**
 * How often the app asks whether anything has changed.
 *
 * Matches the health poll and the alert poll, so the three surfaces of one page
 * cannot show state from three different moments.
 */
export const CHANGE_CURSOR_POLL_MS = 30_000;

/**
 * One catalog fetch for the whole app.
 *
 * A context rather than a per-page hook so the dashboard and the provider pages
 * can never disagree about the totals — the reconciliation the audit demands
 * (provider counts summing to the global count) is then true by construction
 * rather than by both sides happening to fetch the same thing.
 */
interface CatalogState {
  data: CatalogData | null;
  error: string | null;
  loading: boolean;
  health?: HealthResponse | null;
  healthError?: string | null;
  healthLoading?: boolean;
  /**
   * Bumped every time this provider refetched — manually or because the change
   * cursor moved. A consumer holding its own derived fetch (the dashboard's
   * change list) puts this in its dependencies so it cannot go on showing a
   * change feed from before the roster it sits next to.
   */
  revision: number;
  reload: () => void;
}

const Ctx = createContext<CatalogState>({
  data: null,
  error: null,
  loading: true,
  health: null,
  healthError: null,
  healthLoading: true,
  revision: 0,
  reload: () => {},
});

export function CatalogProvider({ children }: { children: ReactNode }) {
  const [data, setData] = useState<CatalogData | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [health, setHealth] = useState<HealthResponse | null>(null);
  const [healthError, setHealthError] = useState<string | null>(null);
  const [healthLoading, setHealthLoading] = useState(true);
  const [nonce, setNonce] = useState(0);
  const dataRef = useRef<CatalogData | null>(null);
  const renderedCursor = useRef<string | null | undefined>(undefined);
  const pendingCursor = useRef<string | null | undefined>(undefined);

  useEffect(() => {
    dataRef.current = data;
  }, [data]);

  useEffect(() => {
    const ctrl = new AbortController();
    const cursorForAttempt = pendingCursor.current;
    // Background revalidation must not unmount pages that already have data:
    // their expanded rows, focus, and scroll position belong to the user.
    setLoading(dataRef.current === null);
    fetchCatalog(ctrl.signal)
      .then((d) => {
        if (ctrl.signal.aborted) return;
        setData(d);
        setError(null);
        if (cursorForAttempt !== undefined) {
          renderedCursor.current = cursorForAttempt;
          if (pendingCursor.current === cursorForAttempt) pendingCursor.current = undefined;
        }
      })
      .catch((e) => {
        if (ctrl.signal.aborted) return;
        if (pendingCursor.current === cursorForAttempt) pendingCursor.current = undefined;
        setError(e instanceof Error ? e.message : String(e));
      })
      .finally(() => { if (!ctrl.signal.aborted) setLoading(false); });
    return () => ctrl.abort();
  }, [nonce]);

  useEffect(() => {
    const ctrl = new AbortController();
    let active = true;

    const pollHealth = () => {
      setHealthLoading(true);
      fetchHealth(ctrl.signal)
        .then((result) => {
          if (!active) return;
          setHealth(result);
          setHealthError(null);
        })
        .catch((reason) => {
          if (!active || ctrl.signal.aborted) return;
          setHealthError(reason instanceof Error ? reason.message : String(reason));
        })
        .finally(() => {
          if (active && !ctrl.signal.aborted) setHealthLoading(false);
        });
    };

    pollHealth();
    const interval = window.setInterval(pollHealth, 30_000);
    return () => {
      active = false;
      ctrl.abort();
      window.clearInterval(interval);
    };
  }, [nonce]);

  /**
   * Reload when the catalog actually changed, not on a timer.
   *
   * The models and providers used to be fetched exactly once per mount. Health
   * polled, alerts polled, and the table did not — so a sync that added or
   * retired a model left the roster on screen stale until someone reloaded by
   * hand, and nothing on the page admitted it.
   *
   * Polling the cursor rather than the catalog is the point: every recorded
   * change moves `MAX(at)` in `model_events`, so one aggregate query answers
   * "is what I am showing still current" without refetching a payload that is
   * usually identical. The first reading only arms the comparison — it must not
   * count as a change, or every mount would fetch the catalog twice.
   */
  useEffect(() => {
    const ctrl = new AbortController();

    const checkForChanges = () => {
      if (document.visibilityState !== 'visible') return;
      fetchChangeCursor(ctrl.signal)
        .then((cursor) => {
          if (ctrl.signal.aborted) return;
          const previous = renderedCursor.current;
          if (previous === undefined) {
            renderedCursor.current = cursor;
            return;
          }
          if (previous === cursor || pendingCursor.current === cursor) return;
          pendingCursor.current = cursor;
          setNonce((value) => value + 1);
        })
        // A failed probe is not worth a visible error: the catalog fetch and the
        // health poll already report an unreachable service, and a third voice
        // saying the same thing would only crowd the page.
        .catch(() => {});
    };

    checkForChanges();
    const interval = window.setInterval(checkForChanges, CHANGE_CURSOR_POLL_MS);
    document.addEventListener('visibilitychange', checkForChanges);
    window.addEventListener('focus', checkForChanges);
    return () => {
      ctrl.abort();
      window.clearInterval(interval);
      document.removeEventListener('visibilitychange', checkForChanges);
      window.removeEventListener('focus', checkForChanges);
    };
  }, []);

  useEffect(() => {
    const pending = data?.models.filter((model) => model.resolution.state === 'processing') ?? [];
    if (pending.length === 0 || data?.origin !== 'live') return;
    const next = pending
      .map((model) => model.resolution.nextAttemptAt)
      .filter((value): value is string => value !== null)
      .map((value) => new Date(value).getTime());
    const untilNext = next.length > 0 ? Math.min(...next) - Date.now() + 500 : 30_000;
    const timer = window.setTimeout(() => setNonce((value) => value + 1), Math.min(30_000, Math.max(1_000, untilNext)));
    return () => window.clearTimeout(timer);
  }, [data]);

  return (
    <Ctx.Provider value={{ data, error, loading, health, healthError, healthLoading, revision: nonce, reload: () => setNonce((n) => n + 1) }}>
      {children}
    </Ctx.Provider>
  );
}

export const useCatalog = () => useContext(Ctx);

/** Models for one provider, straight from the same payload the totals come from. */
export function useProviderModels(providerId: string | undefined) {
  const { data } = useCatalog();
  const provider = data?.providers.find((p) => p.id === providerId) ?? null;
  const models = data?.models.filter((m) => m.providerId === providerId) ?? [];
  return { provider, models, meta: data?.meta ?? null };
}

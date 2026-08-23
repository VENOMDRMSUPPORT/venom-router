import { createContext, useContext, useEffect, useState, type ReactNode } from 'react';
import { fetchCatalog, fetchHealth, type CatalogData, type HealthResponse } from '../api/client';

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
  reload: () => void;
}

const Ctx = createContext<CatalogState>({
  data: null,
  error: null,
  loading: true,
  health: null,
  healthError: null,
  healthLoading: true,
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

  useEffect(() => {
    const ctrl = new AbortController();
    setLoading(true);
    fetchCatalog(ctrl.signal)
      .then((d) => { setData(d); setError(null); })
      .catch((e) => { if (!ctrl.signal.aborted) setError(e instanceof Error ? e.message : String(e)); })
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
    <Ctx.Provider value={{ data, error, loading, health, healthError, healthLoading, reload: () => setNonce((n) => n + 1) }}>
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

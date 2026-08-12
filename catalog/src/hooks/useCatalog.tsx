import { createContext, useContext, useEffect, useState, type ReactNode } from 'react';
import { fetchCatalog, type CatalogData } from '../api/client';

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
  reload: () => void;
}

const Ctx = createContext<CatalogState>({ data: null, error: null, loading: true, reload: () => {} });

export function CatalogProvider({ children }: { children: ReactNode }) {
  const [data, setData] = useState<CatalogData | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
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

  return (
    <Ctx.Provider value={{ data, error, loading, reload: () => setNonce((n) => n + 1) }}>
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

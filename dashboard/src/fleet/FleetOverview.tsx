import { useCallback, useEffect, useState } from "react";
import { EmptyState, ErrorState, Spinner, StatCard } from "@venom/design-system/primitives";
import {
  isSessionExpired,
  listAccounts,
  listProviders,
  toApiError,
  type AccountProjection,
  type AuthApiError,
  type Provider,
} from "../api/controlClient";
import ConnectDialog from "./ConnectDialog";
import ProviderRow from "./ProviderRow";

export interface FleetOverviewProps {
  csrfToken: string;
  onSessionExpired: () => void;
}

/** Fetches every account page (bounded — an owner console's account count
 * is small; this cap just guards against a pathological infinite cursor
 * loop, it is not a real pagination limit). */
async function fetchAllAccounts(): Promise<AccountProjection[]> {
  const all: AccountProjection[] = [];
  let cursor: string | undefined;
  for (let page = 0; page < 25; page++) {
    const result = await listAccounts({ cursor, limit: 200 });
    all.push(...result.accounts);
    if (!result.nextCursor) break;
    cursor = result.nextCursor;
  }
  return all;
}

/**
 * The Provider Fleet dashboard (P2b-UI-003): stat cards, one
 * ProviderSummaryCard per catalog provider (expandable to its
 * ProviderAccountRows), API-key/OAuth connect dialogs, credential reveal,
 * funding override, and lifecycle actions (stop/resume/refresh
 * health/disconnect). Assembled entirely from `@venom/design-system`'s
 * domain components — there is no single shipped ProviderFleet UI
 * component to render, only the domain building blocks this composes.
 */
export default function FleetOverview(props: FleetOverviewProps) {
  const { csrfToken, onSessionExpired } = props;

  const [providers, setProviders] = useState<Provider[] | null>(null);
  const [accounts, setAccounts] = useState<AccountProjection[] | null>(null);
  const [loadError, setLoadError] = useState<AuthApiError | null>(null);
  const [reloadToken, setReloadToken] = useState(0);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [connectProvider, setConnectProvider] = useState<Provider | null>(null);

  const reload = useCallback(() => setReloadToken((t) => t + 1), []);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      setLoadError(null);
      try {
        const [providerList, accountList] = await Promise.all([listProviders(), fetchAllAccounts()]);
        if (cancelled) return;
        setProviders(providerList);
        setAccounts(accountList);
      } catch (err) {
        if (cancelled) return;
        if (isSessionExpired(err)) {
          onSessionExpired();
          return;
        }
        setLoadError(toApiError(err));
      }
    }

    void load();
    return () => {
      cancelled = true;
    };
  }, [reloadToken, onSessionExpired]);

  function toggleExpanded(providerId: string) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(providerId)) next.delete(providerId);
      else next.add(providerId);
      return next;
    });
  }

  if (loadError) {
    return (
      <ErrorState
        code={loadError.code}
        title="Could not load the provider fleet"
        description={loadError.message}
        onRetry={loadError.retryable ? reload : undefined}
      />
    );
  }

  if (!providers || !accounts) {
    return <Spinner size="lg" label="Loading the provider fleet" />;
  }

  const accountsByProvider = new Map<string, AccountProjection[]>();
  for (const account of accounts) {
    const list = accountsByProvider.get(account.provider);
    if (list) list.push(account);
    else accountsByProvider.set(account.provider, [account]);
  }

  const healthyCount = accounts.filter((a) => a.display_status === "healthy").length;

  return (
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-4 gap-3">
        <StatCard label="Providers" value={providers.length} icon="server" />
        <StatCard label="Accounts" value={accounts.length} icon="user-round" />
        <StatCard label="Healthy" value={healthyCount} tone="healthy" icon="heart-pulse" />
        {/* Model discovery is a later phase (non-goal here) — an honest
         * "—" rather than a fabricated count. */}
        <StatCard label="Models" value="—" tone="unknown" icon="box" />
      </div>

      {providers.length === 0 ? (
        <EmptyState icon="server" title="No providers in the catalog" />
      ) : (
        <div className="flex flex-col gap-3">
          {providers.map((provider) => (
            <ProviderRow
              key={provider.id}
              provider={provider}
              accounts={accountsByProvider.get(provider.id) ?? []}
              expanded={expanded.has(provider.id)}
              onToggleExpand={() => toggleExpanded(provider.id)}
              onConnect={() => setConnectProvider(provider)}
              csrfToken={csrfToken}
              onSessionExpired={onSessionExpired}
              onChanged={reload}
            />
          ))}
        </div>
      )}

      <ConnectDialog
        provider={connectProvider}
        csrfToken={csrfToken}
        onSessionExpired={onSessionExpired}
        onClose={() => setConnectProvider(null)}
        onConnected={() => {
          setConnectProvider(null);
          reload();
        }}
      />
    </div>
  );
}

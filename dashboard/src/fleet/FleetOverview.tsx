import { useCallback, useEffect, useState } from "react";
import {
  Button,
  EmptyState,
  ErrorState,
  FilterBar,
  SegmentedControl,
  Spinner,
  StatCard,
} from "@venom/design-system/primitives";
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
import ProviderCard from "./ProviderCard";
import type { FleetView } from "./FleetBreadcrumbChips";

export interface FleetOverviewProps {
  csrfToken: string;
  onSessionExpired: () => void;
  /** Reports the breadcrumb-chip counts (active = providers with at least
   * one connected account, total = full catalog) to the shell once the
   * live data has loaded. */
  onCounts?: (counts: { active: number; total: number }) => void;
  /** The breadcrumb-row chip selection: "active" narrows the grid to
   * providers with ≥1 connected account (on top of the search/category
   * filters); "all" shows the full scoped catalog. Defaults to "all". */
  view?: FleetView;
}

type AuthCategory = "all" | "oauth" | "api_key" | "custom";

const CATEGORY_OPTIONS = [
  { value: "oauth", label: "OAuth Providers" },
  { value: "api_key", label: "Api key Providers" },
  { value: "custom", label: "Custom Providers" },
];

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
 * The Provider Fleet dashboard (P2b-UI-003, legacy-parity layout): stat
 * cards, a toolbar (live "Search integrations…" filter + All/OAuth/API Key
 * segmented tabs scoping by auth mode), and the responsive 2-column
 * integration-card grid (ProviderCard — official logo or letter mark,
 * legacy auth badges, per-state action button, expandable account rows),
 * plus the API-key/OAuth connect dialogs, credential reveal, funding
 * override, and lifecycle actions. Assembled entirely from
 * `@venom/design-system`'s building blocks — there is no single shipped
 * ProviderFleet UI component to render.
 *
 * Like the legacy category tabs, the OAuth/API Key tabs scope by typed
 * auth mode only (no slug logic); `custom_openai` stays visible under All
 * only.
 */
export default function FleetOverview(props: FleetOverviewProps) {
  const { csrfToken, onSessionExpired, onCounts, view = "all" } = props;

  const [providers, setProviders] = useState<Provider[] | null>(null);
  const [accounts, setAccounts] = useState<AccountProjection[] | null>(null);
  const [loadError, setLoadError] = useState<AuthApiError | null>(null);
  const [reloadToken, setReloadToken] = useState(0);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [connectProvider, setConnectProvider] = useState<Provider | null>(null);
  const [category, setCategory] = useState<AuthCategory>("all");
  const [search, setSearch] = useState("");

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

  // Report the breadcrumb-chip counts from the FULL catalog (never the
  // search/tab-scoped view) whenever fresh data lands.
  useEffect(() => {
    if (!providers || !accounts || !onCounts) return;
    const withAccounts = new Set(accounts.map((a) => a.provider));
    onCounts({
      active: providers.filter((p) => withAccounts.has(p.id)).length,
      total: providers.length,
    });
  }, [providers, accounts, onCounts]);

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

  // Legacy-parity scoping: the breadcrumb-row view toggle ("active" narrows
  // to providers with ≥1 connected account) composes with the segmented
  // tabs (auth mode) and the live search.
  const connectedProviders = new Set(accountsByProvider.keys());
  const scopedProviders = providers.filter((p) => {
    if (view === "active" && !connectedProviders.has(p.id)) return false;
    if (category === "all") return true;
    if (category === "oauth") return p.auth_mode === "oauth2";
    if (category === "custom") return p.auth_mode === "custom_openai";
    return p.auth_mode === "api_key";
  });
  const query = search.trim().toLowerCase();
  const filteredProviders = query
    ? scopedProviders.filter(
        (p) =>
          p.display_name.toLowerCase().includes(query) ||
          p.id.toLowerCase().includes(query) ||
          p.description.toLowerCase().includes(query),
      )
    : scopedProviders;
  const emptyActiveView = view === "active" && category === "all" && query.length === 0;

  return (
    <div className="flex flex-col gap-4">
      <div className="vn-provider-stats">
        <StatCard label="Providers" value={providers.length} icon="server" />
        <StatCard label="Accounts" value={accounts.length} icon="user-round" />
        <StatCard label="Healthy" value={healthyCount} tone="healthy" icon="heart-pulse" />
        {/* Model discovery is a later phase (non-goal here) — an honest
         * "—" rather than a fabricated count. */}
        <StatCard label="Models" value="—" tone="unknown" icon="box" />
      </div>

      <FilterBar
        label="Provider filters"
        searchValue={search}
        onSearchChange={setSearch}
        searchLabel="Search integrations"
        searchPlaceholder="Search integrations…"
      >
        <SegmentedControl
          label="Filter providers by authentication type"
          options={CATEGORY_OPTIONS}
          value={category}
          onChange={(value) => setCategory(value as AuthCategory)}
        />
      </FilterBar>

      {providers.length === 0 ? (
        <EmptyState icon="server" title="No providers in the catalog" />
      ) : filteredProviders.length === 0 ? (
        <div className="vn-panel p-8">
          <EmptyState
            icon={emptyActiveView ? "circle-check" : "search"}
            title={emptyActiveView ? "No active providers" : "No integrations found"}
            description={emptyActiveView
              ? "No provider accounts are connected yet. Choose All Integrations to browse the catalog."
              : "Try adjusting your search terms or category filters."}
            action={emptyActiveView ? undefined : (
              <Button variant="secondary" size="sm" onClick={() => setSearch("")}>
                Clear Search
              </Button>
            )}
          />
        </div>
      ) : (
        <div className="vn-provider-grid">
          {filteredProviders.map((provider) => (
            <ProviderCard
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

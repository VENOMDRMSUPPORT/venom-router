import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Alert,
  Badge,
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
  listOfferings,
  listProviders,
  toApiError,
  type AccountProjection,
  type AuthApiError,
  type EffectiveOffering,
  type Provider,
} from "../api/controlClient";
import ConnectDialog from "./ConnectDialog";
import ModelTestReport from "./ModelTestReport";
import ProviderCard from "./ProviderCard";
import ProviderRow from "./ProviderRow";
import { distinctModelStats } from "./modelStatus";
import { providerDescription, providerDisplayName } from "./providerMeta";
import type { FleetView } from "./FleetBreadcrumbChips";
import "./fleet.css";

/** The auth-mode filter the segmented tabs select. "all" is the only tab
 * that shows `custom_openai` entries. */
export type AuthCategory = "all" | "oauth" | "api_key";

export interface FleetOverviewProps {
  csrfToken: string;
  onSessionExpired: () => void;
  /** Reports the breadcrumb-chip counts (active = providers with at least
   * one connected account, total = full catalog) to the shell once the
   * live data has loaded. */
  onCounts?: (counts: { active: number; total: number }) => void;
  /** The breadcrumb-row chip selection: "active" renders the compact
   * provider ROW LIST (providers with ≥1 account); "all" renders the full
   * catalog CARD GRID. Defaults to "all". */
  view?: FleetView;
  /** Controlled auth-category filter (the shell lifts it so the
   * breadcrumb's third segment can mirror it). Uncontrolled when absent. */
  category?: AuthCategory;
  onCategoryChange?: (category: AuthCategory) => void;
}

const CATEGORY_OPTIONS = [
  { value: "all", label: "All" },
  { value: "oauth", label: "OAuth" },
  { value: "api_key", label: "API Key" },
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

/** Fetches every offerings page (same bounded cursor loop as accounts). */
async function fetchAllOfferings(): Promise<EffectiveOffering[]> {
  const all: EffectiveOffering[] = [];
  let cursor: string | undefined;
  for (let page = 0; page < 25; page++) {
    const result = await listOfferings({ cursor, limit: 200 });
    all.push(...result.offerings);
    if (!result.nextCursor) break;
    cursor = result.nextCursor;
  }
  return all;
}

function matchesCategory(provider: Provider, category: AuthCategory): boolean {
  if (category === "all") return true;
  if (category === "oauth") return provider.auth_mode === "oauth2";
  return provider.auth_mode === "api_key";
}

/**
 * The Providers page (P2b-UI-003, rebuilt to the documented fleet layout):
 * contextual stat cards, the search + All/OAuth/API Key filter bar, and
 * TWO views selected by the shell's breadcrumb chips —
 *
 *   Active Providers: a compact ROW LIST of providers with ≥1 connected
 *   account (ProviderRow), each expandable into its numbered account rows
 *   with per-account sync/discover/report/disable/disconnect actions.
 *
 *   All Integrations: the full catalog CARD GRID (ProviderCard) with
 *   marketing meta, CONNECTED state, and the connect call-to-action.
 *
 * Offerings (GET /offerings) load in parallel and drive the model counts;
 * a failed offerings read NEVER breaks the page — counts render an honest
 * "—" and the failure is surfaced once, inline.
 */
export default function FleetOverview(props: FleetOverviewProps) {
  const { csrfToken, onSessionExpired, onCounts, view = "all", onCategoryChange } = props;

  const [providers, setProviders] = useState<Provider[] | null>(null);
  const [accounts, setAccounts] = useState<AccountProjection[] | null>(null);
  const [offerings, setOfferings] = useState<EffectiveOffering[] | null>(null);
  const [offeringsError, setOfferingsError] = useState<AuthApiError | null>(null);
  const [loadError, setLoadError] = useState<AuthApiError | null>(null);
  const [reloadToken, setReloadToken] = useState(0);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [connectProvider, setConnectProvider] = useState<Provider | null>(null);
  const [internalCategory, setInternalCategory] = useState<AuthCategory>("all");
  const [search, setSearch] = useState("");
  const [reportAccountId, setReportAccountId] = useState<string | null>(null);

  const category = props.category ?? internalCategory;

  const reload = useCallback(() => setReloadToken((t) => t + 1), []);

  function handleCategoryChange(next: AuthCategory) {
    setInternalCategory(next);
    onCategoryChange?.(next);
  }

  useEffect(() => {
    let cancelled = false;

    async function load() {
      setLoadError(null);
      try {
        // Offerings ride alongside but fail SOFT: model counts degrade to
        // "—" while the rest of the page still works.
        const offeringsPromise: Promise<
          { ok: true; offerings: EffectiveOffering[] } | { ok: false; error: unknown }
        > = fetchAllOfferings().then(
          (list) => ({ ok: true as const, offerings: list }),
          (error: unknown) => ({ ok: false as const, error }),
        );
        const [providerList, accountList, offeringsResult] = await Promise.all([
          listProviders(),
          fetchAllAccounts(),
          offeringsPromise,
        ]);
        if (cancelled) return;
        setProviders(providerList);
        setAccounts(accountList);
        if (offeringsResult.ok) {
          setOfferings(offeringsResult.offerings);
          setOfferingsError(null);
        } else {
          if (isSessionExpired(offeringsResult.error)) {
            onSessionExpired();
            return;
          }
          setOfferings(null);
          setOfferingsError(toApiError(offeringsResult.error));
        }
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

  const offeringsByAccount = useMemo(() => {
    if (!offerings) return null;
    const map = new Map<string, EffectiveOffering[]>();
    for (const offering of offerings) {
      const list = map.get(offering.account_id);
      if (list) list.push(offering);
      else map.set(offering.account_id, [offering]);
    }
    return map;
  }, [offerings]);

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

  // A disconnected account is retained server-side for history, but it is no
  // longer active: it must not keep its provider in the Active Providers view,
  // render as a live account row, or count toward the fleet stat cards. Every
  // "active fleet" derivation below works off liveAccounts, so a provider whose
  // only account is disconnected drops out of Active and is found again under
  // All Integrations (its original, un-connected catalog state).
  const liveAccounts = accounts.filter((a) => a.connection_state !== "disconnected");

  const accountsByProvider = new Map<string, AccountProjection[]>();
  for (const account of liveAccounts) {
    const list = accountsByProvider.get(account.provider);
    if (list) list.push(account);
    else accountsByProvider.set(account.provider, [account]);
  }
  const providersById = new Map(providers.map((p) => [p.id, p]));
  const connectedProviders = new Set(accountsByProvider.keys());

  // --- Contextual stat-card scope: the CURRENT view + auth filter ---------

  const categoryProviders = providers.filter((p) => matchesCategory(p, category));
  const scopedConnectedCount = categoryProviders.filter((p) => connectedProviders.has(p.id)).length;
  const scopedAccounts = liveAccounts.filter((a) => {
    const provider = providersById.get(a.provider);
    return provider ? matchesCategory(provider, category) : category === "all";
  });
  const scopedAccountIds = new Set(scopedAccounts.map((a) => a.id));
  const scopedProviderCountForAccounts = new Set(scopedAccounts.map((a) => a.provider)).size;
  const healthyCount = scopedAccounts.filter((a) => a.display_status === "healthy").length;
  const scopedOfferings = offerings ? offerings.filter((o) => scopedAccountIds.has(o.account_id)) : null;
  const modelStats = scopedOfferings ? distinctModelStats(scopedOfferings) : null;

  // --- View scoping + search ------------------------------------------------

  const viewScoped = view === "active"
    ? categoryProviders.filter((p) => connectedProviders.has(p.id))
    : categoryProviders;
  const query = search.trim().toLowerCase();
  const filteredProviders = query
    ? viewScoped.filter(
        (p) =>
          providerDisplayName(p).toLowerCase().includes(query) ||
          p.display_name.toLowerCase().includes(query) ||
          p.id.toLowerCase().includes(query) ||
          providerDescription(p).toLowerCase().includes(query) ||
          p.description.toLowerCase().includes(query),
      )
    : viewScoped;
  const emptyActiveView = view === "active" && category === "all" && query.length === 0;

  /** Distinct-model stats (total discovered + verified-working) across ONE
   * provider's accounts, or null while the offerings read is unknown. */
  function providerModelStats(providerId: string): { total: number; working: number } | null {
    if (!offerings) return null;
    const ids = new Set((accountsByProvider.get(providerId) ?? []).map((a) => a.id));
    return distinctModelStats(offerings.filter((o) => ids.has(o.account_id)));
  }

  function accountModelCount(accountId: string): number | null {
    if (!offeringsByAccount) return null;
    return distinctModelStats(offeringsByAccount.get(accountId) ?? []).total;
  }

  const reportAccount = reportAccountId ? accounts.find((a) => a.id === reportAccountId) ?? null : null;
  const reportProvider = reportAccount ? providersById.get(reportAccount.provider) : undefined;

  return (
    <div className="flex flex-col gap-4">
      <div className="vn-provider-stats">
        <StatCard
          label="Providers"
          value={view === "active" ? scopedConnectedCount : categoryProviders.length}
          meta={view === "active" ? "connected integrations" : "all integrations"}
          icon="server"
        />
        <StatCard
          label="Accounts"
          value={scopedAccounts.length}
          meta={`across ${scopedProviderCountForAccounts} provider${scopedProviderCountForAccounts === 1 ? "" : "s"}`}
          icon="user-round"
        />
        <StatCard
          label="Healthy"
          value={`${healthyCount}/${scopedAccounts.length}`}
          tone="healthy"
          icon="heart-pulse"
          meta={
            <span className="flex items-center gap-2">
              account health
              {scopedAccounts.length > 0 && healthyCount === scopedAccounts.length ? (
                <Badge tone="healthy">all healthy</Badge>
              ) : null}
            </span>
          }
        />
        {/* An offerings read that has not landed renders the honest "—",
         * never a fabricated 0. */}
        <StatCard
          label="Models"
          value={modelStats ? modelStats.total : "—"}
          tone={modelStats ? undefined : "unknown"}
          icon="box"
          meta={modelStats ? `${modelStats.working} working · unique` : offeringsError ? "offerings unavailable" : "loading…"}
        />
      </div>

      {offeringsError ? (
        <Alert tone="warning" title="Could not load model offerings">
          Model counts show "—" until the offerings read succeeds ({offeringsError.code}).{" "}
          <Button variant="ghost" size="sm" onClick={reload}>
            Retry
          </Button>
        </Alert>
      ) : null}

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
          onChange={(value) => handleCategoryChange(value as AuthCategory)}
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
      ) : view === "active" ? (
        <div className="vnd-fleet-rows">
          {filteredProviders.map((provider) => {
            const stats = providerModelStats(provider.id);
            return (
            <ProviderRow
              key={provider.id}
              provider={provider}
              accounts={accountsByProvider.get(provider.id) ?? []}
              uniqueModelCount={stats ? stats.total : null}
              workingModelCount={stats ? stats.working : null}
              accountModelCounts={accountModelCount}
              expanded={expanded.has(provider.id)}
              onToggleExpand={() => toggleExpanded(provider.id)}
              onAddAccount={() => setConnectProvider(provider)}
              onOpenModelReport={(account) => setReportAccountId(account.id)}
              csrfToken={csrfToken}
              onSessionExpired={onSessionExpired}
              onChanged={reload}
            />
            );
          })}
        </div>
      ) : (
        <div className="vn-provider-grid">
          {filteredProviders.map((provider) => (
            <ProviderCard
              key={provider.id}
              provider={provider}
              accountCount={(accountsByProvider.get(provider.id) ?? []).length}
              onConnect={() => setConnectProvider(provider)}
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

      {reportAccount ? (
        <ModelTestReport
          open
          account={reportAccount}
          providerName={reportProvider ? providerDisplayName(reportProvider) : reportAccount.provider}
          offerings={offeringsByAccount?.get(reportAccount.id) ?? []}
          csrfToken={csrfToken}
          onSessionExpired={onSessionExpired}
          onClose={() => setReportAccountId(null)}
          onRefetch={reload}
        />
      ) : null}
    </div>
  );
}

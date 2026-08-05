import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "@venom/design-system";
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
import { useRefreshBurst } from "./useRefreshBurst";
import { usePollingRefresh } from "./usePollingRefresh";
import { providerDescription, providerDisplayName } from "./providerMeta";
import type { FleetView } from "./FleetBreadcrumbChips";
import { countsTowardFleet, isListedAccount } from "./accountScope";
import {
  CATEGORY_OPTIONS,
  DEFAULT_AUTH_CATEGORY,
  authCategoryLabel,
  matchesAuthCategory,
  type AuthCategory,
} from "./authCategory";
import "./fleet.css";


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
  /** Switches the breadcrumb-row view. Connecting an account from the "all"
   * catalog grid calls this with "active": the whole point of connecting is
   * to SEE the new provider, and it does not appear in the grid the owner is
   * standing on. Uncontrolled when absent (the view simply does not move). */
  onViewChange?: (view: FleetView) => void;
  /** Controlled auth-category filter (the shell lifts it so the
   * breadcrumb's third segment can mirror it). Uncontrolled when absent. */
  category?: AuthCategory;
  onCategoryChange?: (category: AuthCategory) => void;
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
  return matchesAuthCategory(provider.auth_mode, category);
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
  const {
    csrfToken,
    onSessionExpired,
    onCounts,
    view = "all",
    onViewChange,
    onCategoryChange,
  } = props;

  const [providers, setProviders] = useState<Provider[] | null>(null);
  const [accounts, setAccounts] = useState<AccountProjection[] | null>(null);
  const [offerings, setOfferings] = useState<EffectiveOffering[] | null>(null);
  const [offeringsError, setOfferingsError] = useState<AuthApiError | null>(null);
  const [loadError, setLoadError] = useState<AuthApiError | null>(null);
  const [reloadToken, setReloadToken] = useState(0);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [connectProvider, setConnectProvider] = useState<Provider | null>(null);
  const [internalCategory, setInternalCategory] = useState<AuthCategory>(DEFAULT_AUTH_CATEGORY);
  const [search, setSearch] = useState("");
  const [reportAccountId, setReportAccountId] = useState<string | null>(null);

  const category = props.category ?? internalCategory;

  const reload = useCallback(() => setReloadToken((t) => t + 1), []);

  // After a successful connect the backend keeps working asynchronously
  // (discover models -> certify which chat models actually run, ~30s), so a
  // single reload would only capture the pre-discovery "0 / 0" snapshot. This
  // re-fetches on a short burst so the counts and the health dot fill in on
  // their own — no manual refresh.
  const startRefreshBurst = useRefreshBurst(reload);

  const handleBurstRefresh = useCallback(() => {
    startRefreshBurst();
    toast.info("Fleet status refreshed");
  }, [startRefreshBurst]);

  // The steady live heartbeat: the backend's own loops keep certifying
  // models, probing health and syncing quota in the background, so the page
  // re-fetches every few seconds (paused while the tab is hidden) — working
  // counts, health dots, quota bars and balances update on their own.
  usePollingRefresh(reload);

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
    // A provider is "active" only if it has a LIVE (non-disconnected) account —
    // the same rule the Active view uses. Counting raw accounts here made the
    // "Active Providers" chip show a stale 1 while the view correctly showed
    // none (a disconnected account still lingered under the old soft-disconnect).
    const withLiveAccounts = new Set(
      accounts.filter((a) => a.connection_state !== "disconnected").map((a) => a.provider),
    );
    onCounts({
      active: providers.filter((p) => withLiveAccounts.has(p.id)).length,
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
  const liveAccounts = accounts.filter(isListedAccount);

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
  // The COUNTED scope — every stat card and the model totals derive from it,
  // and it is the only thing here that applies countsTowardFleet: a disabled
  // account still renders (accountsByProvider above keeps it) but must read as
  // if it were not there in every number, and come straight back on re-enable
  // because these are derivations, not stored counts.
  const scopedAccounts = liveAccounts.filter(countsTowardFleet).filter((a) => {
    const provider = providersById.get(a.provider);
    // An account whose provider is missing from the catalog is an anomaly
    // with no auth_mode to filter on. It used to surface under "All"; with
    // that tab gone it is attributed to the key-authenticated tab, which is
    // the complement branch — so such an account stays VISIBLE somewhere
    // rather than being silently dropped from both tabs' counts.
    return provider ? matchesCategory(provider, category) : category === "api_key";
  });
  const scopedAccountIds = new Set(scopedAccounts.map((a) => a.id));
  const scopedProviderCountForAccounts = new Set(scopedAccounts.map((a) => a.provider)).size;
  const healthyCount = scopedAccounts.filter((a) => a.display_status === "healthy").length;
  const scopedOfferings = offerings
    ? offerings.filter((o) => scopedAccountIds.has(o.account_id))
    : null;
  const scopedHealthyAccountIds = new Set(
    scopedAccounts.filter((a) => a.display_status === "healthy").map((a) => a.id),
  );
  const modelStats = scopedOfferings
    ? distinctModelStats(scopedOfferings.filter((o) => scopedHealthyAccountIds.has(o.account_id)))
    : null;

  // --- View scoping + search ------------------------------------------------

  const viewScoped =
    view === "active"
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
  // "Nothing is connected in THIS tab" vs "your search matched nothing".
  // The old `category === "all"` term is gone with the All tab: a category
  // filter is now always in effect, so keeping it would make this
  // permanently false and every empty active view would read as a failed
  // search — with a Clear Search button that changes nothing.
  const emptyActiveView = view === "active" && query.length === 0;
  const categoryLabel = authCategoryLabel(category);

  /** Distinct-model stats (total discovered + verified-working) across ONE
   * provider's accounts, or null while the offerings read is unknown. */
  function providerModelStats(providerId: string): { total: number; working: number } | null {
    if (!offerings) return null;
    const providerAccounts = accountsByProvider.get(providerId) ?? [];
    const ids = new Set(providerAccounts.map((a) => a.id));
    const healthyIds = new Set(
      providerAccounts.filter((a) => a.display_status === "healthy").map((a) => a.id),
    );
    return distinctModelStats(
      offerings.filter((o) => ids.has(o.account_id) && healthyIds.has(o.account_id)),
    );
  }

  /** Distinct live models for ONE account, or null while either read is
   * unknown. Both reads must be present: without the accounts read there is
   * no health to judge, and reporting 0 then would claim "no models" for an
   * account that simply has not loaded yet. */
  function accountModelCount(accountId: string): number | null {
    if (!offeringsByAccount || !accounts) return null;
    const account = accounts.find((candidate) => candidate.id === accountId);
    if (!account || account.display_status !== "healthy") return 0;
    return distinctModelStats(offeringsByAccount.get(accountId) ?? []).total;
  }

  const reportAccount = reportAccountId
    ? (accounts.find((a) => a.id === reportAccountId) ?? null)
    : null;
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
          label="Working Models"
          value={modelStats ? modelStats.working : "—"}
          tone={modelStats ? undefined : "unknown"}
          icon="box"
          meta={
            modelStats
              ? `${modelStats.total} discovered`
              : offeringsError
                ? "offerings unavailable"
                : "loading…"
          }
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
            title={emptyActiveView ? `No active ${categoryLabel}` : "No integrations found"}
            description={
              emptyActiveView
                ? // Scoped to the tab: an owner with API-key accounts but no
                  // OAuth ones would be told "nothing is connected", which is
                  // false, if this claimed the whole fleet was empty.
                  `No ${categoryLabel} are connected yet. Choose All Integrations to browse the catalog.`
                : "Try adjusting your search terms or category filters."
            }
            action={
              emptyActiveView ? undefined : (
                <Button variant="secondary" size="sm" onClick={() => setSearch("")}>
                  Clear Search
                </Button>
              )
            }
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
          // Land the owner where the new account is actually visible. The
          // catalog GRID ("all") only shows integrations, never accounts, so
          // staying there after a successful connect hides the very thing
          // that was just created; the row LIST ("active") is where it
          // appears. Harmless when already on "active".
          onViewChange?.("active");
          reload();
          handleBurstRefresh();
        }}
      />

      {reportAccount ? (
        <ModelTestReport
          open
          account={reportAccount}
          providerName={
            reportProvider ? providerDisplayName(reportProvider) : reportAccount.provider
          }
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

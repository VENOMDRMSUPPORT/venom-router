import { useMemo, useState, useEffect } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { LuArrowUpRight, LuArrowRight, LuArrowDownUp, LuCircleAlert, LuActivity, LuCircleCheck, LuCpu, LuInfo, LuRefreshCw, LuSearchX, LuTriangleAlert, LuChevronDown, LuChevronUp } from 'react-icons/lu';
import { useCatalog } from '../../hooks/useCatalog';
import { present } from '../../api/presentation';
import { fetchCatalogNotifications, fetchChanges, formatTokens, formatAgo, type CatalogNotification, type CatalogData, type Change, type HealthResponse } from '../../api/client';
import { CATALOG_API_CONTRACT_VERSION } from '../../../config/api-contract';
import { FreshnessBadge } from '../../components/FreshnessBadge/FreshnessBadge';
import { Toolbar, PROVIDER_FILTERS } from '../../components/Toolbar/Toolbar';
import { FactState } from '../../components/FactState/FactState';
import { providerMatchesFilter } from '../../api/filters';
import { matchesProviderSearch } from '../../api/provider-search';
import { performanceRows, summarizePerformance, type PerformanceRow, type PerformanceSummary } from '../../api/performance';
import { buildMonitoringSignals, type MonitoringSeverity } from '../../api/monitoring';
import styles from './DashboardPage.module.css';

type ProviderSortKey = 'name' | 'models' | 'context' | 'score' | 'freshness';
type SortDirection = 'asc' | 'desc';
type ChangeWindow = '24h' | '7d' | '30d' | 'all';

const SORT_OPTIONS: { value: ProviderSortKey; label: string }[] = [
  { value: 'score', label: 'Overall score coverage' },
  { value: 'models', label: 'Live model count' },
  { value: 'context', label: 'Maximum context' },
  { value: 'freshness', label: 'Freshness' },
  { value: 'name', label: 'Provider name' },
];

export function DashboardPage() {
  const { data, error, loading, reload, health, healthError, healthLoading, revision } = useCatalog();
  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState('all');
  const [view, setView] = useState<'grid' | 'table'>('table');
  const [preferredView, setPreferredView] = useState<'grid' | 'table'>('table');
  const [sortKey, setSortKey] = useState<ProviderSortKey>('score');
  const [sortDirection, setSortDirection] = useState<SortDirection>('desc');
  const [monitoringOpen, setMonitoringOpen] = useState(true);
  const [changes, setChanges] = useState<Change[] | null>(null);
  const [changesError, setChangesError] = useState<string | null>(null);
  const [changesLoading, setChangesLoading] = useState(true);
  const [changesOpen, setChangesOpen] = useState(true);
  const [changeFilter, setChangeFilter] = useState<'all' | 'availability' | 'metadata' | 'score'>('all');
  const [changeWindow, setChangeWindow] = useState<ChangeWindow>('7d');
  const [changesNonce, setChangesNonce] = useState(0);
  const [notifications, setNotifications] = useState<CatalogNotification[]>([]);
  const [notificationsLoading, setNotificationsLoading] = useState(true);
  const [notificationsError, setNotificationsError] = useState<string | null>(null);
  const [notificationsNonce, setNotificationsNonce] = useState(0);

  const navigate = useNavigate();

  useEffect(() => {
    const ctrl = new AbortController();
    setChangesLoading(true);
    // The signal has to reach the request, not just guard the setState that
    // follows it: this controller was created and aborted while `fetchChanges`
    // was the one read in the client that took no signal, so the cleanup could
    // not actually cancel anything it started.
    fetchChanges(undefined, ctrl.signal)
      .then((result) => { if (!ctrl.signal.aborted) { setChanges(result.changes); setChangesError(null); } })
      .catch((reason) => { if (!ctrl.signal.aborted) setChangesError(reason instanceof Error ? reason.message : String(reason)); })
      .finally(() => { if (!ctrl.signal.aborted) setChangesLoading(false); });
    return () => ctrl.abort();
    // `revision` is a dependency, not decoration: the catalog reloads itself
    // when the change cursor moves, and a change feed that did not follow would
    // sit beside a refreshed roster describing the state before it.
  }, [changesNonce, revision]);

  const handleMonitoringRetry = () => {
    reload();
    setChangesNonce((value) => value + 1);
    setNotificationsNonce((value) => value + 1);
  };

  useEffect(() => {
    let cancelled = false;
    const readNotifications = () => {
      setNotificationsLoading(true);
      fetchCatalogNotifications()
        .then((result) => { if (!cancelled) { setNotifications(Array.isArray(result.notifications) ? result.notifications : []); setNotificationsError(null); } })
        .catch((reason) => { if (!cancelled) setNotificationsError(reason instanceof Error ? reason.message : String(reason)); })
        .finally(() => { if (!cancelled) setNotificationsLoading(false); });
    };
    readNotifications();
    const timer = window.setInterval(readNotifications, 30_000);
    return () => { cancelled = true; window.clearInterval(timer); };
  }, [notificationsNonce]);

  // Dynamic responsive view switcher: automatically adapt view based on window size
  useEffect(() => {
    const handleResize = () => {
      const isMobile = window.innerWidth < 768;
      if (isMobile) {
        setView('grid');
      } else {
        setView(preferredView);
      }
    };

    handleResize();
    window.addEventListener('resize', handleResize);
    return () => window.removeEventListener('resize', handleResize);
  }, [preferredView]);

  const handleViewChange = (newView: 'grid' | 'table') => {
    setView(newView);
    setPreferredView(newView);
  };

  const monitoringSignals = useMemo(() => buildMonitoringSignals({ data, health, healthError, healthLoading }), [data, health, healthError, healthLoading]);
  const recentChanges = useMemo(() => {
    if (!changes) return [];
    const matchesFilter = (change: Change) => {
      if (changeFilter === 'all') return true;
      if (changeFilter === 'availability') return ['added', 'readded', 'retired', 'became_missing', 'excluded'].includes(change.class);
      if (changeFilter === 'metadata') return ['price_changed', 'context_changed', 'capability_changed'].includes(change.class);
      return change.class.startsWith('quality_');
    };
    const windowMs: Record<ChangeWindow, number> = { '24h': 24 * 60 * 60 * 1000, '7d': 7 * 24 * 60 * 60 * 1000, '30d': 30 * 24 * 60 * 60 * 1000, all: Number.POSITIVE_INFINITY };
    const cutoff = Date.now() - windowMs[changeWindow];
    return changes
      .filter((change) => matchesFilter(change) && new Date(change.observedAt).getTime() >= cutoff)
      .sort((a, b) => new Date(b.observedAt).getTime() - new Date(a.observedAt).getTime())
      .slice(0, 6);
  }, [changes, changeFilter, changeWindow]);

  const providers = useMemo(() => {
    if (!data) return [];

    const modelsByProvider = new Map<string, typeof data.models>();
    for (const model of data.models) {
      const existing = modelsByProvider.get(model.providerId) ?? [];
      existing.push(model);
      modelsByProvider.set(model.providerId, existing);
    }

    const filtered = data.providers.filter((provider) => {
      const blurb = present(provider.id).blurb;
      return matchesProviderSearch(provider, data.models, blurb, query)
        && providerMatchesFilter(provider.id, data.models, filter);
    });

    const metric = (provider: typeof data.providers[number]): number | string => {
      const providerModels = modelsByProvider.get(provider.id) ?? [];
      if (sortKey === 'name') return provider.name.toLowerCase();
      if (sortKey === 'models') return provider.liveModels;
      if (sortKey === 'context') return Math.max(0, ...providerModels.map((model) => model.contextTokens ?? 0));
      if (sortKey === 'freshness') return provider.hoursSinceSuccess ?? Number.POSITIVE_INFINITY;
      return provider.liveModels > 0 ? provider.overallScoreScored / provider.liveModels : 0;
    };

    return filtered.sort((a, b) => {
      const left = metric(a);
      const right = metric(b);
      const comparison = typeof left === 'string' && typeof right === 'string'
        ? left.localeCompare(right)
        : Number(left) - Number(right);
      if (comparison !== 0) return sortDirection === 'asc' ? comparison : -comparison;
      return a.name.localeCompare(b.name);
    });
  }, [data, query, filter, sortKey, sortDirection]);

  /*
   * Above the guards, because a hook may not be conditional.
   *
   * These two sat after `if (loading && !data)` and `if (!data)`, so the loading
   * render called 42 hooks and the render that followed called 44. React answers
   * that with "Rendered more hooks than during the previous render" and tears the
   * tree down — a blank dashboard on every cold load, since the first render
   * never has data. Every test in the suite handed the component a finished
   * payload, so the empty path never ran and none of them saw it.
   *
   * Derived from `data` rather than the destructured `models`, which only exists
   * once the guards have passed. Both helpers accept an empty list.
   */
  const performanceSummary = useMemo(() => summarizePerformance(data?.models ?? []), [data]);
  const performanceList = useMemo(() => performanceRows(data?.models ?? []).slice(0, 5), [data]);

  if (loading && !data) return <DashboardSkeleton />;
  if (!data) {
    return (
      <div className={styles.errorState} role="alert">
        <div className={styles.errorContent}>
          <LuCircleAlert size={20} className={styles.errorIcon} aria-hidden="true" />
          <div>
            <h2 className={styles.errorTitle}>Catalog service unavailable</h2>
            <p className={styles.errorMessage}>
              We could not load the live catalog or its offline snapshot. Your data has not been changed.
            </p>
            {error && <code className={styles.errorDetail}>{error}</code>}
          </div>
        </div>
        <button type="button" className={styles.retryBtn} onClick={reload}>
          <LuRefreshCw size={14} aria-hidden="true" />
          Retry connection
        </button>
      </div>
    );
  }

  const { meta, models } = data;
  const maxContext = Math.max(0, ...models.map((m) => m.contextTokens ?? 0));
  const oldestSuccess = data.providers
    .map((p) => p.lastSuccessfulSyncAt)
    .filter((x): x is string => Boolean(x))
    .sort()[0] ?? null;
  const stale = data.providers.filter((p) => p.freshness !== 'fresh');

  return (
    <div>
      <header className={styles.hero}>
        <span className={styles.badge}>Enterprise Matrix 2026</span>
        <h1 className={styles.title}>AI Model Catalogs</h1>
        <p className={styles.subtitle}>
          Live model inventories for coding agents, read from each provider's own
          API and scored against published benchmarks. Every figure below states
          where it came from and how certain it is.
        </p>
        <div className={styles.heroMeta} aria-label="Catalog operational status">
          <span className={`${styles.heroStatus} ${data.origin === 'live' && stale.length === 0 ? styles.heroStatusLive : styles.heroStatusWarn}`}>
            <span className={styles.statusDot} aria-hidden="true" />
            {data.origin === 'live' ? stale.length === 0 ? 'Live catalog' : 'Live · attention needed' : 'Offline snapshot'}
          </span>
          <span className={styles.heroUpdated}>
            {data.origin === 'live' ? `Last provider sync ${formatAgo(oldestSuccess)}` : `Snapshot ${formatAgo(data.snapshotGeneratedAt ?? null)}`}
          </span>
        </div>
      </header>

      {/* Operational status, not a marketing claim. Each number is a fact the
          service can point at a timestamp for. */}
      <div className={styles.grid4}>
        <Kpi
          value={String(meta.catalogReady)}
          label="Catalog-ready models"
          hint={`Every operational fact resolved. ${meta.needsVerification} more are served by the providers but have an unresolved fact and are listed separately on their provider page.`}
        />
        <Kpi value={formatTokens(maxContext)} label="Max context" hint="Largest context window in the catalog right now." />
        <Kpi
          value={`${meta.overallScoreScored}/${meta.liveModels}`}
          label="Complete overall scores"
          hint={`Operational data is available for ${meta.operationalScored}/${meta.liveModels} models. ${meta.liveModels - meta.overallScoreScored} model(s) do not yet have a complete overall-score-v1 evaluation. Provider availability and metadata do not substitute for reproducible task, speed, and cost evidence.`}
        />
        {/* The identity breakdown arrives as one answer or not at all, so one
            null here means the whole tile is unknown. It used to interpolate
            `undefined` straight into the page — "undefined/116" — which is the
            same failure as a fabricated zero wearing a worse costume. */}
        <Kpi
          value={meta.identity.resolved === null ? null : `${meta.identity.resolved}/${meta.liveModels}`}
          label="Identity proven"
          hint={
            meta.identity.resolved === null
              ? 'This catalog service response did not carry the identity breakdown — unknown, not zero. Reload once the service is back on the current contract.'
              : `${meta.identity.identityReview} are in identity review — candidates were examined and refused, with the evidence recorded — and ${meta.identity.unresolved} matched nothing upstream at all. The three states are exclusive and sum to every live model. Identity is a separate question from quality: a proven identity does not imply a benchmark exists.`
          }
        />
        <Kpi
          value={data.origin === 'live' ? formatAgo(oldestSuccess) : 'snapshot'}
          label={data.origin === 'live' ? 'Oldest provider sync' : 'Offline fallback'}
          hint={
            data.origin === 'live'
              ? 'The least recently synced provider. Freshness is per provider and shown on each card.'
              : `The service is unreachable, so this page is showing a snapshot generated ${formatAgo(data.snapshotGeneratedAt ?? null)}.`
          }
        />
      </div>

      {/*
        The three things the catalog now knows and used to render as an em dash.
        They sit on their own row, above the fold, because they are the reason a
        cell is empty — and a reader who cannot see them concludes the data is
        simply complete.
      */}
      <div className={styles.grid4} data-testid="truth-tiles">
        <Kpi
          value={meta.conflictedModels === null ? null : String(meta.conflictedModels)}
          label="Models with source conflicts"
          hint={
            meta.conflictedModels === null
              ? 'This catalog service response did not carry this figure — unknown, not zero. A zero here would say the catalog compared its sources and found no disagreement, which is a different fact.'
              : "Sources contradicted each other on at least one field, so no value was taken and both sides are kept. Open any model's evidence to see who said what. A quietly picked winner is indistinguishable from a bug."
          }
        />
        <Kpi
          value={String(meta.needsVerification)}
          label="Models with a missing fact"
          hint="At least one operational fact is published by nobody. These models are still served and listed — with the exact field named — rather than hidden or padded with a plausible default."
        />
        <Kpi
          // `identityDetail` can be absent from a stale server's response even
          // though `CatalogMeta` declares it required — the type is a promise
          // about the current contract, not a guarantee about every payload
          // that will ever arrive over the wire. `undefined` here must render
          // as "unknown", never as the fabricated claim "0": zero says the
          // catalog looked and found none, which is a different fact from
          // "this response didn't say".
          value={meta.identityDetail ? String(meta.identityDetail.rejectedCandidates) : null}
          label="Identity candidates refused"
          hint={
            meta.identityDetail
              ? `Upstream candidates that were examined and refused on evidence, across ${meta.identityDetail.withRejectedCandidates} models. Each one records why and against what evidence, so "identity review" means investigated rather than untouched.`
              : 'This catalog service response did not include this figure — unknown, not zero. Reload once the service is back on the current contract.'
          }
        />
        <Kpi
          value={`${meta.unrated}`}
          label="Unrated, with a recorded reason"
          hint="Every model without a quality score carries a machine-readable reason: identity unresolved, identity ambiguous, no published benchmark, or a vendor the calibration was measured to have no predictive power for. None of these prevents a model from being operationally complete."
        />
      </div>

      {error && (
        <div className={styles.warnBar} role="alert" aria-live="assertive">
          <span>
            Refresh failed. Showing the last known catalog until the service responds again.
            <code className={styles.warnDetail}>{error}</code>
          </span>
          <button type="button" className={styles.warnAction} onClick={reload}>
            <LuRefreshCw size={13} aria-hidden="true" />
            Retry
          </button>
        </div>
      )}

      {data.origin === 'snapshot' && (
        <div className={styles.warnBar} role="status" aria-live="polite">
          Showing a build-time snapshot — the catalog service is not reachable, so
          these figures may be out of date.
        </div>
      )}
      {stale.length > 0 && data.origin === 'live' && (
                <div className={styles.warnBar} role="status" aria-live="polite">
          {stale.length} provider{stale.length > 1 ? 's have' : ' has'} not synced
          successfully in a while: {stale.map((p) => p.name).join(', ')}.
        </div>
      )}

      <MonitoringPanel
        signals={monitoringSignals}
        loading={Boolean(healthLoading && !health)}
        open={monitoringOpen}
        onToggle={() => setMonitoringOpen((value) => !value)}
        onRetry={handleMonitoringRetry}
      />

      <RuntimeSettingsPanel health={health ?? null} error={healthError ?? null} loading={Boolean(healthLoading && !health)} />

      <PerformancePanel summary={performanceSummary} rows={performanceList} />

      <NotificationHistory
        notifications={notifications}
        loading={notificationsLoading}
        error={notificationsError}
        onRetry={handleMonitoringRetry}
      />

      <VersionHistoryPanel
        data={data}
        changes={recentChanges}
        totalChanges={changes?.length ?? 0}
        loading={changesLoading}
        error={changesError}
        open={changesOpen}
        filter={changeFilter}
        onFilterChange={setChangeFilter}
        changeWindow={changeWindow}
        onChangeWindow={setChangeWindow}
        onToggle={() => setChangesOpen((value) => !value)}
        onRetry={handleMonitoringRetry}
      />

      {/* This page lists PROVIDERS, so it takes the provider filters. The model
          filters reached it only as a default, which is how "Not Deprecated"
          arrived with no branch to act on and no effect a reader could see. */}
      <Toolbar
        query={query}
        onQueryChange={setQuery}
        filter={filter}
        onFilterChange={setFilter}
        options={PROVIDER_FILTERS}
        placeholder="Search providers, models, or IDs..."
        searchHintId="dashboard-search-hint"
        view={view}
        onViewChange={handleViewChange}
      />

      <div className={styles.resultBar}>
        <div className={styles.resultSummary} role="status" aria-live="polite">
          Showing <strong>{providers.length}</strong> of <strong>{data.providers.length}</strong> providers
          {query.trim() && <span> matching “{query.trim()}”</span>}
        </div>
        <div className={styles.sortControls}>
          <label htmlFor="provider-sort">Sort by</label>
          <select
            id="provider-sort"
            value={sortKey}
            onChange={(event) => {
              const nextKey = event.target.value as ProviderSortKey;
              setSortKey(nextKey);
              setSortDirection(nextKey === 'name' || nextKey === 'freshness' ? 'asc' : 'desc');
            }}
          >
            {SORT_OPTIONS.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
          </select>
          <button
            type="button"
            className={styles.sortDirectionBtn}
            onClick={() => setSortDirection((current) => current === 'asc' ? 'desc' : 'asc')}
            aria-label={`Sort ${sortDirection === 'asc' ? 'descending' : 'ascending'}`}
            title={`Currently ${sortDirection === 'asc' ? 'ascending' : 'descending'}`}
          >
            <LuArrowDownUp size={14} aria-hidden="true" />
            <span>{sortDirection === 'asc' ? 'Ascending' : 'Descending'}</span>
          </button>
        </div>
        <span id="dashboard-search-hint" className={styles.searchHint}>
          Advanced search: <code>model:</code> <code>capability:</code> <code>status:</code> <code>score:</code>
        </span>
      </div>

      {view === 'grid' ? (
        <div className={styles.grid}>
          {providers.map((p) => {
            const pres = present(p.id);
            const mine = models.filter((m) => m.providerId === p.id);
            const free = mine.filter((m) => m.pricing.isFree === true).length;
            const maxCtx = Math.max(0, ...mine.map((m) => m.contextTokens ?? 0));
            const topModels = mine.slice(0, 3);
            const operationalReady = mine.filter((m) => m.vo.value !== null).length;
            const scoredRatio = p.liveModels > 0 ? Math.round((p.overallScoreScored / p.liveModels) * 100) : 0;

            return (
              <Link key={p.id} to={`/provider/${p.id}`} className={styles.card}>
                <div className={styles.cardHeader}>
                  <div className={styles.logoBox}>
                    {pres.logo ? (
                      <img
                        src={pres.logo}
                        alt=""
                        className={`${styles.logo} ${pres.invertInDark ? 'logo-invert-dark' : ''}`}
                      />
                    ) : (
                      <LuCpu size={22} className={styles.fallbackLogo} />
                    )}
                  </div>

                  <div className={styles.cardTitleBox}>
                    <div className={styles.nameRow}>
                      <h3 className={styles.name}>{p.name}</h3>
                      <LuArrowUpRight size={15} className={styles.arrow} />
                    </div>
                    <FreshnessBadge provider={p} compact />
                  </div>
                </div>

                <p className={styles.tagline}>{pres.blurb}</p>

                {topModels.length > 0 && (
                  <div className={styles.modelChips}>
                    {topModels.map((m) => (
                      <span key={`${m.providerId}/${m.modelId}`} className={styles.modelChip}>
                        {m.displayName || m.modelId}
                      </span>
                    ))}
                    {mine.length > 3 && (
                      <span className={styles.moreChip}>+{mine.length - 3} more</span>
                    )}
                  </div>
                )}

                <div className={styles.progressSection}>
                  <div className={styles.progressMeta}>
                    <span className={styles.progressLabel}>Complete overall-score-v1</span>
                    <span className={styles.progressPercent}>{scoredRatio}% ({p.overallScoreScored}/{p.liveModels})</span>
                  </div>
                  <div className={styles.progressBarBg}>
                    <div
                      className={styles.progressBarFill}
                      style={{ width: `${scoredRatio}%` }}
                    />
                  </div>
                  <span className={styles.progressSupport}>
                    Operational data {operationalReady}/{p.liveModels}; provider sync is a separate status.
                  </span>
                </div>

                <div className={styles.stats}>
                  <div className={styles.statBox}>
                    <span className={styles.statValue}>{p.liveModels}</span>
                    <span className={styles.statLabel}>Models</span>
                  </div>
                  <div className={styles.statBox}>
                    <span className={styles.statValue}>{formatTokens(maxCtx)}</span>
                    <span className={styles.statLabel}>Max Ctx</span>
                  </div>
                  <div className={styles.statBox}>
                    <span className={styles.statValue}>{p.overallScoreScored}</span>
                    <span className={styles.statLabel}>Final scores</span>
                  </div>
                  <div className={`${styles.statBox} ${free > 0 ? styles.freeBox : ''}`}>
                    <span className={styles.statValue}>{free > 0 ? free : '—'}</span>
                    <span className={styles.statLabel}>Free</span>
                  </div>
                </div>

                <div className={styles.cardFooterAction}>
                  <span>Explore Catalog Roster</span>
                  <LuArrowRight size={14} className={styles.footerArrow} />
                </div>
              </Link>
            );
          })}
        </div>
      ) : (
        <div className={styles.tableWrap}>
          <div className={styles.tableScroll}>
            <table className={styles.table}>
              <caption className={styles.srOnly}>Catalog providers and operational coverage</caption>
              <thead>
                <tr>
                  <th>Provider</th>
                  <th>Sync Status</th>
                  <th className={styles.num}>Live Models</th>
                  <th className={styles.num}>Max Context</th>
                  <th className={styles.num} title="Models with complete overall-score-v1 task, speed, and cost evidence.">Final score</th>
                  <th className={styles.num} title="Models with a complete legacy operational-fit projection. This is independent of the final evaluation.">Operational data</th>
                  <th className={styles.num}>Free Models</th>
                  <th className={styles.num}>Action</th>
                </tr>
              </thead>
              <tbody>
                {providers.map((p) => {
                  const pres = present(p.id);
                  const mine = models.filter((m) => m.providerId === p.id);
                  const free = mine.filter((m) => m.pricing.isFree === true).length;
                  const maxCtx = Math.max(0, ...mine.map((m) => m.contextTokens ?? 0));
                  const operationalReady = mine.filter((m) => m.vo.value !== null).length;
                  const scoredRatio = p.liveModels > 0 ? Math.round((p.overallScoreScored / p.liveModels) * 100) : 0;
                  return (
                    <tr
                      key={p.id}
                      className={styles.tableRow}
                      onClick={() => navigate(`/provider/${p.id}`)}
                      onKeyDown={(event) => {
                        if (event.key === 'Enter' || event.key === ' ') {
                          event.preventDefault();
                          navigate(`/provider/${p.id}`);
                        }
                      }}
                      tabIndex={0}
                      role="link"
                      aria-label={`View ${p.name} provider roster`}
                    >
                      <td>
                        <div className={styles.providerCell}>
                          {pres.logo && (
                            <img src={pres.logo} alt="" className={`${styles.providerCellLogo} ${pres.invertInDark ? 'logo-invert-dark' : ''}`} />
                          )}
                          <div>
                            <div className={styles.providerCellName}>{p.name}</div>
                            <div className={styles.providerCellBlurb}>{pres.blurb}</div>
                          </div>
                        </div>
                      </td>
                      <td>
                        <FreshnessBadge provider={p} compact />
                      </td>
                      <td className={styles.num}>{p.liveModels}</td>
                      <td className={styles.num}>{formatTokens(maxCtx)}</td>
                      <td className={styles.num}>
                        <div className={styles.tableProgressCell}>
                          <div className={styles.tableProgressValue}>
                            {scoredRatio}% <span className={styles.tableProgressRatio}>({p.overallScoreScored}/{p.liveModels})</span>
                          </div>
                          <div className={styles.tableProgressBarBg}>
                            <div
                              className={styles.tableProgressBarFill}
                              style={{ width: `${scoredRatio}%` }}
                            />
                          </div>
                        </div>
                      </td>
                      <td className={styles.num}>{operationalReady}/{p.liveModels}</td>
                      <td className={styles.num}>{free > 0 ? free : '—'}</td>
                      <td className={styles.num}>
                        <div className={styles.actionCell} title="View provider roster">
                          <button type="button" className={styles.actionBtn} aria-label={`View ${p.name} provider roster`}>
                            <LuArrowUpRight size={15} />
                          </button>
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {providers.length === 0 && (
        <div className={styles.empty} role="status">
          <LuSearchX size={24} className={styles.emptyIcon} aria-hidden="true" />
          <strong className={styles.emptyTitle}>No providers match this view</strong>
          <p className={styles.emptyMessage}>
            Try a broader term or remove the active filter. Search also checks model names and supports qualified queries.
          </p>
          {(query || filter !== 'all') && (
            <button type="button" className={styles.emptyAction} onClick={() => { setQuery(''); setFilter('all'); }}>
              Clear search and filters
            </button>
          )}
        </div>
      )}

      <section className={styles.method}>
        <h2 className={styles.methodTitle}>How these numbers are produced</h2>
        <ul className={styles.methodList}>
          <li>
            <strong>Which models exist</strong> comes from each provider's own live
            API. That is the only source that can show an addition or a removal.
          </li>
          <li>
            <strong>What each model is</strong> — context, output, capabilities,
            price — comes from <code>models.dev</code>.
          </li>
          <li>
            <strong>Quality (VQ)</strong> is a published benchmark figure where one
            exists for that exact model, or a value converted from a second
            benchmark with its measured error attached. Models with neither are
            shown as <em>Unrated</em>, never as zero.
          </li>
          <li>
            <strong>Operational fit (VO)</strong> is derived from published facts
            only, under the <code>{meta.profileId}</code> profile of{' '}
            <code>{meta.methodologyVersion}</code>.
          </li>
          <li>
            <strong>Overall-score-v1</strong> is complete only after accepted task,
            speed, and cost evidence exists. A successful provider request proves
            availability; it does not produce a benchmark score.
          </li>
          {meta.calibration && (
            <li>
              The current calibration was fitted on {meta.calibration.n} models
              (ρ&nbsp;={meta.calibration.rho.toFixed(3)}, error&nbsp;±
              {meta.calibration.looRmse.toFixed(1)})
              {meta.calibration.excludedGroups.length > 0 && (
                <> and excludes {meta.calibration.excludedGroups.join(', ')}, where the two benchmarks measurably disagree</>
              )}
              .
            </li>
          )}
        </ul>
      </section>

      <footer className={styles.footer}>
        <span>Venom Router Catalog</span>
        <span className={styles.footerDot}>·</span>
        <span>{meta.methodologyVersion}</span>
        <span className={styles.footerDot}>·</span>
        <Link to="/changes" className={styles.footerLink}>What's new</Link>
      </footer>
    </div>
  );
}

function PerformancePanel({ summary, rows }: { summary: PerformanceSummary; rows: PerformanceRow[] }) {
  const bestThroughput = summary.bestThroughput?.performance.outputTokensPerSecondMedian ?? null;
  const formatSeconds = (value: number | null) => value === null ? '—' : `${value.toFixed(2)}s`;
  const formatRate = (value: number | null) => value === null ? '—' : `${(value * 100).toFixed(1)}%`;

  return (
    <section className={styles.performancePanel} aria-labelledby="performance-panel-title">
      <div className={styles.performanceHeader}>
        <div>
          <span className={styles.monitoringEyebrow}>Measured runtime evidence</span>
          <h2 id="performance-panel-title" className={styles.performanceTitle}>Latency & model performance</h2>
          <p className={styles.performanceDescription}>Stored speed-probe aggregates from Catalog evaluations. Unmeasured models are not ranked or treated as slow.</p>
        </div>
        <span className={styles.performanceCoverage}>{summary.measuredModels}/{summary.totalModels} measured</span>
      </div>

      <div className={styles.performanceKpis} role="group" aria-label="Performance summary">
        <div className={styles.performanceKpi}><strong>{formatSeconds(summary.medianTtftSeconds)}</strong><span>Median TTFT</span></div>
        <div className={styles.performanceKpi}><strong>{summary.medianOutputTokensPerSecond === null ? '—' : `${summary.medianOutputTokensPerSecond.toFixed(1)}`}</strong><span>Median tokens/s</span></div>
        <div className={styles.performanceKpi}><strong>{formatSeconds(summary.medianEndToEndP95Seconds)}</strong><span>Median p95 response</span></div>
        <div className={styles.performanceKpi}><strong>{formatRate(summary.averageSuccessRate)}</strong><span>Average success</span></div>
      </div>

      {rows.length === 0 ? (
        <div className={styles.performanceEmpty} role="status">
          <LuActivity size={20} aria-hidden="true" />
          <div><strong>No measured performance data</strong><p>Speed evaluations have not produced complete TTFT, throughput, and response-time samples for the current catalog.</p></div>
        </div>
      ) : (
        <div className={styles.performanceTableWrap}>
          <table className={styles.performanceTable}>
            <caption className={styles.srOnly}>Top measured models by median output throughput</caption>
            <thead><tr><th>Model</th><th>Median TTFT</th><th>Tokens/s</th><th>Response p95</th><th>Success</th></tr></thead>
            <tbody>
              {rows.map(({ model, performance }) => {
                const throughput = performance.outputTokensPerSecondMedian ?? 0;
                const barWidth = bestThroughput ? Math.max(4, Math.round((throughput / bestThroughput) * 100)) : 0;
                return (
                  <tr key={`${model.providerId}/${model.modelId}`}>
                    <td><Link to={`/provider/${encodeURIComponent(model.providerId)}?model=${encodeURIComponent(model.modelId)}`} className={styles.performanceModelLink}>{model.displayName || model.modelId}</Link><span className={styles.performanceProvider}>{model.providerId}</span></td>
                    <td>{formatSeconds(performance.ttftMedianSeconds)}</td>
                    <td><div className={styles.performanceMetric}><span>{throughput.toFixed(1)}</span><span className={styles.performanceBar} style={{ width: `${barWidth}%` }} aria-hidden="true" /></div></td>
                    <td>{formatSeconds(performance.endToEndP95Seconds)}</td>
                    <td>{formatRate(performance.successRate)}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      <p className={styles.performanceFootnote}>Based on {summary.successfulSamples}/{summary.sampleCount} complete speed samples. A blank metric means the server did not publish a complete measurement.</p>
    </section>
  );
}

function NotificationHistory({
  notifications,
  loading,
  error,
  onRetry,
}: {
  notifications: CatalogNotification[];
  loading: boolean;
  error: string | null;
  onRetry: () => void;
}) {
  const unreadCount = notifications.filter((notification) => notification.readAt === null).length;
  const categoryLabel = (category: CatalogNotification['category']) => category === 'success' ? 'Added' : category === 'error' ? 'Removed' : 'Needs attention';

  return (
    <section className={styles.alertCenter} aria-labelledby="alert-center-title">
      <div className={styles.alertCenterHeader}>
        <div>
          <span className={styles.monitoringEyebrow}>Recorded catalog events</span>
          <h2 id="alert-center-title" className={styles.alertCenterTitle}>Notification history <span className={styles.alertCount}>{unreadCount} unread</span></h2>
        </div>
        <button type="button" className={styles.monitoringAction} onClick={onRetry} disabled={loading}>Refresh notifications</button>
      </div>

      {error && <div className={styles.alertCenterError} role="alert"><span>{error}</span><button type="button" className={styles.monitoringAction} onClick={onRetry}>Retry</button></div>}
      {loading && <div className={styles.alertCenterState} aria-busy="true">Loading notification history…</div>}
      {!loading && !error && notifications.length === 0 && <div className={styles.alertCenterState}>No catalog notifications have been recorded.</div>}
      {!loading && notifications.length > 0 && (
        <ul className={styles.alertList}>
          {notifications.map((notification) => (
            <li key={notification.id} className={styles.alertItem}>
              <div className={styles.alertItemMain}>
                <div className={styles.alertItemTopline}>
                  <span className={styles.alertSeverity}>{categoryLabel(notification.category)}</span>
                  <span className={styles.alertStatus}>{notification.readAt === null ? 'Unread' : 'Read'}</span>
                  <time dateTime={notification.observedAt}>{formatAgo(notification.observedAt)}</time>
                </div>
                <strong>{notification.title}</strong>
                <span className={styles.alertDetail}>{notification.detail}</span>
                {(notification.providerId || notification.modelId) && (
                  <span className={styles.alertTargets}>
                    {notification.providerId && <Link to={`/provider/${encodeURIComponent(notification.providerId)}`} className={styles.alertTargetLink}>{notification.providerId}</Link>}
                    {notification.modelId && <Link to={`/provider/${encodeURIComponent(notification.providerId ?? '')}?model=${encodeURIComponent(notification.modelId)}`} className={styles.alertTargetLink}>{notification.modelId}</Link>}
                  </span>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function RuntimeSettingsPanel({ health, error, loading }: { health: HealthResponse | null | undefined; error: string | null | undefined; loading: boolean }) {
  const schedule = health?.service.nextScheduledRunAt;
  const nextRun = schedule
    ? new Date(schedule).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })
    : 'Not reported';

  const rows = [
    ['Service status', health?.service.status === 'up' ? 'Up' : health?.service.status === 'degraded' ? 'Degraded' : 'Not reported'],
    ['Database readable', health ? health.service.databaseReadable ? 'Yes' : 'No' : 'Not reported'],
    ['Scheduler', health ? health.service.schedulerEnabled ? 'Enabled' : 'Disabled' : 'Not reported'],
    ['Next scheduled sync', nextRun],
    ['Freshness policy', health ? `${health.catalog.staleAfterHours} hours` : 'Not reported'],
    ['Catalog state', health?.catalog.status === 'current' ? 'Current' : health?.catalog.status === 'stale' ? 'Stale' : 'Not reported'],
  ];

  return (
    <section className={styles.settingsPanel} aria-labelledby="runtime-settings-title">
      <div className={styles.settingsHeader}>
        <div>
          <span className={styles.monitoringEyebrow}>Read-only configuration</span>
          <h2 id="runtime-settings-title" className={styles.settingsTitle}>Catalog runtime settings</h2>
        </div>
        <span className={styles.settingsSource}>Source: /v1/health</span>
      </div>
      {loading && <div className={styles.settingsState} aria-busy="true">Reading runtime settings…</div>}
      {error && !health && <div className={styles.settingsState} role="status">Runtime settings unavailable until the health endpoint responds.</div>}
      {!loading && !error && health && (
        <dl className={styles.settingsGrid}>
          {rows.map(([label, value]) => (
            <div key={label} className={styles.settingRow}>
              <dt>{label}</dt>
              <dd>{value}</dd>
            </div>
          ))}
        </dl>
      )}
      {!loading && !health && !error && <div className={styles.settingsState}>The API did not report runtime settings.</div>}
    </section>
  );
}

const CHANGE_LABELS: Record<string, string> = {
  added: 'Added', readded: 'Back', retired: 'Retired', became_missing: 'Missing', excluded: 'Excluded',
  price_changed: 'Price', context_changed: 'Context', capability_changed: 'Capability',
  quality_became_available: 'Now scored', quality_evidence_upgraded: 'Better evidence',
  quality_changed: 'Score moved', quality_lost: 'Score withdrawn',
};

function VersionHistoryPanel({
  data,
  changes,
  totalChanges,
  loading,
  error,
  open,
  filter,
  onFilterChange,
  changeWindow,
  onChangeWindow,
  onToggle,
  onRetry,
}: {
  data: CatalogData;
  changes: Change[];
  totalChanges: number;
  loading: boolean;
  error: string | null;
  open: boolean;
  filter: 'all' | 'availability' | 'metadata' | 'score';
  onFilterChange: (filter: 'all' | 'availability' | 'metadata' | 'score') => void;
  changeWindow: ChangeWindow;
  onChangeWindow: (window: ChangeWindow) => void;
  onToggle: () => void;
  onRetry: () => void;
}) {
  const filterOptions: { value: typeof filter; label: string }[] = [
    { value: 'all', label: 'All changes' },
    { value: 'availability', label: 'Availability' },
    { value: 'metadata', label: 'Metadata' },
    { value: 'score', label: 'Scoring' },
  ];

  return (
    <section className={styles.historyPanel} aria-labelledby="version-history-title">
      <div className={styles.historyHeader}>
        <div className={styles.historyTitleGroup}>
          <span className={styles.historyIcon} aria-hidden="true"><LuRefreshCw size={15} /></span>
          <div>
            <span className={styles.monitoringEyebrow}>Version control</span>
            <h2 id="version-history-title" className={styles.historyTitle}>Catalog release & change history</h2>
          </div>
        </div>
        <div className={styles.historyHeaderActions}>
          <Link to="/changes" className={styles.historyLink}>Open full history <LuArrowUpRight size={13} aria-hidden="true" /></Link>
          <button type="button" className={styles.monitoringToggle} onClick={onToggle} aria-expanded={open} aria-controls="version-history-content">
            {open ? 'Hide details' : 'Show details'}
            {open ? <LuChevronUp size={14} aria-hidden="true" /> : <LuChevronDown size={14} aria-hidden="true" />}
          </button>
        </div>
      </div>

      <div className={styles.releaseMeta} aria-label="Current Catalog versions">
        <span className={styles.releaseBadge}><strong>Catalog</strong> {data.meta.methodologyVersion}</span>
        <span className={styles.releaseBadge}><strong>Scoring</strong> {data.meta.scoringPolicy.methodologyVersion ?? 'not reported'}</span>
        <span className={styles.releaseBadge}><strong>API</strong> {CATALOG_API_CONTRACT_VERSION}</span>
        <span className={styles.releaseOrigin}>{data.origin === 'live' ? 'Live source' : 'Offline snapshot'}</span>
      </div>

      {open && (
        <div id="version-history-content" className={styles.historyContent}>
          <div className={styles.historyToolbar}>
            <span className={styles.historySummary} role="status" aria-live="polite">
              {loading ? 'Loading change history…' : `${totalChanges} recorded change${totalChanges === 1 ? '' : 's'} · showing ${changeWindow === 'all' ? 'all time' : `last ${changeWindow}`}`}
            </span>
            <div className={styles.historyToolbarControls}>
              <label className={styles.historyWindowLabel} htmlFor="change-window">Window</label>
              <select id="change-window" className={styles.historyWindow} value={changeWindow} onChange={(event) => onChangeWindow(event.target.value as ChangeWindow)}>
                <option value="24h">Last 24 hours</option>
                <option value="7d">Last 7 days</option>
                <option value="30d">Last 30 days</option>
                <option value="all">All time</option>
              </select>
              <div className={styles.historyFilters} role="group" aria-label="Change history filters">
              {filterOptions.map((option) => (
                <button key={option.value} type="button" className={`${styles.historyFilter} ${filter === option.value ? styles.historyFilterActive : ''}`} aria-pressed={filter === option.value} onClick={() => onFilterChange(option.value)}>
                  {option.label}
                </button>
              ))}
              </div>
            </div>
          </div>

          {loading && <div className={styles.historyLoading} aria-busy="true">Reading the Catalog change log…</div>}
          {error && !loading && (
            <div className={styles.historyError} role="alert">
              <span>Change history is unavailable right now.</span>
              <button type="button" className={styles.monitoringAction} onClick={onRetry}>Retry</button>
            </div>
          )}
          {!loading && !error && changes.length === 0 && <div className={styles.historyEmpty}>No changes recorded for this category.</div>}
          {!loading && !error && changes.length > 0 && (
            <ol className={styles.historyList}>
              {changes.map((change, index) => (
                <li key={`${change.providerId}/${change.modelId}/${change.class}/${change.observedAt}/${index}`} className={styles.historyItem}>
                  <span className={styles.historyMarker} aria-hidden="true" />
                  <div className={styles.historyItemBody}>
                    <div className={styles.historyItemTopline}>
                      <span className={styles.historyTag}>{CHANGE_LABELS[change.class] ?? change.class}</span>
                      <time dateTime={change.observedAt}>{formatAgo(change.observedAt)}</time>
                    </div>
                    <Link to={`/provider/${encodeURIComponent(change.providerId)}`} className={styles.historyProviderLink}>{change.providerId}</Link>
                    <Link to={`/provider/${encodeURIComponent(change.providerId)}?model=${encodeURIComponent(change.modelId)}`} className={styles.historyModelLink}>{change.modelId}</Link>
                    {change.from !== null && change.to !== null && <span className={styles.historyDelta}>{change.from} → {change.to}{change.field ? ` · ${change.field}` : ''}</span>}
                    {change.note && change.from === null && <span className={styles.historyNote}>{change.note}</span>}
                  </div>
                </li>
              ))}
            </ol>
          )}
        </div>
      )}
    </section>
  );
}

function MonitoringPanel({
  signals,
  loading,
  open,
  onToggle,
  onRetry,
}: {
  signals: ReturnType<typeof buildMonitoringSignals>;
  loading: boolean;
  open: boolean;
  onToggle: () => void;
  onRetry: () => void;
}) {
  const mostUrgent = signals.find((signal) => signal.severity === 'critical')
    ?? signals.find((signal) => signal.severity === 'warning')
    ?? signals[0];
  const panelSeverity: MonitoringSeverity = mostUrgent?.severity ?? (loading ? 'info' : 'success');

  return (
    <section className={`${styles.monitoringPanel} ${styles[`monitoring-${panelSeverity}`]}`} aria-labelledby="monitoring-title">
      <div className={styles.monitoringHeader}>
        <div className={styles.monitoringTitleGroup}>
          <span className={styles.monitoringIcon} aria-hidden="true"><LuActivity size={16} /></span>
          <div>
            <span className={styles.monitoringEyebrow}>Operational monitor</span>
            <h2 id="monitoring-title" className={styles.monitoringTitle}>Catalog health</h2>
          </div>
        </div>
        <div className={styles.monitoringHeaderActions}>
          <span className={styles.monitoringSummary} role="status" aria-live="polite">
            {loading ? 'Checking service…' : mostUrgent?.severity === 'success' ? 'All systems nominal' : `${signals.length} active signal${signals.length === 1 ? '' : 's'}`}
          </span>
          <button type="button" className={styles.monitoringToggle} onClick={onToggle} aria-expanded={open} aria-controls="monitoring-signals">
            {open ? 'Hide details' : 'Show details'}
            {open ? <LuChevronUp size={14} aria-hidden="true" /> : <LuChevronDown size={14} aria-hidden="true" />}
          </button>
        </div>
      </div>

      {open && (
        <div id="monitoring-signals" className={styles.monitoringSignals}>
          {loading ? (
            <div className={styles.monitoringLoading}><span className={styles.monitoringPulse} /> Waiting for the Catalog health endpoint…</div>
          ) : signals.map((signal) => (
            <article key={signal.id} className={`${styles.monitoringSignal} ${styles[`signal-${signal.severity}`]}`} role={signal.severity === 'critical' || signal.severity === 'warning' ? 'alert' : 'status'} data-testid={`monitoring-signal-${signal.id}`}>
              <SignalIcon severity={signal.severity} />
              <div className={styles.monitoringSignalBody}>
                <strong>{signal.title}</strong>
                <p>{signal.detail}</p>
              </div>
              {signal.action === 'retry' && (
                <button type="button" className={styles.monitoringAction} onClick={onRetry}>Retry</button>
              )}
              {signal.action === 'changes' && (
                <Link to="/changes" className={styles.monitoringAction}>View changes</Link>
              )}
            </article>
          ))}
        </div>
      )}
    </section>
  );
}

function SignalIcon({ severity }: { severity: MonitoringSeverity }) {
  if (severity === 'critical') return <LuCircleAlert size={17} className={styles.signalIcon} aria-hidden="true" />;
  if (severity === 'warning') return <LuTriangleAlert size={17} className={styles.signalIcon} aria-hidden="true" />;
  if (severity === 'success') return <LuCircleCheck size={17} className={styles.signalIcon} aria-hidden="true" />;
  return <LuInfo size={17} className={styles.signalIcon} aria-hidden="true" />;
}

function DashboardSkeleton() {
  return (
    <div className={styles.loadingState} aria-busy="true" aria-label="Loading catalog">
      <div className={styles.loadingHeader}>
        <span className={styles.skeletonBadge} />
        <span className={styles.skeletonTitle} />
        <span className={styles.skeletonText} />
      </div>
      <div className={styles.grid4} aria-hidden="true">
        {Array.from({ length: 4 }, (_, index) => <div key={index} className={styles.skeletonKpi} />)}
      </div>
      <div className={styles.skeletonTable} aria-hidden="true">
        <span className={styles.skeletonTableHeader} />
        {Array.from({ length: 5 }, (_, index) => <span key={index} className={styles.skeletonTableRow} />)}
      </div>
      <p className={styles.loadingLabel}>Loading catalog…</p>
    </div>
  );
}

/** `value === null` means the figure is unknown, not zero — rendered with the
 * same "missing" chip the rest of the catalog uses for a fact nobody sent. */
function Kpi({ value, label, hint }: { value: string | null; label: string; hint: string }) {
  return (
    <div className={styles.kpi} title={hint}>
      {value === null ? (
        <span className={styles.kpiValue}>
          <FactState state="missing" />
        </span>
      ) : (
        <span className={styles.kpiValue}>{value}</span>
      )}
      <span className={styles.kpiLabel}>{label}</span>
    </div>
  );
}

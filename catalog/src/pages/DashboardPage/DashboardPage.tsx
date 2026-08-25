import { useMemo, useState, useEffect } from 'react';
import { Link } from 'react-router-dom';
import { LuArrowUpRight, LuCircleAlert, LuActivity, LuCircleCheck, LuChevronLeft, LuChevronRight, LuCpu, LuInfo, LuRefreshCw, LuTriangleAlert, LuChevronDown, LuChevronUp } from 'react-icons/lu';
import { useCatalog } from '../../hooks/useCatalog';
import { present } from '../../api/presentation';
import { fetchCatalogNotifications, fetchChanges, formatTokens, formatAgo, type ApiModel, type CatalogNotification, type CatalogData, type Change } from '../../api/client';
import { CATALOG_API_CONTRACT_VERSION } from '../../../config/api-contract';
import { FreshnessBadge } from '../../components/FreshnessBadge/FreshnessBadge';
import { FactState } from '../../components/FactState/FactState';
import { performanceRows, summarizePerformance, type PerformanceRow, type PerformanceSummary } from '../../api/performance';
import { buildMonitoringSignals, type MonitoringSeverity } from '../../api/monitoring';
import styles from './DashboardPage.module.css';

type ChangeWindow = '24h' | '7d' | '30d' | 'all';

const FLEET_PAGE_SIZE = 4;
/** Static lookups, so the CSS-module gate can verify every class by name. */
const FLEET_ACCENTS = [styles.fleetBlue, styles.fleetNeutral, styles.fleetGreen, styles.fleetViolet];

/**
 * Yes, no, and unknown are three different facts, and every distribution on
 * this page keeps them apart. Folding `unknown` into `no` — or worse, dropping
 * it — would let a data gap masquerade as a measured answer, which is the one
 * failure this catalog exists to prevent.
 */
interface TriCount { yes: number; no: number; unknown: number }

function triOf(models: ApiModel[], read: (model: ApiModel) => boolean | null): TriCount {
  let yes = 0;
  let no = 0;
  let unknown = 0;
  for (const model of models) {
    const value = read(model);
    if (value === null) unknown += 1;
    else if (value) yes += 1;
    else no += 1;
  }
  return { yes, no, unknown };
}

interface BucketRow { label: string; count: number; unknown?: boolean }

const SCORE_BUCKETS: { label: string; min: number }[] = [
  { label: '90–100', min: 90 },
  { label: '80–89', min: 80 },
  { label: '70–79', min: 70 },
  { label: '60–69', min: 60 },
  { label: 'Below 60', min: 0 },
];

const CONTEXT_BUCKETS: { label: string; min: number }[] = [
  { label: '1M or more', min: 1_000_000 },
  { label: '256K–1M', min: 256_000 },
  { label: '128K–256K', min: 128_000 },
  { label: '32K–128K', min: 32_000 },
  { label: 'Under 32K', min: 0 },
];

interface Composition {
  capabilities: { key: string; label: string; counts: TriCount }[];
  pricing: TriCount;
  scores: BucketRow[];
  context: BucketRow[];
  maxContext: number;
}

function summarizeComposition(models: ApiModel[]): Composition {
  const scores: BucketRow[] = SCORE_BUCKETS.map((bucket) => ({ label: bucket.label, count: 0 }));
  let notScored = 0;
  for (const model of models) {
    const value = model.overallScore.value;
    if (value === null) { notScored += 1; continue; }
    const index = SCORE_BUCKETS.findIndex((bucket) => value >= bucket.min);
    scores[index === -1 ? scores.length - 1 : index].count += 1;
  }
  scores.push({ label: 'Not yet scored', count: notScored, unknown: true });

  const context: BucketRow[] = CONTEXT_BUCKETS.map((bucket) => ({ label: bucket.label, count: 0 }));
  let contextUnknown = 0;
  for (const model of models) {
    if (model.contextTokens === null) { contextUnknown += 1; continue; }
    const index = CONTEXT_BUCKETS.findIndex((bucket) => model.contextTokens! >= bucket.min);
    context[index === -1 ? context.length - 1 : index].count += 1;
  }
  context.push({ label: 'Not published', count: contextUnknown, unknown: true });

  return {
    capabilities: [
      { key: 'tools', label: 'Tool calling', counts: triOf(models, (m) => m.capabilities.tools) },
      { key: 'reasoning', label: 'Reasoning', counts: triOf(models, (m) => m.capabilities.reasoning) },
      { key: 'structured', label: 'Structured output', counts: triOf(models, (m) => m.capabilities.structured) },
      { key: 'attachment', label: 'Attachments', counts: triOf(models, (m) => m.capabilities.attachment) },
      { key: 'multimodal', label: 'Multimodal input', counts: triOf(models, (m) => m.inputModalities === null ? null : m.inputModalities.length > 1) },
    ],
    pricing: triOf(models, (m) => m.pricing.isFree === null ? null : m.pricing.isFree),
    scores,
    context,
    maxContext: Math.max(0, ...models.map((m) => m.contextTokens ?? 0)),
  };
}

export function DashboardPage() {
  const { data, error, loading, reload, health, healthError, healthLoading, revision } = useCatalog();
  const [monitoringOpen, setMonitoringOpen] = useState(true);
  const [fleetPage, setFleetPage] = useState(0);
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

  /*
   * Above the guards, because a hook may not be conditional. These sat after
   * `if (loading && !data)` once before, the loading render called fewer hooks
   * than the render that followed, and React answered with "Rendered more hooks
   * than during the previous render" — a blank dashboard on every cold load.
   * Derived from `data` rather than destructured fields, which only exist once
   * the guards have passed. Every helper accepts an empty list.
   */
  const performanceSummary = useMemo(() => summarizePerformance(data?.models ?? []), [data]);
  const measuredRows = useMemo(() => performanceRows(data?.models ?? []), [data]);
  const composition = useMemo(() => summarizeComposition(data?.models ?? []), [data]);
  const fleet = useMemo(() => {
    if (!data) return [];
    const freeByProvider = new Map<string, number>();
    for (const model of data.models) {
      if (model.pricing.isFree === true) freeByProvider.set(model.providerId, (freeByProvider.get(model.providerId) ?? 0) + 1);
    }
    // A status readout, not a browser: fixed order, most complete coverage
    // first, so the row that needs work is the one that stands out by falling.
    return [...data.providers]
      .map((provider) => ({
        provider,
        free: freeByProvider.get(provider.id) ?? 0,
        scoredRatio: provider.liveModels > 0 ? provider.overallScoreScored / provider.liveModels : 0,
      }))
      .sort((a, b) => (b.scoredRatio - a.scoredRatio) || a.provider.name.localeCompare(b.provider.name));
  }, [data]);

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

  const { meta } = data;
  const oldestSuccess = data.providers
    .map((p) => p.lastSuccessfulSyncAt)
    .filter((x): x is string => Boolean(x))
    .sort()[0] ?? null;
  const stale = data.providers.filter((p) => p.freshness !== 'fresh');
  const unreadNotifications = notifications.filter((notification) => notification.readAt === null).length;
  // A statistics page samples the stream; the bell owns the full history. The
  // panel states what it withholds, because a silent cap under a bigger badge
  // is the exact defect the notification center was just cured of.
  const recentNotifications = notifications.slice(0, 12);
  const fleetPages = Math.max(1, Math.ceil(fleet.length / FLEET_PAGE_SIZE));
  // Clamped on read, not in an effect: a provider list that shrinks must not
  // strand the pager on a page that no longer exists.
  const safeFleetPage = Math.min(fleetPage, fleetPages - 1);
  const visibleFleet = fleet.slice(safeFleetPage * FLEET_PAGE_SIZE, safeFleetPage * FLEET_PAGE_SIZE + FLEET_PAGE_SIZE);

  return (
    <div className={styles.page}>
      <header className={styles.masthead}>
        <div className={styles.mastheadIntro}>
          <span className={styles.mastheadEyebrow}>Venom Catalog · Operations</span>
          <h1 className={styles.mastheadTitle}>Dashboard</h1>
          <p className={styles.mastheadThesis}>
            Catalog-wide statistics, read from the running service. Every figure
            on this page names its source; an unknown renders as unknown, never
            as zero.
          </p>
        </div>
        <div className={styles.mastheadStatus} aria-label="Catalog operational status">
          <span className={`${styles.statusChip} ${data.origin === 'live' && stale.length === 0 ? styles.statusChipLive : styles.statusChipWarn}`}>
            <span className={styles.statusDot} aria-hidden="true" />
            {data.origin === 'live' ? stale.length === 0 ? 'Live catalog' : 'Live · attention needed' : 'Offline snapshot'}
          </span>
          <span className={styles.statusMetaItem}>
            {data.origin === 'live' ? `Oldest provider sync ${formatAgo(oldestSuccess)}` : `Snapshot ${formatAgo(data.snapshotGeneratedAt ?? null)}`}
          </span>
          <span className={styles.statusMetaItem}>
            <code className={styles.contractChip}>{CATALOG_API_CONTRACT_VERSION}</code>
            <code className={styles.contractChip}>{meta.methodologyVersion}</code>
          </span>
        </div>
      </header>

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

      {/* Four headline figures. Each carries the provenance of its number,
          because a figure with no source is exactly what this catalog refuses
          to publish; a missing figure renders as the catalog's missing chip. */}
      <section className={styles.kpiGrid} aria-label="Catalog key figures">
        <Kpi
          value={String(meta.liveModels)}
          label="Live models"
          caption={`across ${data.providers.length} provider fleet${data.providers.length === 1 ? '' : 's'} · provider live APIs`}
          hint="Models currently present in a provider roster this catalog fetched successfully."
        />
        <Kpi
          value={String(meta.catalogReady)}
          ratio={meta.liveModels > 0 ? meta.catalogReady / meta.liveModels : 0}
          label="Catalog ready"
          caption={`of ${meta.liveModels} · every operational fact resolved`}
          hint={`Every operational fact resolved. ${meta.needsVerification} more are served by the providers but have an unresolved fact and are listed separately on their provider page.`}
        />
        <Kpi
          value={String(meta.overallScoreScored)}
          ratio={meta.liveModels > 0 ? meta.overallScoreScored / meta.liveModels : 0}
          label="Complete overall scores"
          caption={`of ${meta.liveModels} · overall-score-v1 evidence`}
          hint={`Operational data is available for ${meta.operationalScored}/${meta.liveModels} models. ${meta.liveModels - meta.overallScoreScored} model(s) do not yet have a complete overall-score-v1 evaluation. Provider availability and metadata do not substitute for reproducible task, speed, and cost evidence.`}
        />
        {/* The identity breakdown arrives as one answer or not at all, so one
            null here means the whole figure is unknown — rendered as unknown,
            never interpolated into "undefined/116" or a fabricated zero. */}
        <Kpi
          value={meta.identity.resolved === null ? null : String(meta.identity.resolved)}
          ratio={meta.identity.resolved === null || meta.liveModels === 0 ? null : meta.identity.resolved / meta.liveModels}
          label="Identity proven"
          tone="ok"
          caption={meta.identity.resolved === null ? 'not carried by this response' : `of ${meta.liveModels} · ${meta.identity.identityReview ?? 0} in review`}
          hint={
            meta.identity.resolved === null
              ? 'This catalog service response did not carry the identity breakdown — unknown, not zero. Reload once the service is back on the current contract.'
              : `${meta.identity.identityReview} are in identity review — candidates were examined and refused, with the evidence recorded — and ${meta.identity.unresolved} matched nothing upstream at all. Identity is a separate question from quality: a proven identity does not imply a benchmark exists.`
          }
        />
      </section>

      <MonitoringPanel
        signals={monitoringSignals}
        meta={meta}
        loading={Boolean(healthLoading && !health)}
        open={monitoringOpen}
        onToggle={() => setMonitoringOpen((value) => !value)}
        onRetry={handleMonitoringRetry}
      />

      <section className={styles.fleetPanel} aria-labelledby="fleet-title">
        <div className={styles.panelHeader}>
          <div>
            <span className={styles.monitoringEyebrow}>Provider fleet · /v1/providers</span>
            <h2 id="fleet-title" className={styles.panelTitle}>Provider fleet</h2>
          </div>
          {fleetPages > 1 ? (
            <div className={styles.fleetPager} role="group" aria-label="Provider fleet pages">
              <button
                type="button"
                className={styles.fleetPagerBtn}
                onClick={() => setFleetPage((page) => Math.max(0, page - 1))}
                disabled={safeFleetPage === 0}
                aria-label="Previous providers"
              >
                <LuChevronLeft size={15} aria-hidden="true" />
              </button>
              <span className={styles.fleetPagerState} aria-live="polite">{safeFleetPage + 1} / {fleetPages}</span>
              <button
                type="button"
                className={styles.fleetPagerBtn}
                onClick={() => setFleetPage((page) => Math.min(fleetPages - 1, page + 1))}
                disabled={safeFleetPage === fleetPages - 1}
                aria-label="Next providers"
              >
                <LuChevronRight size={15} aria-hidden="true" />
              </button>
            </div>
          ) : (
            <span className={styles.panelAside}>{data.providers.length} provider{data.providers.length === 1 ? '' : 's'} syncing</span>
          )}
        </div>
        {fleet.length === 0 ? (
          <div className={styles.panelEmpty} role="status">No providers are registered in the catalog yet.</div>
        ) : (
          <div className={styles.fleetGrid}>
            {visibleFleet.map(({ provider, free, scoredRatio }, index) => {
              const pres = present(provider.id);
              const percent = Math.round(scoredRatio * 100);
              // The reference design gives each card its own cast. The accent
              // cycles by position, so it decorates without claiming to encode
              // any fact about the provider.
              const accent = FLEET_ACCENTS[(safeFleetPage * FLEET_PAGE_SIZE + index) % FLEET_ACCENTS.length];
              return (
                <Link key={provider.id} to={`/provider/${provider.id}`} className={`${styles.fleetCard} ${accent}`} aria-label={provider.name}>
                  <div className={styles.fleetCardHead}>
                    <span className={styles.fleetLogoBox}>
                      {pres.logo
                        ? <img src={pres.logo} alt="" className={`${styles.fleetLogo} ${pres.invertInDark ? 'logo-invert-dark' : ''}`} />
                        : <LuCpu size={20} aria-hidden="true" />}
                    </span>
                    <span className={styles.fleetName}>{provider.name}</span>
                  </div>
                  <div className={styles.fleetCount}>
                    <strong className={styles.fleetCountValue}>{provider.liveModels}</strong>
                    <span className={styles.fleetCountLabel}>Live models</span>
                  </div>
                  <div className={styles.fleetSync}><FreshnessBadge provider={provider} compact /></div>
                  <dl className={styles.fleetStats}>
                    <div className={styles.fleetStat}>
                      <dt>Scored</dt>
                      <dd>{provider.overallScoreScored} {percent}%</dd>
                    </div>
                    <div className={styles.fleetStat}>
                      <dt>Free models</dt>
                      <dd>{free > 0 ? free : '—'}</dd>
                    </div>
                  </dl>
                </Link>
              );
            })}
          </div>
        )}
      </section>

      {/* One 2x2 grid, one seam: the four operational panels share the same
          column split, and each row's borders stretch to one height. Two rows
          with different splits is what read as clutter. */}
      <div className={styles.opsGrid}>
        <PerformancePanel summary={performanceSummary} rows={measuredRows} />
        <NotificationHistory
          notifications={recentNotifications}
          total={notifications.length}
          loading={notificationsLoading}
          error={notificationsError}
          unread={unreadNotifications}
          onRetry={handleMonitoringRetry}
        />
        <CompositionSection composition={composition} meta={meta} liveModels={meta.liveModels} />
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
      </div>

      <footer className={styles.footer}>
        <span>Venom Router Catalog</span>
        <span className={styles.footerDot}>·</span>
        <span>
          Scores follow {meta.methodologyVersion} under the {meta.profileId} profile; a
          model with no benchmark is unrated, never zero
          {meta.calibration && (
            <>
              {' '}(calibration fitted on {meta.calibration.n} models, ρ&nbsp;=
              {meta.calibration.rho.toFixed(3)}, error&nbsp;±{meta.calibration.looRmse.toFixed(1)})
            </>
          )}
          .
        </span>
        <span className={styles.footerDot}>·</span>
        <Link to="/changes" className={styles.footerLink}>What's new</Link>
      </footer>
    </div>
  );
}

/**
 * The tri-state meter behind every distribution on this page.
 *
 * Three encodings, no colour: solid means a confirmed yes, the bare track means
 * a confirmed no, and the hatched segment means nobody published an answer.
 * Colour stays reserved for health state, and the pattern survives both themes
 * and colour-blindness — which a green/red pair would not.
 */
function HonestyBar({ counts, label }: { counts: TriCount; label: string }) {
  const total = counts.yes + counts.no + counts.unknown;
  const width = (part: number) => total === 0 ? 0 : (part / total) * 100;
  return (
    <div className={styles.triRow}>
      <span className={styles.triLabel}>{label}</span>
      <div className={styles.triBar} role="img" aria-label={`${label}: ${counts.yes} yes, ${counts.no} no, ${counts.unknown} unknown`}>
        {counts.yes > 0 && <span className={styles.triYes} style={{ width: `${width(counts.yes)}%` }} />}
        {counts.unknown > 0 && <span className={styles.triUnknown} style={{ width: `${width(counts.unknown)}%` }} />}
      </div>
      <span className={styles.triCounts}>
        <strong>{counts.yes}</strong> yes · {counts.no} no{counts.unknown > 0 && <span className={styles.triUnknownCount}> · {counts.unknown} unknown</span>}
      </span>
    </div>
  );
}

function DistributionRows({ rows }: { rows: BucketRow[] }) {
  const max = Math.max(1, ...rows.map((row) => row.count));
  return (
    <div className={styles.distList}>
      {rows.map((row) => (
        <div key={row.label} className={styles.distRow}>
          <span className={styles.distLabel}>{row.label}</span>
          <div className={styles.distTrack}>
            {row.count > 0 && (
              <span
                className={row.unknown ? styles.distFillUnknown : styles.distFill}
                style={{ width: `${(row.count / max) * 100}%` }}
              />
            )}
          </div>
          <span className={styles.distCount}>{row.count}</span>
        </div>
      ))}
    </div>
  );
}

function CompositionSection({ composition, meta, liveModels }: { composition: Composition; meta: CatalogData['meta']; liveModels: number }) {
  const conflictFields = Object.entries(meta.conflictsByField);
  return (
    <section className={styles.compositionPanel} aria-labelledby="composition-title">
      <div className={styles.panelHeader}>
        <div>
          <span className={styles.monitoringEyebrow}>Inventory analytics · /v1/models</span>
          <h2 id="composition-title" className={styles.panelTitle}>Catalog composition</h2>
        </div>
        <span className={styles.panelAside}>
          Hatched segments are unknowns — a gap in the sources, not a measured no
        </span>
      </div>

      <div className={styles.compositionGrid}>
        <article className={styles.compCard} aria-labelledby="comp-scores">
          <h3 id="comp-scores" className={styles.compTitle}>Overall score distribution</h3>
          <p className={styles.compCaption}>overall-score-v1 across {liveModels} live models. Unscored models are counted apart — they are unranked, not last.</p>
          <DistributionRows rows={composition.scores} />
        </article>

        <article className={styles.compCard} aria-labelledby="comp-capabilities">
          <h3 id="comp-capabilities" className={styles.compTitle}>Capability coverage</h3>
          <p className={styles.compCaption}>Resolved capability facts, with the models nobody answered for shown as unknown.</p>
          <div className={styles.triList} data-testid="capability-coverage">
            {composition.capabilities.map((capability) => (
              <HonestyBar key={capability.key} counts={capability.counts} label={capability.label} />
            ))}
          </div>
        </article>

        <article className={styles.compCard} aria-labelledby="comp-context">
          <h3 id="comp-context" className={styles.compTitle}>Context windows</h3>
          <p className={styles.compCaption}>
            Declared serving limits{composition.maxContext > 0 && <>; the largest window in the catalog is <strong>{formatTokens(composition.maxContext)}</strong></>}.
          </p>
          <DistributionRows rows={composition.context} />
        </article>

        <article className={styles.compCard} aria-labelledby="comp-integrity">
          <h3 id="comp-integrity" className={styles.compTitle}>Access & integrity</h3>
          <p className={styles.compCaption}>Pricing access, and the identity work recorded behind the roster.</p>
          <div className={styles.triList}>
            <HonestyBar counts={composition.pricing} label="Free to call" />
          </div>
          <dl className={styles.integrityList}>
            <div className={styles.integrityRow}>
              <dt className={styles.integrityLabel}>Identity candidates refused</dt>
              <dd className={styles.integrityValue}>
                {meta.identityDetail ? (
                  <>
                    <strong>{meta.identityDetail.rejectedCandidates}</strong>
                    <span className={styles.integrityCaption}> across {meta.identityDetail.withRejectedCandidates} models, each with recorded evidence</span>
                  </>
                ) : (
                  <>
                    <FactState state="missing" />
                    <span className={styles.integrityCaption}> not carried by this service response — unknown, not zero</span>
                  </>
                )}
              </dd>
            </div>
            <div className={styles.integrityRow}>
              <dt className={styles.integrityLabel}>Fields under open dispute</dt>
              <dd className={styles.integrityValue}>
                {meta.conflictedModels === null ? (
                  <>
                    <FactState state="missing" />
                    <span className={styles.integrityCaption}> not carried by this service response — unknown, not zero</span>
                  </>
                ) : conflictFields.length === 0 ? (
                  <span className={styles.integrityCaption}>none — every recorded dispute has a resolved verdict</span>
                ) : (
                  <span className={styles.integrityCaption}>{conflictFields.map(([field, count]) => `${field} (${count})`).join(' · ')}</span>
                )}
              </dd>
            </div>
          </dl>
        </article>
      </div>
    </section>
  );
}

/**
 * A hand-rolled SVG area chart. No chart library: the pinned runtime and 40
 * lines of path arithmetic cover everything this page needs, and every point
 * is a real measurement — the x-axis is the catalog's measured models in rank
 * order, not an invented time series.
 */
function AreaChart({ values, tone, ariaLabel, yUnit, xStartLabel, xEndLabel }: {
  values: number[];
  tone: 'blue' | 'green';
  ariaLabel: string;
  yUnit: string;
  xStartLabel: string;
  xEndLabel: string;
}) {
  const W = 600;
  const H = 170;
  const PAD = { top: 12, right: 8, bottom: 8, left: 44 };
  const innerW = W - PAD.left - PAD.right;
  const innerH = H - PAD.top - PAD.bottom;
  const max = Math.max(...values, 0.0001);
  const x = (index: number) => PAD.left + (values.length === 1 ? innerW / 2 : (index / (values.length - 1)) * innerW);
  const y = (value: number) => PAD.top + innerH - (value / max) * innerH;
  const line = values.map((value, index) => `${index === 0 ? 'M' : 'L'}${x(index).toFixed(1)},${y(value).toFixed(1)}`).join(' ');
  const area = `${line} L${x(values.length - 1).toFixed(1)},${PAD.top + innerH} L${x(0).toFixed(1)},${PAD.top + innerH} Z`;
  const gradientId = `chart-${tone}`;
  const ticks = [0, 0.5, 1];
  const format = (value: number) => value >= 100 ? value.toFixed(0) : value >= 10 ? value.toFixed(1) : value.toFixed(2);

  return (
    <div className={styles.chartFrame}>
      <svg viewBox={`0 0 ${W} ${H}`} className={`${styles.chartSvg} ${tone === 'blue' ? styles.chartBlue : styles.chartGreen}`} role="img" aria-label={ariaLabel} preserveAspectRatio="none">
        <defs>
          <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="currentColor" stopOpacity="0.32" />
            <stop offset="100%" stopColor="currentColor" stopOpacity="0.02" />
          </linearGradient>
        </defs>
        {ticks.map((tick) => (
          <g key={tick}>
            <line
              x1={PAD.left} x2={W - PAD.right}
              y1={PAD.top + innerH - tick * innerH} y2={PAD.top + innerH - tick * innerH}
              className={styles.chartGrid}
            />
            <text x={PAD.left - 6} y={PAD.top + innerH - tick * innerH + 3} className={styles.chartTick} textAnchor="end">
              {format(max * tick)}{yUnit}
            </text>
          </g>
        ))}
        <path d={area} fill={`url(#${gradientId})`} />
        <path d={line} className={styles.chartLine} fill="none" />
      </svg>
      <div className={styles.chartXAxis} aria-hidden="true">
        <span>{xStartLabel}</span>
        <span>{xEndLabel}</span>
      </div>
    </div>
  );
}

function PerformancePanel({ summary, rows }: { summary: PerformanceSummary; rows: PerformanceRow[] }) {
  const formatSeconds = (value: number | null) => value === null ? '—' : `${value.toFixed(2)}s`;
  const formatRate = (value: number | null) => value === null ? '—' : `${(value * 100).toFixed(1)}%`;
  // Rank order is the honest x-axis this catalog can draw: each point is one
  // measured model. A dated "trend" would require history the API does not
  // serve, and inventing one is the kind of chart this product exists to end.
  const ttftProfile = [...rows]
    .map((row) => row.performance.ttftMedianSeconds)
    .filter((value): value is number => value !== null)
    .sort((a, b) => a - b);
  const p95Profile = [...rows]
    .map((row) => row.performance.endToEndP95Seconds)
    .filter((value): value is number => value !== null)
    .sort((a, b) => a - b);

  return (
    <section className={styles.performancePanel} aria-labelledby="performance-panel-title">
      <div className={styles.panelHeader}>
        <div>
          <span className={styles.monitoringEyebrow}>Measured runtime evidence</span>
          <h2 id="performance-panel-title" className={styles.panelTitle}>Model performance</h2>
        </div>
        <span className={styles.performanceCoverage}>{summary.measuredModels}/{summary.totalModels} measured</span>
      </div>
      <p className={styles.panelDescription}>
        Stored speed-probe aggregates from Catalog evaluations. Unmeasured models
        are not drawn, ranked, or treated as slow.
      </p>

      <div className={styles.performanceKpis} role="group" aria-label="Performance summary">
        <div className={styles.performanceKpi}><strong>{formatSeconds(summary.medianTtftSeconds)}</strong><span>Median TTFT</span></div>
        <div className={styles.performanceKpi}><strong>{summary.medianOutputTokensPerSecond === null ? '—' : `${summary.medianOutputTokensPerSecond.toFixed(1)}`}</strong><span>Median tokens/s</span></div>
        <div className={styles.performanceKpi}><strong>{formatSeconds(summary.medianEndToEndP95Seconds)}</strong><span>Median p95 response</span></div>
        <div className={styles.performanceKpi}><strong>{formatRate(summary.averageSuccessRate)}</strong><span>Average success</span></div>
      </div>

      {ttftProfile.length < 2 ? (
        <div className={styles.performanceEmpty} role="status">
          <LuActivity size={20} aria-hidden="true" />
          <div><strong>No measured performance data</strong><p>Speed evaluations have not produced complete TTFT, throughput, and response-time samples for the current catalog.</p></div>
        </div>
      ) : (
        <div className={styles.chartStack}>
          <div className={styles.chartBlock}>
            <div className={styles.chartHead}>
              <h3 className={styles.chartTitle}>Time to first token</h3>
              <span className={styles.chartMeta}>median TTFT per measured model · fastest → slowest</span>
            </div>
            <AreaChart
              values={ttftProfile}
              tone="blue"
              ariaLabel={`Median time to first token across ${ttftProfile.length} measured models, from ${ttftProfile[0].toFixed(2)} to ${ttftProfile[ttftProfile.length - 1].toFixed(2)} seconds`}
              yUnit="s"
              xStartLabel="fastest model"
              xEndLabel="slowest model"
            />
          </div>
          <div className={styles.chartBlock}>
            <div className={styles.chartHead}>
              <h3 className={styles.chartTitle}>End-to-end response, p95</h3>
              <span className={styles.chartMeta}>per measured model · fastest → slowest</span>
            </div>
            <AreaChart
              values={p95Profile}
              tone="green"
              ariaLabel={`95th percentile end-to-end response across ${p95Profile.length} measured models, from ${p95Profile[0].toFixed(2)} to ${p95Profile[p95Profile.length - 1].toFixed(2)} seconds`}
              yUnit="s"
              xStartLabel="fastest model"
              xEndLabel="slowest model"
            />
          </div>
        </div>
      )}
      <p className={styles.performanceFootnote}>Based on {summary.successfulSamples}/{summary.sampleCount} complete speed samples. A blank metric means the server did not publish a complete measurement.</p>
    </section>
  );
}

function NotificationHistory({
  notifications,
  total,
  loading,
  error,
  unread,
  onRetry,
}: {
  notifications: CatalogNotification[];
  total: number;
  loading: boolean;
  error: string | null;
  unread: number;
  onRetry: () => void;
}) {
  const categoryLabel = (category: CatalogNotification['category']) => category === 'success' ? 'Added' : category === 'error' ? 'Removed' : 'Needs attention';

  return (
    <section className={styles.alertCenter} aria-labelledby="alert-center-title">
      <div className={styles.alertCenterHeader}>
        <div>
          <span className={styles.monitoringEyebrow}>Recorded catalog events</span>
          <h2 id="alert-center-title" className={styles.alertCenterTitle}>Notification history <span className={styles.alertCount}>{unread} unread</span></h2>
        </div>
        <button type="button" className={styles.monitoringAction} onClick={onRetry} disabled={loading}>Refresh notifications</button>
      </div>

      {error && <div className={styles.alertCenterError} role="alert"><span>{error}</span><button type="button" className={styles.monitoringAction} onClick={onRetry}>Retry</button></div>}
      {loading && <div className={styles.alertCenterState} aria-busy="true">Loading notification history…</div>}
      {!loading && !error && notifications.length === 0 && <div className={styles.alertCenterState}>No catalog notifications have been recorded.</div>}
      {!loading && total > notifications.length && (
        <p className={styles.alertTruncation} role="status">
          Showing the {notifications.length} most recent of {total}. The notification bell holds the full history.
        </p>
      )}
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
  meta,
  loading,
  open,
  onToggle,
  onRetry,
}: {
  signals: ReturnType<typeof buildMonitoringSignals>;
  meta: CatalogData['meta'];
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
          {/* The two integrity figures an operator triages by, kept beside the
              health verdict they qualify. Null renders as the missing chip. */}
          <span className={styles.healthChip} title="Models with a field withheld because entitled sources still disagree. Resolved disputes no longer count here.">
            {meta.conflictedModels === null ? <FactState state="missing" /> : <strong>{meta.conflictedModels}</strong>} open conflicts
          </span>
          <span className={styles.healthChip} title="Models without a quality score, each carrying a machine-readable reason.">
            <strong>{meta.unrated}</strong> unrated
          </span>
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
      <div className={styles.skeletonKpis} aria-hidden="true">
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

/**
 * One headline figure.
 *
 * `value === null` means the figure is unknown, not zero — rendered with the
 * same "missing" chip the rest of the catalog uses for a fact nobody sent.
 * `ratio` draws the thin completeness meter; `tone` tints the value where the
 * design calls a figure out, and never replaces the number itself.
 */
function Kpi({ value, label, caption, hint, ratio, tone }: {
  value: string | null;
  label: string;
  caption: string;
  hint: string;
  ratio?: number | null;
  tone?: 'ok' | 'warn';
}) {
  return (
    <div className={styles.kpiCard} title={hint}>
      <span className={`${styles.kpiValue} ${tone === 'ok' ? styles.toneOk : ''} ${tone === 'warn' ? styles.toneWarn : ''}`}>
        {value === null ? <FactState state="missing" /> : value}
      </span>
      {typeof ratio === 'number' && (
        <span className={styles.meter} aria-hidden="true">
          <i className={styles.meterFill} style={{ width: `${Math.round(Math.min(1, Math.max(0, ratio)) * 100)}%` }} />
        </span>
      )}
      <span className={styles.kpiLabel}>{label}</span>
      <span className={styles.kpiCaption}>{caption}</span>
    </div>
  );
}

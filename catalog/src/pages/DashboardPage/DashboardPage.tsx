import { useMemo, useState, useEffect } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { LuArrowUpRight, LuArrowRight, LuCircleAlert, LuCpu, LuRefreshCw } from 'react-icons/lu';
import { useCatalog } from '../../hooks/useCatalog';
import { present } from '../../api/presentation';
import { formatTokens, formatAgo } from '../../api/client';
import { FreshnessBadge } from '../../components/FreshnessBadge/FreshnessBadge';
import { Toolbar, PROVIDER_FILTERS } from '../../components/Toolbar/Toolbar';
import { FactState } from '../../components/FactState/FactState';
import { providerMatchesFilter } from '../../api/filters';
import styles from './DashboardPage.module.css';

export function DashboardPage() {
  const { data, error, loading, reload } = useCatalog();
  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState('all');
  const [view, setView] = useState<'grid' | 'table'>('table');
  const [preferredView, setPreferredView] = useState<'grid' | 'table'>('table');

  const navigate = useNavigate();

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

  const providers = useMemo(() => {
    if (!data) return [];
    const q = query.toLowerCase().trim();
    return data.providers.filter((p) => {
      const blurb = present(p.id).blurb.toLowerCase();
      if (q && !p.name.toLowerCase().includes(q) && !blurb.includes(q) && !p.id.includes(q)) return false;
      return providerMatchesFilter(p.id, data.models, filter);
    });
  }, [data, query, filter]);

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

      {/* This page lists providers, so it takes the provider filters. The model
          filters reached it only as a default, which is how "Not Deprecated"
          arrived with no branch to act on and no effect a reader could see. */}
      <Toolbar
        query={query}
        onQueryChange={setQuery}
        filter={filter}
        onFilterChange={setFilter}
        options={PROVIDER_FILTERS}
        placeholder="Search providers by name or ID..."
        view={view}
        onViewChange={handleViewChange}
      />

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

      {providers.length === 0 && <div className={styles.empty}>No providers match your search.</div>}

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



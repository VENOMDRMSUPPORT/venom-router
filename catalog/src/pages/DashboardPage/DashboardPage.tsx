import { useMemo, useState, useEffect } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { LuArrowUpRight, LuArrowRight, LuCpu } from 'react-icons/lu';
import { useCatalog } from '../../hooks/useCatalog';
import { present } from '../../api/presentation';
import { formatTokens, formatAgo } from '../../api/client';
import { FreshnessBadge } from '../../components/FreshnessBadge/FreshnessBadge';
import { Toolbar } from '../../components/Toolbar/Toolbar';
import styles from './DashboardPage.module.css';

export function DashboardPage() {
  const { data, error, loading } = useCatalog();
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
      if (filter === 'free') return data.models.some((m) => m.providerId === p.id && m.pricing.isFree === true);
      if (filter === 'paid') return data.models.some((m) => m.providerId === p.id && m.pricing.outputPerMTokens !== null && m.pricing.outputPerMTokens > 0);
      if (filter === '1m') return data.models.some((m) => m.providerId === p.id && (m.contextTokens ?? 0) >= 1_000_000);
      if (filter === 'multimodal') return data.models.some((m) => m.providerId === p.id && (m.inputModalities?.length ?? 0) > 1);
      return true;
    });
  }, [data, query, filter]);

  if (loading) return <div className={styles.empty}>Loading catalog…</div>;
  if (error || !data) {
    return (
      <div className={styles.empty}>
        Could not reach the catalog service or its snapshot.
        <div className={styles.emptyHint}>
          Start it with <code>npm run serve</code> in <code>catalog/</code>.
          {error && <> ({error})</>}
        </div>
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
          value={`${meta.qualityScored}/${meta.liveModels}`}
          label="Benchmarked externally"
          hint={`${meta.unrated} of these models have not been measured by any published benchmark. That is a gap in what the industry has measured, not a gap in this catalog — unknown quality is not low quality.`}
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

      {data.origin === 'snapshot' && (
        <div className={styles.warnBar}>
          Showing a build-time snapshot — the catalog service is not reachable, so
          these figures may be out of date.
        </div>
      )}
      {stale.length > 0 && data.origin === 'live' && (
        <div className={styles.warnBar}>
          {stale.length} provider{stale.length > 1 ? 's have' : ' has'} not synced
          successfully in a while: {stale.map((p) => p.name).join(', ')}.
        </div>
      )}

      <Toolbar
        query={query}
        onQueryChange={setQuery}
        filter={filter}
        onFilterChange={setFilter}
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
            const scoredRatio = p.liveModels > 0 ? Math.round((p.qualityScored / p.liveModels) * 100) : 0;

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
                        {m.modelId}
                      </span>
                    ))}
                    {mine.length > 3 && (
                      <span className={styles.moreChip}>+{mine.length - 3} more</span>
                    )}
                  </div>
                )}

                <div className={styles.progressSection}>
                  <div className={styles.progressMeta}>
                    <span className={styles.progressLabel}>Benchmark Scored</span>
                    <span className={styles.progressPercent}>{scoredRatio}% ({p.qualityScored}/{p.liveModels})</span>
                  </div>
                  <div className={styles.progressBarBg}>
                    <div
                      className={styles.progressBarFill}
                      style={{ width: `${scoredRatio}%` }}
                    />
                  </div>
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
                    <span className={styles.statValue}>{p.qualityScored}</span>
                    <span className={styles.statLabel}>Scored</span>
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
                  <th className={styles.num} title="How many of this provider's models a published benchmark has measured. A low figure means the industry has not benchmarked those models yet — it is not a gap in this catalog's data.">Benchmarked</th>
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
                  const scoredRatio = p.liveModels > 0 ? Math.round((p.qualityScored / p.liveModels) * 100) : 0;
                  return (
                    <tr key={p.id} className={styles.tableRow} onClick={() => navigate(`/provider/${p.id}`)}>
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
                            {scoredRatio}% <span className={styles.tableProgressRatio}>({p.qualityScored}/{p.liveModels})</span>
                          </div>
                          <div className={styles.tableProgressBarBg}>
                            <div
                              className={styles.tableProgressBarFill}
                              style={{ width: `${scoredRatio}%` }}
                            />
                          </div>
                        </div>
                      </td>
                      <td className={styles.num}>{free > 0 ? free : '—'}</td>
                      <td className={styles.num}>
                        <div className={styles.actionCell} title="View provider roster">
                          <button className={styles.actionBtn} aria-label="View provider roster">
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

function Kpi({ value, label, hint }: { value: string; label: string; hint: string }) {
  return (
    <div className={styles.kpi} title={hint}>
      <span className={styles.kpiValue}>{value}</span>
      <span className={styles.kpiLabel}>{label}</span>
    </div>
  );
}



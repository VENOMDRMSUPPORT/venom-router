import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { LuArrowUpRight } from 'react-icons/lu';
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
  const [view, setView] = useState<'grid' | 'table'>('grid');

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
        <Kpi value={String(meta.liveModels)} label="Live models" hint="Currently served by the providers below, as of the last successful sync." />
        <Kpi value={formatTokens(maxContext)} label="Max context" hint="Largest context window in the catalog right now." />
        <Kpi
          value={`${meta.qualityScored}/${meta.liveModels}`}
          label="Quality-scored"
          hint={`${meta.unrated} models have no published benchmark. Unknown quality — not low quality.`}
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
        onViewChange={setView}
      />

      <div className={styles.grid}>
        {providers.map((p) => {
          const pres = present(p.id);
          const mine = models.filter((m) => m.providerId === p.id);
          const free = mine.filter((m) => m.pricing.isFree === true).length;
          const maxCtx = Math.max(0, ...mine.map((m) => m.contextTokens ?? 0));
          return (
            <Link key={p.id} to={`/provider/${p.id}`} className={styles.card}>
              <div className={styles.cardTop}>
                <div className={styles.cardHead}>
                  {pres.logo && (
                    <img src={pres.logo} alt="" className={`${styles.logo} ${pres.invertInDark ? 'logo-invert-dark' : ''}`} />
                  )}
                  <div>
                    <div className={styles.nameRow}>
                      <h3 className={styles.name}>{p.name}</h3>
                      <LuArrowUpRight size={14} className={styles.arrow} />
                    </div>
                    <FreshnessBadge provider={p} compact />
                  </div>
                </div>
                <p className={styles.tagline}>{pres.blurb}</p>
              </div>
              <div className={styles.stats}>
                <Stat value={String(p.liveModels)} label="Models" />
                <Stat value={formatTokens(maxCtx)} label="Max ctx" />
                <Stat value={`${p.qualityScored}`} label="Scored" />
                <Stat value={free > 0 ? String(free) : '—'} label="Free" />
              </div>
            </Link>
          );
        })}
      </div>

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

function Stat({ value, label }: { value: string; label: string }) {
  return (
    <div className={styles.stat}>
      <span className={styles.statValue}>{value}</span>
      <span className={styles.statLabel}>{label}</span>
    </div>
  );
}

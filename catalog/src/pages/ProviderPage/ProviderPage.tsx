import { useMemo, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { LuArrowLeft } from 'react-icons/lu';
import { useCatalog, useProviderModels } from '../../hooks/useCatalog';
import { present } from '../../api/presentation';
import { formatTokens, type ApiModel } from '../../api/client';
import { FreshnessBadge } from '../../components/FreshnessBadge/FreshnessBadge';
import { VQCell, VOCell, RankCell, CostCell } from '../../components/ScoreCell/ScoreCell';
import { Callout } from '../../components/Callout/Callout';
import { Toolbar } from '../../components/Toolbar/Toolbar';
import { NotFoundPage } from '../NotFoundPage/NotFoundPage';
import styles from './ProviderPage.module.css';

export function ProviderPage() {
  const { id } = useParams<{ id: string }>();
  const { loading, error } = useCatalog();
  const { provider, models, meta } = useProviderModels(id);

  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState('all');
  const [view, setView] = useState<'grid' | 'table'>(() =>
    typeof window !== 'undefined' && window.innerWidth < 768 ? 'grid' : 'table'
  );

  const filteredModels = useMemo(() => {
    let list = models;
    if (query.trim()) {
      const q = query.toLowerCase();
      list = list.filter(
        (m) =>
          m.modelId.toLowerCase().includes(q) ||
          (m.canonicalId && m.canonicalId.toLowerCase().includes(q))
      );
    }
    if (filter === 'free') list = list.filter((m) => m.pricing.isFree === true);
    if (filter === 'paid') list = list.filter((m) => m.pricing.isFree !== true);
    if (filter === '1m') list = list.filter((m) => (m.contextTokens ?? 0) >= 1000000);
    if (filter === 'multimodal')
      list = list.filter((m) => (m.inputModalities ?? []).some((x) => x !== 'text'));
    return list;
  }, [models, query, filter]);

  if (loading) return <div className={styles.state}>Loading…</div>;
  if (error) return <div className={styles.state}>Catalog unavailable: {error}</div>;
  if (!provider) return <NotFoundPage />;

  const pres = present(provider.id);
  const maxCtx = Math.max(0, ...models.map((m) => m.contextTokens ?? 0));
  const free = models.filter((m) => m.pricing.isFree === true).length;

  // The completeness gate: rows whose operational facts are all resolved make up
  // the catalog proper. The rest are held in a labelled section rather than
  // dropped — the inventory stays whole, the main table stays trustworthy.
  const ready = filteredModels.filter((m) => m.catalogReady);
  const pending = filteredModels.filter((m) => !m.catalogReady);

  const rated = ready
    .filter((m) => m.qualityRank !== null)
    .sort((a, b) => a.qualityRank! - b.qualityRank!);
  const unrated = ready
    .filter((m) => m.qualityRank === null)
    .sort((a, b) => (b.vo.value ?? -1) - (a.vo.value ?? -1));

  return (
    <div>
      <Link to="/" className={styles.back}>
        <LuArrowLeft size={14} />
        <span>All providers</span>
      </Link>

      <header className={styles.header}>
        <div className={styles.logoBox}>
          {pres.logo ? (
            <img src={pres.logo} alt="" className={`${styles.logo} ${pres.invertInDark ? 'logo-invert-dark' : ''}`} />
          ) : (
            <span className={styles.fallbackLogo}>{provider.name.charAt(0)}</span>
          )}
        </div>
        <div className={styles.headerText}>
          <div className={styles.titleRow}>
            <h1 className={styles.title}>{provider.name}</h1>
            <FreshnessBadge provider={provider} />
          </div>
          <p className={styles.subtitle}>{pres.blurb}</p>
        </div>
      </header>

      <div className={styles.stats}>
        <Stat value={String(provider.liveModels)} label="Live models" />
        <Stat value={formatTokens(maxCtx)} label="Max context" />
        <Stat value={`${provider.qualityScored}/${provider.liveModels}`} label="Quality-scored" />
        <Stat value={free > 0 ? String(free) : '—'} label="Free models" isFree={free > 0} />
      </div>

      {pres.note && (
        <Callout>
          <strong>Note:</strong> {pres.note}{' '}
          {pres.docsUrl && (
            <a href={pres.docsUrl} target="_blank" rel="noopener noreferrer">
              Provider docs →
            </a>
          )}
        </Callout>
      )}

      {/* Unified Toolbar */}
      <Toolbar
        query={query}
        onQueryChange={setQuery}
        filter={filter}
        onFilterChange={setFilter}
        view={view}
        onViewChange={setView}
      />

      {view === 'table' ? (
        <>
          <ModelTable title={`Ranked by quality (${rated.length})`} models={rated} />
          {unrated.length > 0 && (
            <ModelTable
              title={`No quality evidence (${unrated.length})`}
              models={unrated}
              note="No benchmark publishes a figure for these models. Their operational data is shown as normal — unknown quality is not low quality, and they are not ranked."
            />
          )}
        </>
      ) : (
        <>
          <ModelGrid title={`Ranked by quality (${rated.length})`} models={rated} />
          {unrated.length > 0 && (
            <ModelGrid
              title={`No quality evidence (${unrated.length})`}
              models={unrated}
              note="No benchmark publishes a figure for these models. Operational data is shown below."
            />
          )}
        </>
      )}

      {pending.length > 0 && (
        <ModelTable
          title={`Needs verification (${pending.length})`}
          models={pending}
          note={`These models are served by the provider, but at least one operational fact could not be resolved from any source. They are listed here rather than inside the catalog so an incomplete row never sits beside a complete one: ${[...new Set(pending.flatMap((m) => m.missingFacts))].join(', ')}.`}
        />
      )}

      <section className={styles.provenance}>
        <h3 className={styles.provTitle}>Where this data comes from</h3>
        <dl className={styles.provGrid}>
          <Row k="Roster">
            <code>{provider.rosterUrl}</code> — the provider's own API, the only
            source that can show a model being added or withdrawn.
          </Row>
          <Row k="Specs">
            <code>models.dev</code> — context, output, capabilities and price.
          </Row>
          <Row k="Quality">
            Published benchmark figures, matched by deterministic identity rules
            only. A model whose identity cannot be established gets no score
            rather than a similar model's score.
          </Row>
          <Row k="Last successful sync">
            {provider.lastSuccessfulSyncAt ?? 'never'}
            {provider.lastAttemptedSyncAt !== provider.lastSuccessfulSyncAt && (
              <> · last attempt {provider.lastAttemptedSyncAt} ({provider.lastOutcome})</>
            )}
          </Row>
          <Row k="Methodology">
            <code>{meta?.methodologyVersion}</code> · profile <code>{meta?.profileId}</code>
          </Row>
        </dl>
      </section>
    </div>
  );
}

function ModelTable({ title, models, note }: { title: string; models: ApiModel[]; note?: string }) {
  if (models.length === 0) return null;
  return (
    <div className={styles.tableWrap}>
      <h3 className={styles.tableTitle}>{title}</h3>
      {note && <p className={styles.tableNote}>{note}</p>}
      <div className={styles.tableScroll}>
        <table className={styles.table}>
          <thead>
            <tr>
              <th className={styles.narrow}>Rank</th>
              <th>Model</th>
              <th>VQ</th>
              <th>VO</th>
              <th>Context</th>
              <th>Max out</th>
              <th title="What this provider charges you per million input tokens.">In</th>
              <th title="What this provider charges you per million output tokens.">Out</th>
              <th>Capabilities</th>
            </tr>
          </thead>
          <tbody>
            {models.map((m) => (
              <tr key={`${m.providerId}/${m.modelId}`}>
                <td className={styles.narrow}><RankCell model={m} /></td>
                <td>
                  <span className={styles.modelName}>{m.modelId}</span>
                  {!m.catalogReady && (
                    <span className={styles.pendingNote} title={`Unresolved: ${m.missingFacts.join(', ')}`}>
                      unresolved: {m.missingFacts.join(', ')}
                    </span>
                  )}
                  {m.canonicalId && m.canonicalId.replace(/^[^/]+\//, '') !== m.modelId && (
                    <span className={styles.canonical} title="The upstream model this row was proven to be. Two providers serving it share one score.">
                      {m.canonicalId}
                    </span>
                  )}
                </td>
                <td><VQCell model={m} /></td>
                <td><VOCell model={m} /></td>
                <td className={styles.num}>{formatTokens(m.contextTokens)}</td>
                <td className={styles.num}>{formatTokens(m.maxOutputTokens)}</td>
                <td className={styles.num}><CostCell model={m} side="in" /></td>
                <td className={styles.num}><CostCell model={m} side="out" /></td>
                <td>
                  <div className={styles.caps}>
                    {m.capabilities.tools && <span className={styles.cap}>tools</span>}
                    {m.capabilities.reasoning && <span className={styles.cap}>reasoning</span>}
                    {m.capabilities.structured && <span className={styles.cap}>structured</span>}
                    {(m.inputModalities ?? []).filter((x) => x !== 'text').map((x) => (
                      <span key={x} className={styles.cap}>{x}</span>
                    ))}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function ModelGrid({ title, models, note }: { title: string; models: ApiModel[]; note?: string }) {
  if (models.length === 0) return null;
  return (
    <div className={styles.tableWrap}>
      <h3 className={styles.tableTitle}>{title}</h3>
      {note && <p className={styles.tableNote}>{note}</p>}
      <div className={styles.modelGrid}>
        {models.map((m) => (
          <div key={`${m.providerId}/${m.modelId}`} className={styles.modelCard}>
            <div className={styles.modelCardTop}>
              <div>
                <span className={styles.modelName}>{m.modelId}</span>
                {m.canonicalId && m.canonicalId.replace(/^[^/]+\//, '') !== m.modelId && (
                  <span className={styles.canonical}>{m.canonicalId}</span>
                )}
              </div>
              <RankCell model={m} />
            </div>

            <div className={styles.modelCardScores}>
              <div className={styles.scorePill}>
                <span className={styles.scoreTag}>VQ</span>
                <VQCell model={m} />
              </div>
              <div className={styles.scorePill}>
                <span className={styles.scoreTag}>VO</span>
                <VOCell model={m} />
              </div>
            </div>

            <div className={styles.modelCardStats}>
              <div className={styles.miniStat}>
                <span className={styles.miniVal}>{formatTokens(m.contextTokens)}</span>
                <span className={styles.miniLbl}>Context</span>
              </div>
              <div className={styles.miniStat}>
                <span className={styles.miniVal}>{formatTokens(m.maxOutputTokens)}</span>
                <span className={styles.miniLbl}>Max out</span>
              </div>
              <div className={styles.miniStat}>
                <span className={styles.miniVal}><CostCell model={m} side="in" /></span>
                <span className={styles.miniLbl}>In</span>
              </div>
              <div className={styles.miniStat}>
                <span className={styles.miniVal}><CostCell model={m} side="out" /></span>
                <span className={styles.miniLbl}>Out</span>
              </div>
            </div>

            <div className={styles.caps}>
              {m.capabilities.tools && <span className={styles.cap}>tools</span>}
              {m.capabilities.reasoning && <span className={styles.cap}>reasoning</span>}
              {m.capabilities.structured && <span className={styles.cap}>structured</span>}
              {(m.inputModalities ?? []).filter((x) => x !== 'text').map((x) => (
                <span key={x} className={styles.cap}>{x}</span>
              ))}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

function Stat({ value, label, isFree }: { value: string; label: string; isFree?: boolean }) {
  return (
    <div className={`${styles.stat} ${isFree ? styles.freeStat : ''}`}>
      <span className={styles.statValue}>{value}</span>
      <span className={styles.statLabel}>{label}</span>
    </div>
  );
}

function Row({ k, children }: { k: string; children: React.ReactNode }) {
  return (
    <div className={styles.provRow}>
      <dt className={styles.provKey}>{k}</dt>
      <dd className={styles.provVal}>{children}</dd>
    </div>
  );
}

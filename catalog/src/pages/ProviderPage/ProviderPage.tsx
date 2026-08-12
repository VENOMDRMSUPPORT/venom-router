import { Link, useParams } from 'react-router-dom';
import { LuArrowLeft } from 'react-icons/lu';
import { useCatalog, useProviderModels } from '../../hooks/useCatalog';
import { present } from '../../api/presentation';
import { formatTokens, formatPrice, type ApiModel } from '../../api/client';
import { FreshnessBadge } from '../../components/FreshnessBadge/FreshnessBadge';
import { VQCell, VOCell, RankCell } from '../../components/ScoreCell/ScoreCell';
import { Callout } from '../../components/Callout/Callout';
import { NotFoundPage } from '../NotFoundPage/NotFoundPage';
import styles from './ProviderPage.module.css';

export function ProviderPage() {
  const { id } = useParams<{ id: string }>();
  const { loading, error } = useCatalog();
  const { provider, models, meta } = useProviderModels(id);

  if (loading) return <div className={styles.state}>Loading…</div>;
  if (error) return <div className={styles.state}>Catalog unavailable: {error}</div>;
  if (!provider) return <NotFoundPage />;

  const pres = present(provider.id);
  const maxCtx = Math.max(0, ...models.map((m) => m.contextTokens ?? 0));
  const free = models.filter((m) => m.pricing.isFree === true).length;

  // Rated first, in rank order; unrated after, in a labelled section. Never
  // interleaved — an unrated model has unknown quality, not low quality.
  const rated = models.filter((m) => m.qualityRank !== null).sort((a, b) => a.qualityRank! - b.qualityRank!);
  const unrated = models.filter((m) => m.qualityRank === null).sort((a, b) => (b.vo.value ?? -1) - (a.vo.value ?? -1));

  return (
    <div>
      <Link to="/" className={styles.back}>
        <LuArrowLeft size={14} />
        <span>All providers</span>
      </Link>

      <header className={styles.header}>
        {pres.logo && (
          <img src={pres.logo} alt="" className={`${styles.logo} ${pres.invertInDark ? 'logo-invert-dark' : ''}`} />
        )}
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
        <Stat value={free > 0 ? String(free) : '—'} label="Free models" />
      </div>

      {pres.note && <Callout><strong>Note:</strong> {pres.note}{' '}
        {pres.docsUrl && <a href={pres.docsUrl} target="_blank" rel="noopener noreferrer">Provider docs →</a>}
      </Callout>}

      <ModelTable title={`Ranked by quality (${rated.length})`} models={rated} />
      {unrated.length > 0 && (
        <ModelTable
          title={`No quality evidence (${unrated.length})`}
          models={unrated}
          note="No benchmark publishes a figure for these models. Their operational data is shown as normal — unknown quality is not low quality, and they are not ranked."
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
              <th>In $/M</th>
              <th>Out $/M</th>
              <th>Capabilities</th>
            </tr>
          </thead>
          <tbody>
            {models.map((m) => (
              <tr key={`${m.providerId}/${m.modelId}`}>
                <td className={styles.narrow}><RankCell model={m} /></td>
                <td>
                  <span className={styles.modelName}>{m.modelId}</span>
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
                <td className={styles.num}>{formatPrice(m.pricing.inputPerMTokens)}</td>
                <td className={styles.num}>{formatPrice(m.pricing.outputPerMTokens)}</td>
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

function Stat({ value, label }: { value: string; label: string }) {
  return (
    <div className={styles.stat}>
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

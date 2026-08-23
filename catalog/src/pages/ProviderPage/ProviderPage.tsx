import { Fragment, useMemo, useState } from 'react';
import { useParams } from 'react-router-dom';
import {
  LuWrench,
  LuBrain,
  LuBraces,
  LuImage,
  LuMic,
  LuVideo,
  LuPaperclip,
  LuCircleCheck,
  LuTriangleAlert,
  LuCircleX,
  LuLayers,
  LuZap,
  LuActivity,
  LuSparkles,
  LuInfo,
  LuShieldCheck,
  LuCreditCard,
  LuBookOpen,
  LuExternalLink,
  LuChevronDown,
  LuChevronUp,
  LuFileSearch,
  LuPlay,
} from 'react-icons/lu';
import { useCatalog, useProviderModels } from '../../hooks/useCatalog';
import { present } from '../../api/presentation';
import { formatTokens, type ApiModel } from '../../api/client';
import { FreshnessBadge } from '../../components/FreshnessBadge/FreshnessBadge';
import { ModelScoreCell, ModelRankCell, CostCell, pageLocalRanks } from '../../components/ScoreCell/ScoreCell';
import { Toolbar } from '../../components/Toolbar/Toolbar';
import { NotFoundPage } from '../NotFoundPage/NotFoundPage';
import { EvidencePanel } from '../../components/EvidencePanel/EvidencePanel';
import { EvaluateModal } from '../../components/EvaluateModal/EvaluateModal';
import { vendorQualifier } from '../../api/presentation';
import { FactState, factStateOf } from '../../components/FactState/FactState';
import { modelMatchesFilter } from '../../api/filters';
import styles from './ProviderPage.module.css';

export function ProviderPage() {
  const { id } = useParams<{ id: string }>();
  const { loading, error } = useCatalog();
  const { provider, models, meta } = useProviderModels(id);

  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState('all');
  const [view, setView] = useState<'grid' | 'table'>(() => {
    if (typeof window === 'undefined') return 'table';
    try {
      const saved = JSON.parse(window.localStorage.getItem('venom-catalog-settings') ?? '{}') as {
        defaultView?: 'grid' | 'table';
      };
      if (saved.defaultView === 'grid' || saved.defaultView === 'table') return saved.defaultView;
    } catch {
      // Ignore malformed browser preferences and retain the responsive default.
    }
    return window.innerWidth < 768 ? 'grid' : 'table';
  });

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
    if (filter !== 'all') {
      list = list.filter((m) => modelMatchesFilter(m, filter));
    }
    return list;
  }, [models, query, filter]);

  if (loading) return <div className={styles.state}>Loading…</div>;
  if (error) return <div className={styles.state}>Catalog unavailable: {error}</div>;
  if (!provider) return <NotFoundPage />;

  const pres = present(provider.id);
  const maxCtx = Math.max(0, ...models.map((m) => m.contextTokens ?? 0));
  const free = models.filter((m) => m.pricing.isFree === true).length;
  const operationalReady = models.filter((m) => m.vo.value !== null).length;

  const visibleModels = [...filteredModels].sort((a, b) => {
    if (a.overallRank !== null && b.overallRank !== null) return a.overallRank - b.overallRank;
    if (a.overallRank !== null) return -1;
    if (b.overallRank !== null) return 1;
    return a.modelId.localeCompare(b.modelId);
  });

  // Determine status for each stat tile
  const liveModelsStatus: 'ok' | 'warn' | 'error' = provider.liveModels > 0 ? 'ok' : 'error';
  const maxCtxStatus: 'ok' | 'warn' | 'error' = maxCtx > 0 ? 'ok' : 'error';
  /**
   * Unrated rows with nothing recorded about WHY.
   *
   * The distinction the tile turns on. An unrated model whose reason is
   * recorded — no benchmark publishes a figure, the index does not list it yet,
   * the calibration was measured to have no predictive power for the vendor —
   * is a gap in what the industry measured, and this page says exactly that
   * three sections further down. A row with no reason at all is a hole in OUR
   * data, and that is the one thing here we can act on.
   */
  const overallIncomplete = provider.liveModels - provider.overallScoreScored;

  /**
   * The provider's costing, when it is the same answer for every row.
   *
   * A plan covers the whole roster or it is not a plan, so this is a fact about
   * the provider that happened to be rendered per cell — twice in the price
   * columns and again on the VO badge, thirty-nine times over a thirteen-row
   * table. Worse, it was rendered in the `n/a` vocabulary the catalog reserves
   * for facts nobody published, so a settled answer read as a page of holes.
   * Stated once here, the columns are free to show the figure that actually
   * varies. Null when the rows disagree, which after the billing fix can only
   * happen if a provider's policy is genuinely mixed — and then the per-cell
   * labels are the honest rendering and come back on their own.
   */
  const uniformCostKind = (() => {
    if (models.length === 0) return null;
    const kinds = new Set(models.map((m) => m.pricing.kind));
    if (kinds.size !== 1) return null;
    const [only] = [...kinds];
    return only === 'included' || only === 'free' ? only : null;
  })();

  /**
   * The tile flags what is ours to fix, not what the world has not measured.
   *
   * It used to raise the same warning triangle for "one model is unrated" as it
   * does for a provider serving zero models, so a catalog behaving exactly as
   * designed read as a broken one. Aiming the signal is not removing it: an
   * unrated row with no recorded reason still raises it, and so does a provider
   * with nothing to score at all.
   */
  const qualityStatus: 'ok' | 'warn' | 'error' =
    provider.liveModels === 0 || overallIncomplete > 0 ? 'warn' : 'ok';
  const freeStatus: 'ok' | 'warn' | 'error' = 'ok';

  const hasContext = provider.overallScoreScored < provider.liveModels || uniformCostKind === 'included' || Boolean(pres.note);

  return (
    <div className={styles.pageContainer}>
      {/* Enterprise Provider Hero */}
      <header className={styles.header}>
        <div className={styles.headerMain}>
          <div className={styles.logoBox}>
            {pres.logo ? (
              <img src={pres.logo} alt="" className={`${styles.logo} ${pres.invertInDark ? 'logo-invert-dark' : ''}`} />
            ) : (
              <span className={styles.fallbackLogo}>{provider.name.charAt(0)}</span>
            )}
          </div>
          <div className={styles.headerInfo}>
            <h1 className={styles.title}>{provider.name}</h1>
            <p className={styles.subtitle}>{pres.blurb}</p>
          </div>
        </div>

        {/* Right-Aligned Metadata & Action Mini-Cards */}
        <div className={styles.heroMiniGrid}>
          {/* Sync Freshness Mini-Card */}
          <FreshnessBadge provider={provider} />

          {/* Active Models Mini-Card */}
          <div className={styles.heroMiniCard} title={`${provider.liveModels} models currently active in provider roster`}>
            <span className={styles.heroMiniIconEmerald}>
              <LuLayers size={13} />
            </span>
            <div className={styles.heroMiniContent}>
              <span className={styles.heroMiniValue}>{provider.liveModels}</span>
              <span className={styles.heroMiniLabel}>Active Models</span>
            </div>
          </div>

          {/* Subscription / Plan Mini-Card */}
          {uniformCostKind === 'included' && (
            <div className={`${styles.heroMiniCard} ${styles.heroMiniCardBlue}`} title="Included under provider subscription plan">
              <span className={styles.heroMiniIconBlue}>
                <LuCreditCard size={13} />
              </span>
              <div className={styles.heroMiniContent}>
                <span className={styles.heroMiniValue}>Included</span>
                <span className={styles.heroMiniLabel}>Subscription</span>
              </div>
            </div>
          )}

          {/* Official Documentation Action Card */}
          {pres.docsUrl && (
            <a
              href={pres.docsUrl}
              target="_blank"
              rel="noopener noreferrer"
              className={`${styles.heroMiniCard} ${styles.heroDocsCard}`}
              title="Open provider official documentation"
            >
              <span className={styles.heroMiniIconDocs}>
                <LuBookOpen size={13} />
              </span>
              <div className={styles.heroMiniContent}>
                <span className={styles.heroMiniValue}>
                  Docs
                  <LuExternalLink size={10} className={styles.docsArrow} />
                </span>
                <span className={styles.heroMiniLabel}>Official API</span>
              </div>
            </a>
          )}
        </div>
      </header>

      {/* KPI Stats Grid */}
      <div className={styles.stats}>
        <Stat
          value={String(provider.liveModels)}
          label="Live models"
          status={liveModelsStatus}
          statusTitle={provider.liveModels > 0 ? `${provider.liveModels} active models available` : 'No models available'}
          icon={<LuLayers size={15} />}
          subtext={provider.liveModels > 0 ? 'Active in roster' : 'No models listed'}
          accent="emerald"
        />
        <Stat
          value={formatTokens(maxCtx)}
          label="Max context"
          status={maxCtxStatus}
          statusTitle={maxCtx > 0 ? `Max context window: ${formatTokens(maxCtx)}` : 'Context window unknown'}
          icon={<LuZap size={15} />}
          subtext="Peak context window"
          accent="blue"
        />
        <Stat
          value={`${provider.overallScoreScored}/${provider.liveModels}`}
          label="Complete evaluations"
          status={qualityStatus}
          statusTitle={
            provider.overallScoreScored === provider.liveModels
              ? 'Every published model has a complete overall-score-v1 evaluation.'
              : `${overallIncomplete} model(s) still lack sufficient reproducible evidence. Operational data is available for ${operationalReady}/${provider.liveModels}; provider availability is not a benchmark result.`
          }
          testId="quality-tile-status"
          icon={<LuActivity size={15} />}
          progress={provider.liveModels > 0 ? Math.round((provider.overallScoreScored / provider.liveModels) * 100) : 0}
          subtext={provider.liveModels > 0 ? 'Reproducible overall-score-v1' : 'Pending evaluation'}
          accent={qualityStatus === 'ok' ? 'emerald' : 'amber'}
        />
        <Stat
          value={free > 0 ? String(free) : 'None'}
          label="Free models"
          status={freeStatus}
          statusTitle={free > 0 ? `${free} free model(s) available` : 'No zero-cost models, which is normal for a subscription provider.'}
          isFree={free > 0}
          icon={<LuSparkles size={15} />}
          subtext={free > 0 ? 'Zero-cost models' : 'Commercial / subscription'}
          accent={free > 0 ? 'emerald' : 'neutral'}
        />
      </div>

      {/* Unified Toolbar */}
      <Toolbar
        query={query}
        onQueryChange={setQuery}
        filter={filter}
        onFilterChange={setFilter}
        view={view}
        onViewChange={setView}
        placeholder={`Search ${provider.name} models by name or ID...`}
      />

      {view === 'table' ? (
        <ModelTable costStatedOnce={uniformCostKind !== null} title={`Models (${visibleModels.length})`} models={visibleModels} />
      ) : (
        <ModelGrid costStatedOnce={uniformCostKind !== null} title={`Models (${visibleModels.length})`} models={visibleModels} />
      )}

      {/* Unified Enterprise Context Hub */}
      {hasContext && (
        <div className={styles.contextHub}>
          <div className={styles.contextHubHeader}>
            <div className={styles.contextHubTitle}>
              <LuInfo size={15} className={styles.contextHubIcon} />
              <span>What to know before reading these rows</span>
            </div>
            <span className={styles.contextHubTag}>Context & Policies</span>
          </div>

          <div className={styles.contextGrid}>
            {/* Global Catalog Ranking Scope Note */}
            <div className={styles.contextCard}>
              <div className={styles.contextCardHeader}>
                <div className={`${styles.contextBadge} ${styles.contextBadgeScope}`}>
                  <LuLayers size={14} />
                  <span>Catalog Ranking</span>
                </div>
                <span className={styles.contextMeta}>This Page</span>
              </div>
              <p className={styles.contextCardText} data-testid="rank-scope-note">
                {RANK_SCOPE_NOTE}
              </p>
            </div>

            {provider.overallScoreScored < provider.liveModels && (
              <div className={styles.contextCard}>
                <div className={styles.contextCardHeader}>
                  <div className={`${styles.contextBadge} ${styles.contextBadgeWarn}`}>
                    <LuShieldCheck size={14} />
                    <span>Evaluation Integrity</span>
                  </div>
                </div>
                <p className={styles.contextCardText}>
                  Operational data is complete for {operationalReady}/{provider.liveModels}.
                  A successful API request proves a model is reachable, not that it is good —
                  the score above counts only reproducible <code>overall-score-v1</code> evidence.
                </p>
              </div>
            )}

            {uniformCostKind === 'included' && (
              <div className={styles.contextCard}>
                <div className={styles.contextCardHeader}>
                  <div className={`${styles.contextBadge} ${styles.contextBadgeInfo}`}>
                    <LuCreditCard size={14} />
                    <span>Billing Policy</span>
                  </div>
                  <span data-testid="billing-note" />
                  <span className={styles.contextMeta}>Included Plan</span>
                </div>
                <p className={styles.contextCardText}>
                  Every model here is covered by the subscription, which is not the same as free.
                  The <code>ref</code> prices are the rates the plan meters against, shown only so
                  models can be compared — cost is excluded from VO on this page.
                </p>
              </div>
            )}

            {pres.note && (
              <div className={styles.contextCard}>
                <div className={styles.contextCardHeader}>
                  <div className={`${styles.contextBadge} ${styles.contextBadgeScope}`}>
                    <LuBookOpen size={14} />
                    <span>Provider Scope</span>
                  </div>
                  {pres.docsUrl && (
                    <a href={pres.docsUrl} target="_blank" rel="noopener noreferrer" className={styles.contextDocsLink}>
                      Provider docs
                      <LuExternalLink size={12} />
                    </a>
                  )}
                </div>
                <p className={styles.contextCardText}>{pres.note}</p>
              </div>
            )}
          </div>
        </div>
      )}

      {/* Data Provenance & Compliance Audit */}
      <section className={styles.provenance}>
        <div className={styles.provHeader}>
          <div className={styles.provHeaderTitle}>
            <LuShieldCheck size={16} className={styles.provHeaderIcon} />
            <h3 className={styles.provTitle}>Where this data comes from</h3>
          </div>
          <span className={styles.provBadge}>Audit & Verification</span>
        </div>
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
            <span title={provider.lastSuccessfulSyncAt ?? undefined}>
              {provider.lastSuccessfulSyncAt ?? 'never'}
            </span>
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

const rowKey = (m: ApiModel) => `${m.providerId}/${m.modelId}`;

/**
 * The id to show under the model name, and what it means.
 *
 * Two questions share this slot and they are not the same: `canonicalId` is the
 * reference-index entry a SCORE was taken from, `vendorModelId` is which model
 * the row is. A row can have the second without the first — `cline-pass/glm-5.3`
 * is `z-ai/glm-5.3` from Z.ai's own listing, which is also where its 1M context
 * came from — and printing a sentence about the missing first one answered a
 * question the column does not ask. They are told apart by their titles, because
 * rendering them identically would claim a measurement that does not exist.
 */
function displayIdentity(m: ApiModel): { id: string; title: string; fromVendor: boolean } | null {
  if (m.canonicalId && m.canonicalId.replace(/^[^/]+\//, '') !== m.modelId) {
    return {
      id: m.canonicalId,
      title: 'The upstream model this row was proven to be. Two providers serving it share one score.',
      fromVendor: false,
    };
  }
  if (m.vendorModelId) {
    return {
      id: m.vendorModelId,
      title:
        "The vendor's id for this model, from its own listing in the spec feed — not the entry a benchmark figure was taken from, because no reference index lists this model yet.",
      fromVendor: true,
    };
  }
  return null;
}

/**
 * What the rank is measured against.
 *
 * Without it a thirteen-row table numbered #1, #2, #5, #5, #8 … with an `=` on
 * almost every row reads as broken. Both are correct and neither is legible on
 * its own: the ranking is over every scored model in the catalog, so another
 * provider's model sits in each gap, and a tie is usually the SAME model sold by
 * another provider — filtered out of this view, leaving a tie with nobody
 * visible. The number was never wrong; its scope was never stated.
 *
 * The gaps are gone: the column now numbers the rows on screen, and the
 * catalog-wide rank moved to the tooltip. The ties stayed, because they are not
 * a scope problem — `ranking.ts` rule 3 refuses to order two estimates whose
 * intervals overlap, and this note is where a reader learns that a shared
 * position is a statement about the evidence rather than a rendering fault.
 */
const RANK_SCOPE_NOTE =
  'Numbered by position in this list. The catalog-wide rank is on each number’s tooltip, and it can differ: '
  + 'another provider’s offer may sit between two rows here. '
  + 'Only models with a complete overall-score-v1 result are placed; an = marks a tie the evidence cannot separate — '
  + 'two scores whose uncertainty intervals overlap share one position, because a 3-point gap between two ±5.7 '
  + 'estimates is not an ordering.';

/**
 * What the row says when the service did not report an identity state.
 *
 * `unknown` is the one non-resolved state that still prints inline, because it
 * is not a finding about the model at all — it says the response carried no
 * identity field, and nothing else on the row can say that. The two findings
 * (`identity_review`, `unresolved`) stopped printing inline at the owner's
 * direction (2026-08-21): the "not in the reference index" wording sat on every
 * affected row and read as a defect. They remain one click away — the evidence
 * badge carries the refused-candidate count, and the evidence panel's identity
 * section shows the state and every refusal with its evidence.
 */
const IDENTITY_NOTE: Record<
  'unknown',
  { label: (m: ApiModel) => string; title: (m: ApiModel) => string }
> = {
  unknown: {
    label: () => 'identity not reported',
    title: () =>
      'This catalog service response did not carry an identity state for this row. Unknown — not a finding that nothing matched.',
  },
};

/**
 * How much of this row's story is not visible in the row itself.
 *
 * The count is the point: a row with three conflicts and two refused identity
 * candidates should not look the same as one with a clean provenance trail, or
 * nobody will ever open either.
 */
function EvidenceToggle({ model, open, onToggle }: { model: ApiModel; open: boolean; onToggle: () => void }) {
  const resolutionFlag = model.resolution.state === 'complete' ? 0 : 1;
  const scoreFlag = model.overallScore.status === 'complete' ? 0 : 1;
  const flags = model.conflicts.length + model.rejectedCandidates.length + model.missingFacts.length + resolutionFlag + scoreFlag;
  return (
    <button
      type="button"
      className={flags > 0 ? styles.evidenceBtnFlagged : styles.evidenceBtn}
      onClick={onToggle}
      aria-expanded={open}
      title={
        flags > 0
          ? `Inspect evidence: ${model.missingFacts.length} missing, ${model.conflicts.length} conflicted, ${model.rejectedCandidates.length} refused candidate(s)`
          : 'Inspect evidence & provenance trail'
      }
      aria-label={`Inspect evidence for ${model.modelId}`}
      data-testid={`evidence-toggle-${model.modelId}`}
    >
      <LuFileSearch size={13} className={styles.btnIcon} />
      <span className={styles.srOnly}>{open ? 'hide' : 'why'}</span>
      {flags > 0 && <span className={styles.evidenceCount}>{flags}</span>}
    </button>
  );
}

/**
 * Opens the evaluation modal for one row.
 *
 * Beside the evidence toggle on purpose: "why is this value what it is" and
 * "go measure it" are the two things an owner wants from a row that is missing
 * something.
 */
function EvaluateButton({ model, onOpen }: { model: ApiModel; onOpen: () => void }) {
  return (
    <button
      type="button"
      className={styles.evaluateBtn}
      onClick={onOpen}
      title="Run missing evaluations for this model"
      aria-label={`Evaluate ${model.modelId}`}
      data-testid={`evaluate-${model.modelId}`}
    >
      <LuPlay size={12} className={styles.btnIcon} />
      <span className={styles.srOnly}>run</span>
    </button>
  );
}

function VendorQualifierBadge({ qualifier }: { qualifier: string }) {
  const lower = qualifier.toLowerCase();
  const isNew = lower.includes('new');
  const isMultiplier = lower.includes('usage') || /\d+x/i.test(lower);
  const isDiscount = lower.includes('off') || lower.includes('free') || lower.includes('%');

  let typeClass = styles.promoDefault;
  if (isNew) typeClass = styles.promoNew;
  else if (isMultiplier) typeClass = styles.promoMultiplier;
  else if (isDiscount) typeClass = styles.promoDiscount;

  return (
    <span
      className={`${styles.vendorQualifier} ${typeClass}`}
      title={`The provider lists this model with qualifier: "${qualifier}".`}
    >
      {isNew && <span className={styles.promoPulseDot} />}
      {isMultiplier && <LuZap size={10} className={styles.promoIcon} />}
      <span>{qualifier}</span>
    </span>
  );
}

function ModelTable({ title, models, note, costStatedOnce }: { title: string; models: ApiModel[]; note?: string; costStatedOnce?: boolean }) {
  // Numbered against the rows actually on screen, so the column reads 1..N with
  // no gap inherited from offers this page does not show.
  const localRanks = pageLocalRanks(models);
  const [openRows, setOpenRows] = useState<Set<string>>(() => new Set());
  const [evaluating, setEvaluating] = useState<ApiModel | null>(null);
  const toggleRow = (k: string) =>
    setOpenRows((prev) => {
      const next = new Set(prev);
      if (next.has(k)) next.delete(k);
      else next.add(k);
      return next;
    });
  if (models.length === 0) return null;
  return (
    <div className={styles.tableWrap}>
      <h3 className={styles.srOnly}>{title}</h3>
      {note && <p className={styles.tableNote}>{note}</p>}

      <CapabilitiesLegend />

      <div className={styles.tableScroll}>
        <table className={styles.table}>
          <thead>
            <tr>
              <th className={styles.narrow}>#</th>
              <th>Model</th>
              <th className={styles.centered}>Score</th>
              <th className={styles.num}>Context</th>
              <th className={styles.num}>Max out</th>
              {/* The column states its own semantics, so the cells do not have
                  to repeat it once per row. */}
              <th
                className={styles.num}
                data-testid="cost-column-in"
                title={
                  costStatedOnce
                    ? 'The published per-million-token input rate this plan meters usage against. Not a charge.'
                    : 'What this provider charges you per million input tokens.'
                }
              >
                In{costStatedOnce ? <span className={styles.thNote}> · ref</span> : ''}
              </th>
              <th
                className={styles.num}
                data-testid="cost-column-out"
                title={
                  costStatedOnce
                    ? 'The published per-million-token output rate this plan meters usage against. Not a charge.'
                    : 'What this provider charges you per million output tokens.'
                }
              >
                Out{costStatedOnce ? <span className={styles.thNote}> · ref</span> : ''}
              </th>
              <th>Capabilities</th>
              <th className={`${styles.narrow} ${styles.centered}`}>Evidence</th>
            </tr>
          </thead>
          <tbody>
            {models.map((m, index) => (
              <Fragment key={`${m.providerId}/${m.modelId}`}>
              <tr className={
                m.overallRank === null && models[index - 1]?.overallRank !== null && index > 0
                  ? styles.firstUnplaced
                  : undefined
              }>
                <td className={styles.narrow}><ModelRankCell model={m} localRanks={localRanks} /></td>
                <td>
                  <span className={styles.modelName}>{m.displayName || m.modelId}</span>
                  {/* The raw id is the API call — keep it one glance away, but
                      the official name leads because it is what the provider's
                      own app calls this model. */}
                  {m.displayName && m.displayName !== m.modelId && (
                    <span className={styles.canonical} title="The provider's model id, as called in the API">
                      {m.modelId}
                    </span>
                  )}
                  {m.lifecycle === 'deprecated' && (
                    <span
                      className={styles.lifecycleBadge}
                      title="The provider has marked this model deprecated. It still answers, and its own app no longer offers it."
                    >
                      deprecated
                    </span>
                  )}
                  {vendorQualifier(m.modelId, m.displayName) && (
                    <VendorQualifierBadge qualifier={vendorQualifier(m.modelId, m.displayName)!} />
                  )}
                  {/* Identity findings (identity_review, unresolved) print
                      nothing inline — owner decision 2026-08-21. Their state
                      and refused candidates stay one click away on the evidence
                      badge, which carries the count. Only `unknown` keeps a
                      note, because it reports a missing field in the response,
                      not a finding about the model, and nothing else on the row
                      can say it. Suppressed once an id can be shown instead:
                      the column asks which model this is, and an id answers
                      it. */}
                  {m.identityState === 'unknown' && !displayIdentity(m)?.fromVendor && (
                    <span className={styles.identityNote} title={IDENTITY_NOTE.unknown.title(m)}>
                      {IDENTITY_NOTE.unknown.label(m)}
                    </span>
                  )}
                  {(() => {
                    const shown = displayIdentity(m);
                    return shown && (
                      <span className={styles.canonical} title={shown.title}>
                        {shown.id}
                      </span>
                    );
                  })()}
                </td>
                <td className={styles.centered}><ModelScoreCell model={m} /></td>
                <td className={styles.num}>
                  <FactState state={factStateOf(m, 'context', m.contextTokens)}>
                    {formatTokens(m.contextTokens)}
                  </FactState>
                </td>
                <td className={styles.num}>
                  <FactState state={factStateOf(m, 'maxOutput', m.maxOutputTokens)}>
                    {formatTokens(m.maxOutputTokens)}
                  </FactState>
                </td>
                <td className={styles.num}><CostCell model={m} side="in" statedOnce={costStatedOnce} /></td>
                <td className={styles.num}><CostCell model={m} side="out" statedOnce={costStatedOnce} /></td>
                <td>
                  <CapabilityBadges model={m} showUnknown />
                </td>
                <td className={`${styles.narrow} ${styles.centered}`}>
                  <div className={styles.rowActions}>
                    <EvidenceToggle model={m} open={openRows.has(rowKey(m))} onToggle={() => toggleRow(rowKey(m))} />
                    <EvaluateButton model={m} onOpen={() => setEvaluating(m)} />
                  </div>
                </td>
              </tr>
              {openRows.has(rowKey(m)) && (
                <tr className={styles.evidenceRow}>
                  <td colSpan={9}>
                    <EvidencePanel model={m} />
                  </td>
                </tr>
              )}
              </Fragment>
            ))}
          </tbody>
        </table>
      </div>
      {evaluating && <EvaluateModal model={evaluating} onClose={() => setEvaluating(null)} />}
    </div>
  );
}

function ModelGrid({ title, models, note, costStatedOnce }: { title: string; models: ApiModel[]; note?: string; costStatedOnce?: boolean }) {
  // Same numbering as the table: the two views of one list must agree.
  const localRanks = pageLocalRanks(models);
  const [openCards, setOpenCards] = useState<Set<string>>(() => new Set());
  const [evaluating, setEvaluating] = useState<ApiModel | null>(null);
  const toggleCard = (key: string) => setOpenCards((previous) => {
    const next = new Set(previous);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    return next;
  });
  if (models.length === 0) return null;
  return (
    <div className={styles.tableWrap}>
      <h3 className={styles.srOnly}>{title}</h3>
      {note && <p className={styles.tableNote}>{note}</p>}

      <CapabilitiesLegend />

      <div className={styles.modelGrid}>
        {models.map((m) => (
          <div key={`${m.providerId}/${m.modelId}`} className={styles.modelCard}>
            <div className={styles.modelCardTop}>
              <div>
                <span className={styles.modelName}>{m.modelId}</span>
                {m.lifecycle === 'deprecated' && (
                  <span
                    className={styles.lifecycleBadge}
                    title="The provider has marked this model deprecated. It still answers, and its own app no longer offers it."
                  >
                    deprecated
                  </span>
                )}
                {vendorQualifier(m.modelId, m.displayName) && (
                  <VendorQualifierBadge qualifier={vendorQualifier(m.modelId, m.displayName)!} />
                )}
                {(() => {
                  const shown = displayIdentity(m);
                  return shown && (
                    <span className={styles.canonical} title={shown.title}>
                      {shown.id}
                    </span>
                  );
                })()}
              </div>
              <ModelRankCell model={m} localRanks={localRanks} />
            </div>

            <div className={styles.modelCardScores}>
              <div className={styles.scorePill}>
                <span className={styles.scoreTag}>Score</span>
                <ModelScoreCell model={m} />
              </div>
            </div>

            <div className={styles.modelCardStats}>
              <div className={styles.miniStat}>
                <span className={styles.miniVal}>
                  <FactState state={factStateOf(m, 'context', m.contextTokens)}>{formatTokens(m.contextTokens)}</FactState>
                </span>
                <span className={styles.miniLbl}>Context</span>
              </div>
              <div className={styles.miniStat}>
                <span className={styles.miniVal}>
                  <FactState state={factStateOf(m, 'maxOutput', m.maxOutputTokens)}>{formatTokens(m.maxOutputTokens)}</FactState>
                </span>
                <span className={styles.miniLbl}>Max out</span>
              </div>
              <div className={styles.miniStat}>
                <span className={styles.miniVal}><CostCell model={m} side="in" statedOnce={costStatedOnce} /></span>
                <span className={styles.miniLbl}>In</span>
              </div>
              <div className={styles.miniStat}>
                <span className={styles.miniVal}><CostCell model={m} side="out" statedOnce={costStatedOnce} /></span>
                <span className={styles.miniLbl}>Out</span>
              </div>
            </div>

            <CapabilityBadges model={m} />
            <div className={styles.rowActions}>
              <EvidenceToggle
                model={m}
                open={openCards.has(rowKey(m))}
                onToggle={() => toggleCard(rowKey(m))}
              />
              <EvaluateButton model={m} onOpen={() => setEvaluating(m)} />
            </div>
            {openCards.has(rowKey(m)) && <EvidencePanel model={m} />}
          </div>
        ))}
      </div>
      {evaluating && <EvaluateModal model={evaluating} onClose={() => setEvaluating(null)} />}
    </div>
  );
}

interface StatProps {
  value: string;
  label: string;
  status: 'ok' | 'warn' | 'error';
  statusTitle?: string;
  isFree?: boolean;
  /** Set where a test needs to read the status back rather than infer it from an icon. */
  testId?: string;
  icon?: React.ReactNode;
  progress?: number;
  subtext?: string;
  accent?: 'emerald' | 'blue' | 'amber' | 'neutral';
}

function Stat({ value, label, status, statusTitle, isFree, testId, icon, progress, subtext, accent = 'neutral' }: StatProps) {
  const Icon = status === 'ok' ? LuCircleCheck : status === 'warn' ? LuTriangleAlert : LuCircleX;

  return (
    <div
      className={`${styles.stat} ${isFree ? styles.freeStat : ''} ${styles[`accent_${accent}`] ?? ''}`}
      title={statusTitle}
    >
      <div className={styles.statTop}>
        <div className={styles.statLabelRow}>
          {icon && <span className={styles.statIconBadge}>{icon}</span>}
          <span className={styles.statLabel}>{label}</span>
        </div>
        <div
          className={`${styles.statStatus} ${styles[`status_${status}`]}`}
          title={statusTitle}
          aria-label={statusTitle}
          data-testid={testId}
          data-status={status}
        >
          <Icon size={14} />
        </div>
      </div>

      <div className={styles.statContent}>
        <span className={`${styles.statValue} ${value === 'None' ? styles.statValueMuted : ''}`}>{value}</span>
        {progress !== undefined && (
          <span className={styles.statProgressPill}>{progress}%</span>
        )}
      </div>

      {progress !== undefined && (
        <div className={styles.statProgress}>
          <div className={styles.statProgressBg}>
            <div
              className={styles.statProgressFill}
              style={{ width: `${Math.min(100, Math.max(0, progress))}%` }}
            />
          </div>
        </div>
      )}

      {subtext && (
        <div className={styles.statSubtext}>
          <span>{subtext}</span>
        </div>
      )}
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

function CapabilitiesLegend() {
  const [open, setOpen] = useState(false);
  const items = [
    { icon: LuWrench, name: 'Tools', desc: 'Function calling & external API integration', cls: 'capTools' },
    { icon: LuBrain, name: 'Reasoning', desc: 'Deep thinking & chain-of-thought processing', cls: 'capReasoning' },
    { icon: LuBraces, name: 'Structured', desc: 'Strict JSON schema & grammar-constrained output', cls: 'capStructured' },
    { icon: LuImage, name: 'Vision', desc: 'Image & visual comprehension', cls: 'capImage' },
    { icon: LuMic, name: 'Audio', desc: 'Voice input & speech understanding', cls: 'capAudio' },
    { icon: LuVideo, name: 'Video', desc: 'Video sequence processing', cls: 'capVideo' },
    { icon: LuPaperclip, name: 'Files', desc: 'File & document upload support', cls: 'capAttachment' },
  ];

  return (
    <div className={styles.capLegendWrap}>
      <button
        type="button"
        className={styles.capLegendToggle}
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
      >
        <div className={styles.capLegendToggleLeft}>
          <span className={styles.capLegendIconGroup}>
            <LuWrench size={12} className={styles.capTools} />
            <LuBrain size={12} className={styles.capReasoning} />
            <LuImage size={12} className={styles.capImage} />
          </span>
          <span className={styles.capLegendTitle}>Model Capabilities Legend</span>
        </div>
        <div className={styles.capLegendToggleRight}>
          <span className={styles.capLegendAction}>{open ? 'Hide legend' : 'Explore capabilities (7)'}</span>
          {open ? <LuChevronUp size={14} /> : <LuChevronDown size={14} />}
        </div>
      </button>

      {open && (
        <div className={styles.capLegendGrid}>
          {items.map((item) => {
            const Icon = item.icon;
            return (
              <div key={item.name} className={styles.capLegendItem}>
                <span className={`${styles.capIcon} ${styles[item.cls]}`}>
                  <Icon size={14} />
                </span>
                <div className={styles.capLegendText}>
                  <strong className={styles.capLegendName}>{item.name}</strong>
                  <span className={styles.capLegendDesc}>{item.desc}</span>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

function CapabilityBadges({ model, showUnknown = false }: { model: ApiModel; showUnknown?: boolean }) {
  const caps: React.ReactNode[] = [];

  if (model.capabilities.tools) {
    caps.push(
      <span
        key="tools"
        className={`${styles.capIcon} ${styles.capTools}`}
        title="Tools (Function calling / API use)"
      >
        <LuWrench size={14} />
      </span>
    );
  }
  if (model.capabilities.reasoning) {
    caps.push(
      <span
        key="reasoning"
        className={`${styles.capIcon} ${styles.capReasoning}`}
        title="Reasoning / Deep Thinking"
      >
        <LuBrain size={14} />
      </span>
    );
  }
  if (model.capabilities.structured) {
    caps.push(
      <span
        key="structured"
        className={`${styles.capIcon} ${styles.capStructured}`}
        title="Structured Output (JSON schema / grammar)"
      >
        <LuBraces size={14} />
      </span>
    );
  }

  if (model.capabilities.attachment) {
    caps.push(
      <span
        key="attachment"
        className={`${styles.capIcon} ${styles.capAttachment}`}
        title="Attachments (Files / Documents)"
        aria-label="Attachments (Files / Documents)"
      >
        <LuPaperclip size={14} />
      </span>
    );
  }

  const modalities = model.inputModalities ?? [];
  modalities.forEach((mod) => {
    if (mod === 'image') {
      caps.push(
        <span
          key="image"
          className={`${styles.capIcon} ${styles.capImage}`}
          title="Image Input (Vision)"
        >
          <LuImage size={14} />
        </span>
      );
    } else if (mod === 'audio') {
      caps.push(
        <span
          key="audio"
          className={`${styles.capIcon} ${styles.capAudio}`}
          title="Audio Input"
        >
          <LuMic size={14} />
        </span>
      );
    } else if (mod === 'video') {
      caps.push(
        <span
          key="video"
          className={`${styles.capIcon} ${styles.capVideo}`}
          title="Video Input"
        >
          <LuVideo size={14} />
        </span>
      );
    }
  });

  return (
    <div className={styles.caps}>
      {caps}
      {showUnknown &&
        (['tools', 'reasoning', 'structured', 'attachment'] as const)
          .filter((f) => model.capabilities[f] === null)
          .map((f) => (
            <CapabilityUnknownIcon key={f} field={f} model={model} />
          ))}
    </div>
  );
}

const CAPABILITY_UNKNOWN: Record<
  'tools' | 'reasoning' | 'structured' | 'attachment',
  { label: string; icon: typeof LuWrench; className: string }
> = {
  tools: { label: 'Tools', icon: LuWrench, className: 'capTools' },
  reasoning: { label: 'Reasoning', icon: LuBrain, className: 'capReasoning' },
  structured: { label: 'Structured output', icon: LuBraces, className: 'capStructured' },
  attachment: { label: 'Attachments', icon: LuPaperclip, className: 'capAttachment' },
};

function CapabilityUnknownIcon({
  field,
  model,
}: {
  field: keyof typeof CAPABILITY_UNKNOWN;
  model: ApiModel;
}) {
  const state = factStateOf(model, field, null);
  const detail = CAPABILITY_UNKNOWN[field];
  const Icon = detail.icon;
  const stateLabel = state === 'conflicted' ? 'sources disagree' : 'not published by any source';

  return (
    <span
      className={`${styles.capIcon} ${styles[detail.className as keyof typeof styles]} ${styles.capState} ${styles[`capState${state[0].toUpperCase()}${state.slice(1)}` as keyof typeof styles]}`}
      title={`${detail.label}: ${stateLabel}`}
      aria-label={`${detail.label} — ${stateLabel}`}
      data-state={state}
    >
      <Icon size={14} />
    </span>
  );
}

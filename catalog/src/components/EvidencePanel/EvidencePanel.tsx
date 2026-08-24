/**
 * Everything the catalog knows about ONE offering, and how it knows it.
 *
 * The question this answers, in one place: *why should I believe this row?* A
 * table can show that a value exists; only this can show where it came from, how
 * strong that evidence is, what is missing and why, who disagreed, and whether
 * the row is even proven to be the model it claims to be.
 *
 * It renders what the API decided. It computes no score, resolves no identity,
 * picks no winner between conflicting sources, and never fills a gap with a
 * plausible default — every one of those decisions already happened on the
 * server, once, where it is tested.
 */

import React from 'react';
import {
  LuShieldCheck,
  LuLayers,
  LuActivity,
  LuCircleAlert,
  LuExternalLink,
  LuInfo,
  LuDatabase,
  LuTriangleAlert,
  LuCheck,
  LuX,
  LuFingerprint,
} from 'react-icons/lu';
import type { ApiModel, FactProvenance, FieldConflict, RejectedCandidate } from '../../api/client';
import styles from './EvidencePanel.module.css';

/** Plain-English reasons for an unrated VQ. The API sends the machine token. */
const UNRATED_REASON: Record<string, string> = {
  identity_unresolved:
    'No upstream index entry matched this id, so there is nothing to attach a score to. Identity is settled against the benchmark index, so a model the index has not listed yet resolves on its own once it appears there.',
  identity_ambiguous:
    'Several upstream candidates matched and a human must choose. A score taken from the wrong one is indistinguishable from a right one.',
  no_published_benchmark:
    'The identity is proven, and nobody has benchmarked this model. The gap is in the world, not in our data.',
  calibration_group_excluded:
    'A figure exists, and the calibration was measured to have no predictive power for this vendor. Mapping it would publish a number the evidence says is wrong.',
};

/** What each evidence state means for how much to trust the value. */
const EVIDENCE_STATE: Record<string, string> = {
  first_party: 'the seller describing its own offer',
  vendor_default:
    'the company that built the model, publishing it from its own storefront — what the model supports, which the host serving it may cap lower',
  pooled_third_party: 'another seller declaring it about the same model',
  index_confirmation: 'a canonical index confirming it — this source can only ever say yes',
  declared_policy: 'a business fact no feed publishes, read from a cited page by hand',
  measured: 'observed directly by an active probe',
};

const FIELD_LABEL: Record<string, string> = {
  context: 'Context window',
  maxOutput: 'Max output',
  modalities: 'Input modalities',
  tools: 'Tool calling',
  reasoning: 'Reasoning',
  structured: 'Structured output',
  attachment: 'Attachments',
  cost: 'Cost semantics',
  billingKind: 'Billing kind',
  effectivePrice: 'Price here',
  referencePrice: 'Reference price elsewhere',
  inputModalities: 'Input modalities',
};

const label = (field: string) => FIELD_LABEL[field] ?? field;

function formatFactValue(field: string, v: unknown): React.ReactNode {
  if (v === null || v === undefined) {
    return <span className={styles.valNull}>null</span>;
  }
  if (typeof v === 'boolean') {
    return (
      <span className={v ? styles.valTrue : styles.valFalse}>
        {v ? <LuCheck size={11} /> : <LuX size={11} />}
        <span>{v ? 'true' : 'false'}</span>
      </span>
    );
  }
  if (Array.isArray(v)) {
    return (
      <span className={styles.valArray}>
        {v.map((item, idx) => (
          <span key={idx} className={styles.valArrayItem}>{String(item)}</span>
        ))}
      </span>
    );
  }
  if (typeof v === 'object') {
    const obj = v as Record<string, unknown>;
    if ('inPerM' in obj && 'outPerM' in obj) {
      const inVal = typeof obj.inPerM === 'number' ? obj.inPerM : Number(obj.inPerM);
      const outVal = typeof obj.outPerM === 'number' ? obj.outPerM : Number(obj.outPerM);
      const fmt = (num: number) => {
        if (num === 0) return '$0.00';
        if (num < 0.01) return `$${num.toFixed(4)}`;
        return `$${num.toFixed(2)}`;
      };
      return (
        <span className={styles.valPrice} title={JSON.stringify(obj)}>
          <span className={styles.valPriceIn}>in: {fmt(inVal)}</span>
          <span className={styles.valPriceDivider}>/</span>
          <span className={styles.valPriceOut}>out: {fmt(outVal)}</span>
        </span>
      );
    }
    return <code className={styles.sideValue}>{JSON.stringify(v)}</code>;
  }
  if (typeof v === 'number') {
    if (field === 'context' || field === 'maxOutput' || field === 'contextTokens' || field === 'maxOutputTokens') {
      return (
        <span className={styles.valNumber} title={`${v.toLocaleString()} tokens`}>
          {v >= 1000 ? `${(v / 1000).toFixed(v % 1000 === 0 ? 0 : 1)}k` : v}{' '}
          <span className={styles.valSub}>({v.toLocaleString()})</span>
        </span>
      );
    }
    return <span className={styles.valNumber}>{v.toLocaleString()}</span>;
  }
  return <span className={styles.valString}>{String(v)}</span>;
}

const show = (v: unknown) => (typeof v === 'object' && v !== null ? JSON.stringify(v) : String(v));

export function EvidencePanel({ model }: { model: ApiModel }) {
  const facts = Object.entries(model.provenanceByField);
  return (
    <div className={styles.panel} data-testid="evidence-panel">
      <div className={styles.summaryGrid}>
        <ReadinessSection model={model} />
        <OverallScoreSection model={model} />
        <ResolutionSection model={model} />
      </div>

      {model.missingFacts.length > 0 && <MissingSection model={model} />}
      {model.vo.notApplicableDimensions.length > 0 && <NotApplicableSection model={model} />}
      {model.conflicts.length > 0 && (
        <ConflictSection conflicts={model.conflicts} open={model.openConflicts.length} />
      )}
      {model.identityState !== 'resolved' && <IdentitySection model={model} />}
      {facts.length > 0 && <ProvenanceSection facts={facts} />}
    </div>
  );
}

const RESOLUTION_LABEL: Record<ApiModel['resolution']['state'], string> = {
  complete: 'Complete',
  processing: 'Processing',
  awaiting_external_benchmark: 'Awaiting benchmark',
  source_incomplete: 'Data incomplete',
  unknown: 'Resolution unavailable',
};

function ResolutionSection({ model }: { model: ApiModel }) {
  const resolution = model.resolution;
  const latestFact = Object.values(model.provenanceByField)
    .filter((fact) => fact.sourceUrl)
    .sort((a, b) => b.resolvedAt.localeCompare(a.resolvedAt))[0];
  return (
    <section className={styles.summaryCard} data-testid="resolution-section">
      <div className={styles.cardHeader}>
        <div className={styles.cardTitleRow}>
          <LuActivity size={14} className={styles.cardIcon} />
          <h4 className={styles.cardHeading}>Resolution Pipeline</h4>
        </div>
        <div className={styles.stateRow}>
          <span className={resolution.state === 'complete' ? styles.ready : styles.notReady}>
            {RESOLUTION_LABEL[resolution.state]}
          </span>
          {resolution.reasons.map((reason) => <span key={reason} className={styles.token}>{reason}</span>)}
        </div>
      </div>
      <div className={styles.cardContent}>
        <p className={styles.note}>
          {latestFact?.sourceUrl && (
            <span className={styles.sourceCheck}>
              Last source checked:{' '}
              <a href={latestFact.sourceUrl} target="_blank" rel="noreferrer noopener" className={styles.inlineSourceLink}>
                {latestFact.sourceUrl}
              </a>
              .{' '}
            </span>
          )}
          {resolution.firstDetectedAt && <>First detected: {resolution.firstDetectedAt}. </>}
          {resolution.lastAttemptAt && <>Last attempt: {resolution.lastAttemptAt}. </>}
          {resolution.nextAttemptAt && <>Next attempt: {resolution.nextAttemptAt}.</>}
          {resolution.state === 'unknown' && 'This snapshot predates the resolution contract.'}
        </p>
      </div>
    </section>
  );
}

/** The server-owned overall score and its audit state; no dimension is recalculated here. */
function OverallScoreSection({ model }: { model: ApiModel }) {
  const score = model.overallScore;
  return (
    <section className={styles.summaryCard} data-testid="overall-score-breakdown">
      <div className={styles.cardHeader}>
        <div className={styles.cardTitleRow}>
          <LuLayers size={14} className={styles.cardIcon} />
          <h4 className={styles.cardHeading}>Overall Score</h4>
        </div>
        <div className={styles.stateRow}>
          <span className={score.status === 'complete' ? styles.ready : styles.notReady}>
            {score.status.replace('_', ' ')}
          </span>
          <span className={styles.token}>{Math.round(score.overallCoverage.percent)}% coverage</span>
        </div>
      </div>
      <div className={styles.cardContent}>
        <div className={styles.scoreFormulaBox}>
          <p className={styles.scoreFormula}>
            {score.value === null ? (
              <>No score is published until every applicable dimension has sufficient evidence.</>
            ) : (
              <>
                Quality {score.qualityScore?.toFixed(1)} x 70% + operations {score.operationalScore?.toFixed(1)} x 30% ={' '}
                <strong className={styles.formulaResult}>{score.display}</strong>
              </>
            )}
          </p>
        </div>
        <div className={styles.metaPillsRow}>
          <span className={styles.token}>{score.methodologyVersion}</span>
          {score.reasons.map((reason) => <span key={reason} className={styles.token}>{reason}</span>)}
          {score.computedAt && <span className={styles.timestampNote}>Computed: {score.computedAt}.</span>}
        </div>
      </div>
    </section>
  );
}

/**
 * Readiness and quality, stated as two independent things.
 *
 * A model can be fully usable and carry no quality score. Conflating the two
 * would either hide working models or imply a score exists where none does.
 */
function ReadinessSection({ model }: { model: ApiModel }) {
  const unrated = model.vq.value === null;
  return (
    <section className={styles.summaryCard}>
      <div className={styles.cardHeader}>
        <div className={styles.cardTitleRow}>
          <LuShieldCheck size={14} className={styles.cardIcon} />
          <h4 className={styles.cardHeading}>Readiness & Quality</h4>
        </div>
        <div className={styles.stateRow}>
          <span className={model.catalogReady ? styles.ready : styles.notReady} data-testid="readiness-state">
            {model.catalogReady ? 'Operationally complete' : 'Incomplete'}
          </span>
          <span className={unrated ? styles.unratedTag : styles.ratedTag} data-testid="quality-state">
            {unrated ? 'Quality unrated' : `VQ ${model.vq.display}`}
          </span>
        </div>
      </div>
      <div className={styles.cardContent}>
        {model.catalogReady && unrated && (
          <p className={styles.note} data-testid="ready-but-unrated">
            Every operational fact is resolved. The missing quality score is a statement about
            published benchmarks, not about this model's usability — it stays in the catalog.
          </p>
        )}
        {unrated && model.vq.unratedReason && (
          <p className={styles.note} data-testid="unrated-reason">
            <span className={styles.token}>{model.vq.unratedReason}</span>{' '}
            {UNRATED_REASON[model.vq.unratedReason] ?? ''}
          </p>
        )}
        {model.vq.evidenceLevel === 'bounded' && model.vq.provenance?.source && (
          <p className={styles.note} data-testid="bound-basis">
            <span className={styles.token}>bounded</span>{' '}
            A reviewed relation to a measured model, not a measurement — the true figure may be
            higher. {model.vq.provenance.source.replace(/^relation:\s*/, '')}
          </p>
        )}
        {!unrated && (
          <p className={styles.noteMuted}>
            Benchmark evidence established under methodology {model.vq.provenance?.methodologyVersion ?? 'v1'}.
          </p>
        )}
      </div>
    </section>
  );
}

function MissingSection({ model }: { model: ApiModel }) {
  return (
    <section className={styles.alertSection} data-testid="missing-section">
      <div className={styles.sectionTitleRow}>
        <LuTriangleAlert size={14} className={styles.alertIcon} />
        <h4 className={styles.heading}>Missing facts</h4>
      </div>
      <p className={styles.note}>
        No source published these. They are named rather than blank, and the row is served
        rather than hidden — but nothing here is shown as though its value were known.
      </p>
      <ul className={styles.list}>
        {model.missingFacts.map((f) => {
          // Only an OPEN dispute can be the reason a field has no value. A
          // settled one has an answer, so blaming the gap on it sends the
          // reader to a conflict that already concluded.
          const conflicted = model.openConflicts.some((c) => c.field === f);
          return (
            <li key={f} className={styles.item} data-testid={`missing-${f}`}>
              <span className={styles.fieldName}>{label(f)}</span>
              <span className={styles.reason}>
                {conflicted
                  ? 'withheld: sources disagreed — see the conflict below'
                  : 'not published by any source we consult'}
              </span>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

function NotApplicableSection({ model }: { model: ApiModel }) {
  return (
    <section className={styles.infoSection} data-testid="notapplicable-section">
      <div className={styles.sectionTitleRow}>
        <LuInfo size={14} className={styles.infoIcon} />
        <h4 className={styles.heading}>Not applicable</h4>
      </div>
      <p className={styles.note}>
        These do not apply to this offering, so they are excluded from the score with the
        remaining weights renormalised. This is an answer, not a gap.
      </p>
      <ul className={styles.list}>
        {model.vo.notApplicableDimensions.map((d) => (
          <li key={d} className={styles.item} data-testid={`na-${d}`}>
            <span className={styles.fieldName}>{label(d)}</span>
            <span className={styles.reason}>
              {d === 'cost' && model.pricing.kind === 'included'
                ? 'billing is a subscription — the plan covers this model, so there is no per-token price to publish. It is not $0.'
                : 'the dimension does not apply to this offering'}
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** Every side of every disagreement, with who said what. No winner is shown. */
/**
 * What this section is actually reporting.
 *
 * It said "sources contradicted each other, so no value was taken" for every
 * conflict it was given, and after fifteen disputes were answered that sentence
 * was false on all fifteen: `gpt-oss:20b` publishes `structured: true`, cited to
 * OpenRouter's supported_parameters listing `structured_outputs`. Both sides are
 * still kept either way — the audit trail is the point — but a reader must be
 * able to tell a withheld field from a settled one.
 */
function describeConflicts(conflicts: FieldConflict[], open: number): string {
  const kept = 'Both sides are kept: a quietly picked winner is indistinguishable from a bug.';
  if (open === 0) {
    return `Sources contradicted each other and something with standing answered, so a value was taken and the dispute is resolved. ${kept}`;
  }
  if (open === conflicts.length) {
    return `Sources contradicted each other, so no value was taken. ${kept}`;
  }
  return `Sources contradicted each other. ${open} of ${conflicts.length} is still open and withholds its value; the rest were answered. ${kept}`;
}

/**
 * The whole history, and the count of it the service still calls open.
 *
 * `open` is passed in rather than re-derived from `conflicts` here: which rows
 * are open is one server-owned judgement, and a copy of it in the panel is free
 * to disagree with the badge and the summary count that read the real one.
 */
function ConflictSection({ conflicts, open }: { conflicts: FieldConflict[]; open: number }) {
  return (
    <section className={styles.alertSection} data-testid="conflict-section">
      <div className={styles.sectionTitleRow}>
        <LuCircleAlert size={14} className={styles.alertIcon} />
        <h4 className={styles.heading}>Source conflicts</h4>
      </div>
      <p className={styles.note}>{describeConflicts(conflicts, open)}</p>
      <div className={styles.conflictGrid}>
        {conflicts.map((c) => (
          <div key={c.field} className={styles.conflict} data-testid={`conflict-${c.field}`}>
            <div className={styles.conflictHead}>
              <span className={styles.fieldName}>{label(c.field)}</span>
              <span className={styles.statusTag}>{c.status}</span>
              <span className={styles.reason}>{c.conflictType.replace(/_/g, ' ')}</span>
            </div>
            <ul className={styles.sides}>
              {c.sides.map((s) => (
                <li key={s.by} className={styles.side}>
                  <code className={styles.sideValue}>{show(s.value)}</code>
                  <span className={styles.sideBy}>declared by {s.by}</span>
                </li>
              ))}
            </ul>
          </div>
        ))}
      </div>
    </section>
  );
}

/** Identity state, and every candidate that was examined and refused. */
function IdentitySection({ model }: { model: ApiModel }) {
  return (
    <section className={styles.section} data-testid="identity-section">
      <div className={styles.sectionTitleRow}>
        <LuFingerprint size={14} className={styles.sectionIcon} />
        <h4 className={styles.heading}>Identity</h4>
      </div>
      <div className={styles.stateRow}>
        <span className={styles.identityTag} data-testid="identity-state">
          {model.identityState.replace(/_/g, ' ')}
        </span>
      </div>
      <p className={styles.note}>
        {model.identityState === 'identity_review' &&
          'Candidates were examined and refused. This row is parked pending a human decision — it is not un-investigated.'}
        {model.identityState === 'unresolved' &&
          'No upstream model matched this id, and no candidate has been examined yet.'}
        {model.identityState === 'unknown' &&
          'This catalog service response did not carry an identity state for this row, so there is nothing to report here — unknown, not a finding that nothing matched. Reload once the service is back on the current contract.'}
      </p>
      {model.rejectedCandidates.map((r) => (
        <RejectedCandidateBlock key={r.candidate ?? 'none'} rejection={r} />
      ))}
    </section>
  );
}

function RejectedCandidateBlock({ rejection: r }: { rejection: RejectedCandidate }) {
  return (
    <div className={styles.rejection} data-testid={`rejection-${r.candidate ?? 'none'}`}>
      <div className={styles.conflictHead}>
        <code className={styles.candidate}>{r.candidate ?? 'no candidate exists'}</code>
        <span className={styles.statusTag}>{r.verdict.replace(/_/g, ' ')}</span>
      </div>
      <p className={styles.why}>{r.why}</p>
      {r.evidence.length > 0 && (
        <ul className={styles.evidence}>
          {r.evidence.map((e) => (
            <li key={e}>{e}</li>
          ))}
        </ul>
      )}
      <div className={styles.meta}>
        {r.sourceUrl && (
          <a className={styles.sourceLink} href={r.sourceUrl} target="_blank" rel="noreferrer noopener">
            <span>{r.sourceUrl}</span>
            <LuExternalLink size={10} />
          </a>
        )}
        <span className={styles.token}>{r.evidenceState}</span>
        <span className={styles.token}>{r.resolverVersion}</span>
        {r.reviewedAt && <span className={styles.token}>reviewed {r.reviewedAt}</span>}
        {r.candidateMeta && <code className={styles.metaJson}>{JSON.stringify(r.candidateMeta)}</code>}
      </div>
    </div>
  );
}

/** Where every resolved value came from, and how strong that evidence is. */
function ProvenanceSection({ facts }: { facts: [string, FactProvenance][] }) {
  return (
    <section className={styles.section} data-testid="provenance-section">
      <div className={styles.provenanceHeader}>
        <div className={styles.sectionTitleRow}>
          <LuDatabase size={14} className={styles.sectionIcon} />
          <h4 className={styles.heading}>Provenance Ledger</h4>
        </div>
        <span className={styles.badgeCount}>{facts.length} Resolved Fields</span>
      </div>
      <p className={styles.note}>
        Every resolved value, and where it was read from. The evidence state is the part the
        source name cannot answer — a seller describing itself and a third party describing
        it both arrive labelled the same.
      </p>
      <div className={styles.tableWrapper}>
        <table className={styles.factTable}>
          <thead>
            <tr>
              <th className={styles.thField}>Field</th>
              <th className={styles.thValue}>Resolved Value</th>
              <th className={styles.thSource}>Source</th>
              <th className={styles.thEvidence}>Evidence State</th>
              <th className={styles.thResolver}>Resolver</th>
              <th className={styles.thProbe}>Probe</th>
            </tr>
          </thead>
          <tbody>
            {facts.map(([field, f]) => (
              <tr key={field} data-testid={`fact-${field}`} className={styles.tableRow}>
                <td className={styles.tdField}>
                  <span className={styles.fieldName}>{label(field)}</span>
                </td>
                <td className={styles.tdValue}>
                  {formatFactValue(field, f.value)}
                </td>
                <td className={styles.tdSource}>
                  {f.sourceUrl ? (
                    <a className={styles.sourceLink} href={f.sourceUrl} target="_blank" rel="noreferrer noopener">
                      <span>{f.source}</span>
                      <LuExternalLink size={10} />
                    </a>
                  ) : (
                    <span className={styles.sourcePlain}>{f.source}</span>
                  )}
                  {f.sourceRef && <span className={styles.ref}>{f.sourceRef}</span>}
                </td>
                <td className={styles.tdEvidence}>
                  <span
                    className={`${styles.evidenceBadge} ${styles[`evidence_${f.evidenceState ?? 'default'}`] ?? ''}`}
                    title={EVIDENCE_STATE[f.evidenceState ?? ''] ?? ''}
                  >
                    {f.evidenceState ?? '—'}
                  </span>
                </td>
                <td className={styles.tdResolver}>
                  <span className={styles.tokenCell}>{f.resolverVersion ?? '—'}</span>
                </td>
                <td className={styles.tdProbe}>
                  <span className={f.probeVersion ? styles.tokenCell : styles.tokenCellMuted}>
                    {f.probeVersion ?? 'not probed'}
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

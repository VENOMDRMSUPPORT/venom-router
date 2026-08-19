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

import type { ApiModel, FactProvenance, FieldConflict, RejectedCandidate } from '../../api/client';
import styles from './EvidencePanel.module.css';

/** Plain-English reasons for an unrated VQ. The API sends the machine token. */
const UNRATED_REASON: Record<string, string> = {
  // Identity is resolved against the benchmark index itself, so "nothing
  // matched" and "the index does not carry this model yet" are one event, not
  // two. The earlier wording said more benchmarks would not help — the opposite
  // of the truth for a model too new to be indexed, and a reader acting on it
  // would go looking for a bind that cannot exist.
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
  // The state that most needs its own words, because it answers a different
  // question from the rest: what the MODEL supports, published by the company
  // that built it — not what this host serves, which nobody published. Cline's
  // own extension shows cline-pass/glm-5.3 at 128K, a fallback it applies to any
  // model missing from OpenRouter; this figure is Z.ai's for the model itself.
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
  // The completeness gate names this one `cost`, the fact table names it
  // `billingKind`. Both reach this map so neither ever renders as a raw key.
  cost: 'Cost semantics',
  billingKind: 'Billing kind',
  effectivePrice: 'Price here',
  referencePrice: 'Reference price elsewhere',
  inputModalities: 'Input modalities',
};

const label = (field: string) => FIELD_LABEL[field] ?? field;
const show = (v: unknown) => (typeof v === 'object' && v !== null ? JSON.stringify(v) : String(v));

export function EvidencePanel({ model }: { model: ApiModel }) {
  const facts = Object.entries(model.provenanceByField);
  return (
    <div className={styles.panel} data-testid="evidence-panel">
      <ReadinessSection model={model} />
      <ResolutionSection model={model} />
      <OverallScoreSection model={model} />
      {model.missingFacts.length > 0 && <MissingSection model={model} />}
      {model.vo.notApplicableDimensions.length > 0 && <NotApplicableSection model={model} />}
      {model.conflicts.length > 0 && <ConflictSection conflicts={model.conflicts} />}
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
    <section className={styles.section} data-testid="resolution-section">
      <h4 className={styles.heading}>Resolution</h4>
      <div className={styles.stateRow}>
        <span className={resolution.state === 'complete' ? styles.ready : styles.notReady}>
          {RESOLUTION_LABEL[resolution.state]}
        </span>
        {resolution.reasons.map((reason) => <span key={reason} className={styles.token}>{reason}</span>)}
      </div>
      <p className={styles.note}>
        {latestFact?.sourceUrl && <>Last source checked: {latestFact.sourceUrl}. </>}
        {resolution.firstDetectedAt && <>First detected: {resolution.firstDetectedAt}. </>}
        {resolution.lastAttemptAt && <>Last attempt: {resolution.lastAttemptAt}. </>}
        {resolution.nextAttemptAt && <>Next attempt: {resolution.nextAttemptAt}.</>}
        {resolution.state === 'unknown' && 'This snapshot predates the resolution contract.'}
      </p>
    </section>
  );
}

/** The server-owned overall score and its audit state; no dimension is recalculated here. */
function OverallScoreSection({ model }: { model: ApiModel }) {
  const score = model.overallScore;
  return (
    <section className={styles.section} data-testid="overall-score-breakdown">
      <h4 className={styles.heading}>Overall score</h4>
      <p className={styles.note}>
        {score.value === null ? (
          <>No score is published until every applicable dimension has sufficient evidence.</>
        ) : (
          <>Quality {score.qualityScore?.toFixed(1)} x 70% + operations {score.operationalScore?.toFixed(1)} x 30% = <strong>{score.display}</strong></>
        )}
      </p>
      <div className={styles.stateRow}>
        <span className={styles.token}>{score.methodologyVersion}</span>
        <span className={score.status === 'complete' ? styles.ready : styles.notReady}>{score.status.replace('_', ' ')}</span>
        <span className={styles.token}>{Math.round(score.overallCoverage.percent)}% coverage</span>
        {score.reasons.map((reason) => <span key={reason} className={styles.token}>{reason}</span>)}
      </div>
      {score.computedAt && <p className={styles.note}>Computed: {score.computedAt}.</p>}
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
    <section className={styles.section}>
      <h4 className={styles.heading}>Readiness</h4>
      <div className={styles.stateRow}>
        <span className={model.catalogReady ? styles.ready : styles.notReady} data-testid="readiness-state">
          {model.catalogReady ? 'Operationally complete' : 'Incomplete'}
        </span>
        <span className={unrated ? styles.unratedTag : styles.ratedTag} data-testid="quality-state">
          {unrated ? 'Quality unrated' : `VQ ${model.vq.display}`}
        </span>
      </div>
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
      {/*
        A bound is the one figure on this page that comes from a person rather
        than a source, so it is the one that most needs its basis visible. Shown
        as "VQ ≥ 53" alone it is indistinguishable from a measurement that
        happens to be written with a sign — which is the whole thing the
        `bounded` level exists to prevent.
      */}
      {model.vq.evidenceLevel === 'bounded' && model.vq.provenance?.source && (
        <p className={styles.note} data-testid="bound-basis">
          <span className={styles.token}>bounded</span>{' '}
          A reviewed relation to a measured model, not a measurement — the true figure may be
          higher. {model.vq.provenance.source.replace(/^relation:\s*/, '')}
        </p>
      )}
    </section>
  );
}

function MissingSection({ model }: { model: ApiModel }) {
  return (
    <section className={styles.section} data-testid="missing-section">
      <h4 className={styles.heading}>Missing facts</h4>
      <p className={styles.note}>
        No source published these. They are named rather than blank, and the row is served
        rather than hidden — but nothing here is shown as though its value were known.
      </p>
      <ul className={styles.list}>
        {model.missingFacts.map((f) => {
          const conflicted = model.conflicts.some((c) => c.field === f);
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
    <section className={styles.section} data-testid="notapplicable-section">
      <h4 className={styles.heading}>Not applicable</h4>
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
function ConflictSection({ conflicts }: { conflicts: FieldConflict[] }) {
  return (
    <section className={styles.section} data-testid="conflict-section">
      <h4 className={styles.heading}>Source conflicts</h4>
      <p className={styles.note}>
        Sources contradicted each other, so no value was taken. Both sides are kept: a
        quietly picked winner is indistinguishable from a bug.
      </p>
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
    </section>
  );
}

/** Identity state, and every candidate that was examined and refused. */
function IdentitySection({ model }: { model: ApiModel }) {
  return (
    <section className={styles.section} data-testid="identity-section">
      <h4 className={styles.heading}>Identity</h4>
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
        {/* Not a finding. The two above are things we established about the
            model; this one is a thing we do not know about the response. */}
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
            {r.sourceUrl}
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
      <h4 className={styles.heading}>Provenance</h4>
      <p className={styles.note}>
        Every resolved value, and where it was read from. The evidence state is the part the
        source name cannot answer — a seller describing itself and a third party describing
        it both arrive labelled the same.
      </p>
      <div className={styles.tableScroll}>
        <table className={styles.factTable}>
          <thead>
            <tr>
              <th>Field</th>
              <th>Value</th>
              <th>Source</th>
              <th>Evidence</th>
              <th>Resolver</th>
              <th>Probe</th>
            </tr>
          </thead>
          <tbody>
            {facts.map(([field, f]) => (
              <tr key={field} data-testid={`fact-${field}`}>
                <td className={styles.fieldName}>{label(field)}</td>
                <td><code className={styles.sideValue}>{show(f.value)}</code></td>
                <td>
                  {f.sourceUrl ? (
                    <a className={styles.sourceLink} href={f.sourceUrl} target="_blank" rel="noreferrer noopener">
                      {f.source}
                    </a>
                  ) : (
                    f.source
                  )}
                  {f.sourceRef && <span className={styles.ref}>{f.sourceRef}</span>}
                </td>
                <td>
                  <span className={styles.token} title={EVIDENCE_STATE[f.evidenceState ?? ''] ?? ''}>
                    {f.evidenceState ?? '—'}
                  </span>
                </td>
                <td className={styles.tokenCell}>{f.resolverVersion ?? '—'}</td>
                {/* Explicitly "not probed" rather than blank: a probe is an
                    optional layer, and its absence is a recorded answer. */}
                <td className={styles.tokenCell}>{f.probeVersion ?? 'not probed'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

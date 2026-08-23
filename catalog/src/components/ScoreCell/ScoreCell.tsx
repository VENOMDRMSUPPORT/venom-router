import { EVIDENCE_LABEL, EVIDENCE_HELP, type ApiModel } from '../../api/client';
import { FactState, factStateOf } from '../FactState/FactState';
import styles from './ScoreCell.module.css';

const OVERALL_PRESENTATION: Record<ApiModel['overallScore']['status'], { label: string; className: string }> = {
  complete: { label: 'Unrated', className: 'unrated' },
  evaluating: { label: 'Evaluating', className: 'processing' },
  insufficient_evidence: { label: 'Insufficient evidence', className: 'incomplete' },
  unknown: { label: 'Unrated', className: 'unrated' },
};

/** Overall-score-v1 presentation. The server owns value, coverage, and state. */
export function ModelScoreCell({ model }: { model: ApiModel }) {
  const score = model.overallScore;

  if (score.value === null) {
    const state = OVERALL_PRESENTATION[score.status];
    const reason = score.reasons.length > 0 ? score.reasons.join(', ') : undefined;
    return (
      <div className={styles.cell}>
        <span className={`${styles.badge} ${styles[state.className]}`} title={reason}>{state.label}</span>
      </div>
    );
  }

  const complete = score.overallCoverage.scored >= score.overallCoverage.applicable;
  const excluded = score.excludedDimensions;
  const totalDimensions = score.includedDimensions.length + excluded.length;
  /**
   * How much of the test set produced this number, folded into the one tooltip
   * the score already carries rather than printed under it.
   *
   * The owner asked for the line gone, and the line is gone. What must not go is
   * the fact: `overallCoverage.percent` is measured against the APPLICABLE
   * dimensions, so a narrower test set certifies itself as 100%. Two records of
   * the same canonical model once came out 11 points apart, both reading
   * "complete", because a disputed capability made vision applicable to one of
   * them — and 24 rows graded on seven dimensions sat in one ranking beside 25
   * graded on eight. The score is right either way; comparing them without
   * knowing the scope is not.
   */
  const scope = excluded.length === 0
    ? ''
    : ` — graded on ${score.includedDimensions.length} of ${totalDimensions} dimensions; not graded: `
      + `${excluded.join(', ')}, which does not apply to this offering, so it is excluded and the remaining `
      + `weights are renormalised. A model graded on all ${totalDimensions} took a wider test.`;
  const breakdown = `${score.methodologyVersion ?? 'overall-score-v1'}: quality ${score.qualityScore?.toFixed(1) ?? 'unknown'} × 70% + operations ${score.operationalScore?.toFixed(1) ?? 'unknown'} × 30% = ${score.display}${scope}`;
  const numVal = score.value ?? 0;
  const scoreStyle = numVal >= 90 ? styles.scoreHigh : numVal >= 80 ? styles.scoreMid : styles.scoreStandard;

  return (
    <div className={styles.cell}>
      <div className={styles.scoreValueWrap}>
        <span className={`${styles.value} ${scoreStyle}`} title={breakdown}>{score.display}</span>
      </div>
      {!complete && (
        <span
          className={`${styles.badge} ${styles.partial}`}
          title={`Scored on ${score.overallCoverage.scored} of ${score.overallCoverage.applicable} applicable dimensions.`}
        >
          {score.overallCoverage.scored} of {score.overallCoverage.applicable} dimensions
        </span>
      )}
    </div>
  );
}

/**
 * Position within the list on screen, numbered from one.
 *
 * The server ranks against the whole catalog, so a provider page inherits gaps
 * from offers that are not on it — clinepass showed 1, 2, 3, 5. Renumbering the
 * groups PRESENT here removes the gap and stops a number moving because some
 * other provider's model was rescored.
 *
 * What it does not do is separate the rows the server calls tied, and that is
 * deliberate: the grouping is the server's, and only its numbering is local.
 * `ranking.ts` rule 3 — two values whose uncertainty intervals overlap are tied,
 * and rendering a 3-point gap between two ±5.7 estimates as an ordering invents
 * precision the evidence does not carry. Seven rows reading #1 is that rule
 * speaking, not a gap this function should paper over.
 */
export function pageLocalRanks(models: ApiModel[]): Map<number, number> {
  const groups = [...new Set(models
    .map((model) => model.overallRank)
    .filter((rank): rank is number => rank !== null))]
    .sort((left, right) => left - right);
  return new Map(groups.map((globalRank, index) => [globalRank, index + 1]));
}

export function ModelRankCell({ model, localRanks }: { model: ApiModel; localRanks?: Map<number, number> }) {
  if (model.overallRank === null) {
    return (
      <span
        className={styles.unplaced}
        title="No complete overall score, so this model is not placed in the ranking."
        data-testid={`model-rank-${model.modelId}`}
      >
        —
      </span>
    );
  }
  // Falls back to the server's number when no list context was given, so a
  // single-row use never silently reports a position of one.
  const shown = localRanks?.get(model.overallRank) ?? model.overallRank;
  return (
    <span
      className={`${styles.rank} ${shown <= 3 ? styles.topRank : ''}`}
      data-testid={`model-rank-${model.modelId}`}
      title={localRanks
        ? `Position ${shown} of ${localRanks.size} scored on this page. Catalog-wide rank: ${model.overallRank}.`
        : undefined}
    >
      #{shown}
      {model.tiedAtOverallRank ? (
        <span className={styles.tie} title="Tied: the overall-score uncertainty intervals overlap.">
          =
        </span>
      ) : null}
    </span>
  );
}

/**
 * VQ presentation.
 *
 * Three rules the backend already enforces, made visible here:
 *  - Calibrated never looks like Measured. Different badge, and the interval is
 *    shown, because a +/-6 estimate and a published figure are not the same claim.
 *  - Unrated shows a dash and the word "unknown", never a zero and never a low
 *    number. Unknown quality is not low quality.
 *  - The value is rendered at the precision the server declared. The client does
 *    not get to add a decimal.
 */
export function VQCell({ model }: { model: ApiModel }) {
  const { vq } = model;

  if (vq.value === null) {
    return (
      <div className={styles.cell}>
        <span className={styles.unknown} title={EVIDENCE_HELP.unrated}>
          —
        </span>
        <span className={`${styles.badge} ${styles.unrated}`}>Unrated</span>
      </div>
    );
  }

  const interval =
    vq.uncertainty !== null && vq.uncertainty >= 0.5
      ? `± ${vq.uncertainty.toFixed(1)}`
      : null;

  return (
    <div className={styles.cell}>
      <span className={styles.value}>
        {vq.display}
        {interval && <span className={styles.interval}>{interval}</span>}
      </span>
      <span
        className={`${styles.badge} ${styles[vq.evidenceLevel]}`}
        title={`${EVIDENCE_HELP[vq.evidenceLevel]}${
          vq.provenance ? `\n\nSource: ${vq.provenance.source}\nUpstream model: ${vq.provenance.sourceModelId}\nIdentity rule: ${vq.provenance.identityRule}\nMethodology: ${vq.provenance.methodologyVersion}` : ''
        }`}
      >
        {EVIDENCE_LABEL[vq.evidenceLevel]}
      </span>
    </div>
  );
}

/** VO is derived from published facts, so it carries coverage, not uncertainty. */
export function VOCell({ model, statedOnce }: { model: ApiModel; statedOnce?: boolean }) {
  const missing = model.vo.missingDimensions;
  const notApplicable = model.vo.notApplicableDimensions;

  // No operational fact published at all. Zero would read as "worst"; a dash
  // reads as "unknown", which is what it is.
  if (model.vo.value === null) {
    return (
      <div className={styles.cell}>
        <span className={styles.unknown} title="No operational data is published for this model.">—</span>
        <span className={`${styles.badge} ${styles.unrated}`}>No data</span>
      </div>
    );
  }

  const dims = Object.entries(model.vo.dimensions)
    .filter(([, v]) => v !== null)
    .map(([k, v]) => `${k}: ${Math.round(v as number)}`)
    .join('\n');

  return (
    <div className={styles.cell}>
      <span className={styles.value} title={dims || undefined}>
        {Math.round(model.vo.value)}
      </span>
      {missing.length > 0 && (
        <span
          className={`${styles.badge} ${styles.partial}`}
          title={`Computed without: ${missing.join(', ')} — nobody publishes those facts for this model.`}
        >
          {missing.length} missing
        </span>
      )}
      {/* A dimension that does not APPLY is not a gap and must not wear the same
          badge. Both leave the weighted mean; only one is open work. */}
      {notApplicable.length > 0 && !statedOnce && (
        <span
          className={`${styles.badge} ${styles.unrated}`}
          title={`${notApplicable.join(', ')} does not apply to this offering — excluded from the score with the remaining weights renormalised. This is an answer, not a missing value.`}
          data-testid="vo-notapplicable"
        >
          {notApplicable.join(', ')} n/a
        </span>
      )}
    </div>
  );
}

/**
 * Rank cell. A tie is shown as a tie: when the server grouped rows because their
 * uncertainty intervals overlap, presenting a strict order here would reinvent
 * the precision the backend just refused to claim.
 */
export function RankCell({ model }: { model: ApiModel }) {
  if (model.qualityRank === null) {
    return <span className={styles.unplaced} title="No quality evidence, so this model is not placed in the quality ranking.">—</span>;
  }
  return (
    <span className={styles.rank}>
      #{model.qualityRank}
      {model.tiedAtRank && (
        <span className={styles.tie} title="Tied: the uncertainty intervals of these scores overlap, so the evidence does not separate them.">
          =
        </span>
      )}
    </span>
  );
}

/**
 * Cost, with its semantics visible.
 *
 * `Included` is not a missing price — it is the true answer for a model covered
 * by a subscription, and inventing a $/M figure there would be a number from a
 * different seller. The list price elsewhere is still shown, faintly and
 * labelled `ref`, so the model can be compared without that figure being
 * mistaken for a bill.
 */
export function CostCell({ model, side, statedOnce }: { model: ApiModel; side: 'in' | 'out'; statedOnce?: boolean }) {
  const p = model.pricing;
  const own = side === 'in' ? p.inputPerMTokens : p.outputPerMTokens;
  const ref = side === 'in' ? p.referenceInPerMTokens : p.referenceOutPerMTokens;
  const money = (v: number) => (v === 0 ? 'Free' : v < 1 ? `$${v.toFixed(2)}` : `$${v.toFixed(2).replace(/\.00$/, '')}`);

  const refNote = ref !== null && ref > 0 && (
    <span className={styles.refPrice} title="List price for this model elsewhere. Shown for comparison — it is not what this provider charges you.">
      ref {money(ref)}
    </span>
  );

  if (p.kind === 'per_token' && own !== null) {
    return <div className={styles.costCell}><span className={styles.value}>{money(own)}</span></div>;
  }
  if (p.kind === 'free') {
    return <div className={styles.costCell}><span className={styles.free}>Free</span>{refNote}</div>;
  }
  if (p.kind === 'included') {
    // `statedOnce` means the caller has already said, once for the whole
    // provider, that the plan covers every model. Repeating it per cell printed
    // one provider-level sentence three times per row — twice in the price
    // columns and again on the VO badge — in the same `n/a` vocabulary the
    // catalog uses for ABSENT facts. An answer rendered as a page full of holes
    // is a worse failure than a missing label: it reads as broken data.
    if (statedOnce) {
      // No per-cell `ref` prefix: the column header carries it, once, for the
      // same reason the plan itself is stated once above the table. The marker
      // cannot simply be dropped — a bare `$3` under a subscription reads as
      // what you pay, which is the one claim the provider's documentation
      // denies — so it moves rather than disappearing.
      return (
        <div className={styles.costCell}>
          {ref !== null && ref > 0 ? (
            <span
              className={styles.refPrice}
              title="The published per-million-token rate this plan meters usage against. Not a charge — the subscription covers this model."
            >
              {money(ref)}
            </span>
          ) : (
            <span className={styles.refPrice} title="No rate is published for this model, so there is no figure to compare against. The plan still covers it.">
              —
            </span>
          )}
        </div>
      );
    }
    return (
      <div className={styles.costCell}>
        <span
          className={styles.included}
          title="Covered by this provider's subscription: the plan includes this model, so there is no per-token price to publish. This is NOT $0 — the cost dimension does not apply, and it is excluded from VO with the remaining weights renormalised."
          data-testid="cost-notapplicable"
        >
          Included · n/a
        </span>
        {refNote}
      </div>
    );
  }
  // A per-token provider that published no price. Named as a gap rather than
  // rendered as an em dash, which a reader cannot tell apart from "not
  // applicable" — and those are opposite claims.
  return (
    <div className={styles.costCell}>
      <FactState state={factStateOf(model, 'billingKind', null)} />
      {refNote}
    </div>
  );
}

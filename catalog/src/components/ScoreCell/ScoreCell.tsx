import { EVIDENCE_LABEL, EVIDENCE_HELP, type ApiModel } from '../../api/client';
import styles from './ScoreCell.module.css';

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
export function VOCell({ model }: { model: ApiModel }) {
  const missing = model.vo.missingDimensions;

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
          title={`Computed without: ${missing.join(', ')} — those facts are not published for this model.`}
        >
          {missing.length} missing
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

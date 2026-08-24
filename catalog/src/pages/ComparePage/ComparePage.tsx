import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { LuArrowUpRight, LuTriangleAlert } from 'react-icons/lu';
import { useCatalog } from '../../hooks/useCatalog';
import { formatTokens, type ApiModel } from '../../api/client';
import { groupByCanonicalModel, type CrossProviderGroup, type CrossProviderOffer } from '../../api/cross-provider';
import { present } from '../../api/presentation';
import styles from './ComparePage.module.css';

/**
 * The same model, offered by more than one provider.
 *
 * Every figure here comes from the catalog API and is grouped by the identity
 * the sync already settled — this page derives nothing about a model that the
 * provider pages do not already state. What it adds is the one question those
 * pages structurally cannot answer, because each shows a single seller: this
 * model is sold in more than one place, so which one should a request go to?
 *
 * The quality half of an overall score belongs to the model, not the seller, so
 * within a group it is identical by construction and the entire difference is
 * operational. Where that is NOT true, the offers were graded on different
 * dimension sets — and the page refuses to name a winner rather than passing a
 * difference in the exam off as a difference in the provider.
 */
export function ComparePage() {
  const { data, loading, error } = useCatalog();
  const [onlyDisagreeing, setOnlyDisagreeing] = useState(false);

  const groups = useMemo(() => groupByCanonicalModel(data?.models ?? []), [data]);
  const shown = onlyDisagreeing ? groups.filter((g) => !g.comparable) : groups;
  const notComparable = groups.filter((g) => !g.comparable).length;

  if (error) return <div className={styles.state}>Catalog unavailable: {error}</div>;
  if (loading && data === null) return <div className={styles.state}>Loading…</div>;

  return (
    <div>
      <header className={styles.header}>
        <h1 className={styles.title}>Same model, which provider?</h1>
        <p className={styles.subtitle}>
          Models this catalog can reach through more than one provider. The quality half of
          an overall score belongs to the model, so within a group the difference is
          operational — speed, headroom, and what it costs here. Widest gap first.
        </p>
        <div className={styles.summary}>
          <span className={styles.stat}>
            <strong data-testid="compare-count">{groups.length}</strong> models sold more than once
          </span>
          {notComparable > 0 && (
            <button
              type="button"
              className={`${styles.filterChip} ${onlyDisagreeing ? styles.filterOn : ''}`}
              onClick={() => setOnlyDisagreeing((v) => !v)}
            >
              <LuTriangleAlert size={13} />
              {notComparable} graded differently
            </button>
          )}
        </div>
      </header>

      {shown.length === 0 && (
        <div className={styles.state} data-testid="compare-empty">
          {groups.length === 0
            ? 'No model in this catalog is offered by more than one provider, so there is nothing to compare.'
            : 'Every group here was graded the same way.'}
        </div>
      )}

      <div className={styles.groups}>
        {shown.map((group) => (
          <Group key={group.canonicalId} group={group} />
        ))}
      </div>
    </div>
  );
}

function Group({ group }: { group: CrossProviderGroup }) {
  return (
    <section className={styles.group} data-testid={`compare-group-${group.canonicalId}`}>
      <div className={styles.groupHead}>
        <div>
          <h2 className={styles.groupTitle}>{group.displayName}</h2>
          <code className={styles.canonical}>{group.canonicalId}</code>
        </div>
        {group.comparable ? (
          <div className={styles.verdict}>
            {group.best && (
              <span className={styles.best} data-testid="compare-best">
                best here: <strong>{group.best.providerId}</strong>
              </span>
            )}
            {group.spread !== null && (
              <span
                className={styles.spread}
                data-testid="compare-spread"
                title="Points between the best and worst scored offer of this model. The quality half is shared, so this gap is operational."
              >
                {group.spread.toFixed(1)} pts apart
              </span>
            )}
          </div>
        ) : (
          <p className={styles.notComparable} data-testid="compare-not-comparable">
            <LuTriangleAlert size={14} />
            Not comparable: these offers were graded on different dimensions
            {' — '}
            <strong>{group.gradedOnDifferentDimensions.join(', ')}</strong>
            {' '}applied to some and not others. The scores are each correct on their own
            test set; the gap between them is not the provider.
          </p>
        )}
      </div>

      <table className={styles.table}>
        <thead>
          <tr>
            <th>Provider</th>
            <th>Overall</th>
            <th>Operational</th>
            <th>Cost</th>
            <th>Context</th>
            <th>Max out</th>
            <th aria-label="Open provider" />
          </tr>
        </thead>
        <tbody>
          {group.offers.map((offer, index) => (
            <Offer
              key={`${offer.providerId}/${offer.model.modelId}`}
              offer={offer}
              leader={group.comparable && index === 0 && offer.model.overallScore.value !== null}
            />
          ))}
        </tbody>
      </table>
    </section>
  );
}

function Offer({ offer, leader }: { offer: CrossProviderOffer; leader: boolean }) {
  const { model } = offer;
  const score = model.overallScore;
  const brand = present(offer.providerId);

  return (
    <tr
      className={leader ? styles.leaderRow : undefined}
      data-testid={`compare-offer-${offer.providerId}`}
    >
      <td>
        <Link to={`/provider/${offer.providerId}`} className={styles.providerLink}>
          {brand.logo && (
            <img
              src={brand.logo}
              alt=""
              className={`${styles.logo} ${brand.invertInDark ? 'logo-invert-dark' : ''}`}
            />
          )}
          {offer.providerId}
        </Link>
        <span className={styles.modelId}>{model.modelId}</span>
      </td>
      <td>
        {/* A dash, never a zero: nobody scored this offer, which is not the same
            claim as scoring it badly. */}
        {score.value === null ? (
          <span className={styles.unrated}>Unrated</span>
        ) : (
          <span className={styles.score}>{score.display}</span>
        )}
      </td>
      <td>
        {score.operationalScore === null ? (
          <span className={styles.unrated}>—</span>
        ) : (
          <span className={styles.operational}>{score.operationalScore.toFixed(1)}</span>
        )}
      </td>
      <td><Cost model={model} /></td>
      <td className={styles.numeric}>{formatTokens(model.contextTokens)}</td>
      <td className={styles.numeric}>{formatTokens(model.maxOutputTokens)}</td>
      <td>
        <Link to={`/provider/${offer.providerId}`} className={styles.open} aria-label={`Open ${offer.providerId}`}>
          <LuArrowUpRight size={14} />
        </Link>
      </td>
    </tr>
  );
}

/**
 * Cost in the vocabulary the rest of the catalog uses: `included` is a real
 * answer for a subscription, not a missing number, and rendering it as $0 would
 * make a plan look free.
 */
function Cost({ model }: { model: ApiModel }) {
  const p = model.pricing;
  if (p.kind === 'free') return <span className={styles.free}>Free</span>;
  if (p.kind === 'included') {
    return (
      <span className={styles.included} title="Covered by this provider's subscription — there is no per-token price to publish.">
        Included
      </span>
    );
  }
  // Either half can be published without the other, and each says so on its own.
  // Requiring the input price hid a published output price entirely, and
  // printing `?? 0` for a missing one showed $0.00 — a figure the provider never
  // published, in the one column where a zero reads as "free".
  if (p.kind === 'per_token' && (p.inputPerMTokens !== null || p.outputPerMTokens !== null)) {
    const perM = (value: number | null) => (value === null ? '—' : `${value.toFixed(2)}`);
    return (
      <span className={styles.price}>
        {perM(p.inputPerMTokens)}
        <span className={styles.priceOut}> / {perM(p.outputPerMTokens)}</span>
      </span>
    );
  }
  return <span className={styles.unrated}>—</span>;
}

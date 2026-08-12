import { LuCircleCheck, LuClock, LuTriangleAlert, LuCircleMinus } from 'react-icons/lu';
import { formatAgo, type ApiProvider } from '../../api/client';
import styles from './FreshnessBadge.module.css';

/**
 * Operational truth about one provider, replacing the old "100% API Verified"
 * claim.
 *
 * The wording is deliberately about the *check*, never about the provider's
 * future behaviour: "synced 20m ago" is a fact we can stand behind, while
 * "verified" implies a guarantee that a sync does not give.
 *
 * A failed attempt does not make the row fresh. Freshness follows the last
 * SUCCESS, and the failure is shown next to it rather than instead of it.
 */
export function FreshnessBadge({ provider, compact = false }: { provider: ApiProvider; compact?: boolean }) {
  const { freshness, lastSuccessfulSyncAt, lastOutcome, lastAttemptedSyncAt } = provider;
  const failedSince =
    lastOutcome && lastOutcome !== 'ok' && lastAttemptedSyncAt && lastAttemptedSyncAt !== lastSuccessfulSyncAt;

  const tone = freshness === 'never' ? 'never' : failedSince ? 'warn' : freshness === 'fresh' ? 'ok' : 'stale';
  const Icon = tone === 'ok' ? LuCircleCheck : tone === 'warn' ? LuTriangleAlert : tone === 'stale' ? LuClock : LuCircleMinus;

  const label =
    freshness === 'never'
      ? 'never synced'
      : `synced ${formatAgo(lastSuccessfulSyncAt)}`;

  return (
    <span
      className={`${styles.badge} ${styles[tone]} ${compact ? styles.compact : ''}`}
      title={
        `Last successful sync: ${lastSuccessfulSyncAt ?? 'never'}\n` +
        `Last attempt: ${lastAttemptedSyncAt ?? 'never'} (${lastOutcome ?? 'n/a'})\n\n` +
        'This says when the roster was last read from the provider. It is not a promise about what the provider serves right now.'
      }
    >
      <Icon size={12} aria-hidden />
      {label}
      {failedSince && <span className={styles.suffix}>· last attempt {lastOutcome}</span>}
    </span>
  );
}

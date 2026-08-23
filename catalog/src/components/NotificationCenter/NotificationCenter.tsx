import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  LuBell,
  LuCheckCheck,
  LuChevronRight,
  LuCircleAlert,
  LuLoaderCircle,
} from 'react-icons/lu';
import { fetchAlerts, formatAgo, updateAlertStatus, type AlertRecord } from '../../api/client';
import styles from './NotificationCenter.module.css';

const MAX_VISIBLE_NOTIFICATIONS = 5;
export const ALERT_REFRESH_INTERVAL_MS = 30_000;

interface NotificationCenterProps {
  providerId?: string;
}

export function NotificationCenter({ providerId }: NotificationCenterProps) {
  const [alerts, setAlerts] = useState<AlertRecord[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [open, setOpen] = useState(false);
  const [acknowledgingIds, setAcknowledgingIds] = useState<Set<string>>(() => new Set());
  const containerRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const latestAlertRequest = useRef(0);

  const refreshAlerts = useCallback(async (signal?: AbortSignal) => {
    const requestNumber = ++latestAlertRequest.current;
    try {
      const result = await fetchAlerts('open', signal);
      if (signal?.aborted || requestNumber !== latestAlertRequest.current) return;
      setAlerts(result.alerts);
      setError(null);
    } catch (reason) {
      if (signal?.aborted || requestNumber !== latestAlertRequest.current) return;
      setError(reason instanceof Error ? reason.message : String(reason));
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    const refreshIfVisible = () => {
      if (document.visibilityState === 'visible') void refreshAlerts(controller.signal);
    };

    void refreshAlerts(controller.signal);
    const interval = window.setInterval(refreshIfVisible, ALERT_REFRESH_INTERVAL_MS);
    document.addEventListener('visibilitychange', refreshIfVisible);
    window.addEventListener('focus', refreshIfVisible);
    return () => {
      controller.abort();
      window.clearInterval(interval);
      document.removeEventListener('visibilitychange', refreshIfVisible);
      window.removeEventListener('focus', refreshIfVisible);
    };
  }, [refreshAlerts]);

  useEffect(() => {
    if (!open) return;

    const closeOnOutsidePress = (event: PointerEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      event.preventDefault();
      setOpen(false);
      triggerRef.current?.focus();
    };

    document.addEventListener('pointerdown', closeOnOutsidePress);
    document.addEventListener('keydown', closeOnEscape);
    return () => {
      document.removeEventListener('pointerdown', closeOnOutsidePress);
      document.removeEventListener('keydown', closeOnEscape);
    };
  }, [open]);

  const matchingAlerts = useMemo(() => {
    const relevantAlerts = providerId
      ? (alerts ?? []).filter((alert) => alert.providerId === providerId)
      : (alerts ?? []);
    return relevantAlerts
      .slice()
      .sort((a, b) => new Date(b.lastSeenAt).getTime() - new Date(a.lastSeenAt).getTime());
  }, [alerts, providerId]);

  const notifications = matchingAlerts.slice(0, MAX_VISIBLE_NOTIFICATIONS);
  const notificationCount = matchingAlerts.length;
  const triggerLabel = notificationCount > 0
    ? `Notifications, ${notificationCount} open`
    : 'Notifications';

  const acknowledgeAlerts = async (alertsToAcknowledge: AlertRecord[]) => {
    if (alertsToAcknowledge.length === 0) return;

    const ids = alertsToAcknowledge.map((alert) => alert.id);
    setActionError(null);
    setAcknowledgingIds((current) => new Set([...current, ...ids]));

    const results = await Promise.allSettled(
      alertsToAcknowledge.map((alert) => updateAlertStatus(alert.id, 'acknowledged')),
    );
    const acknowledged = new Set(
      results.flatMap((result, index) => result.status === 'fulfilled' ? [ids[index]] : []),
    );

    if (acknowledged.size > 0) {
      setAlerts((current) => current?.filter((alert) => !acknowledged.has(alert.id)) ?? null);
    }
    if (acknowledged.size !== ids.length) {
      setActionError('One or more alerts could not be acknowledged. Please try again.');
    }
    setAcknowledgingIds((current) => new Set([...current].filter((id) => !ids.includes(id))));
  };

  return (
    <div className={styles.container} ref={containerRef}>
      <button
        ref={triggerRef}
        type="button"
        className={`${styles.trigger} ${open ? styles.triggerOpen : ''}`}
        onClick={() => setOpen((value) => !value)}
        aria-label={triggerLabel}
        aria-haspopup="dialog"
        aria-controls="catalog-notifications"
        aria-expanded={open}
        title={triggerLabel}
      >
        <LuBell size={16} aria-hidden="true" />
        {notificationCount > 0 && (
          <span className={styles.badge} aria-hidden="true">
            {notificationCount > 9 ? '9+' : notificationCount}
          </span>
        )}
      </button>

      {open && (
        <section id="catalog-notifications" className={styles.popover} role="dialog" aria-label="Catalog notifications">
          <header className={styles.popoverHeader}>
            <div>
              <p className={styles.eyebrow}>Operational alerts</p>
              <h2 className={styles.title}>Notifications</h2>
            </div>
            {notificationCount > 0 && (
              <button
                type="button"
                className={styles.acknowledgeAll}
                onClick={() => void acknowledgeAlerts(notifications)}
                disabled={acknowledgingIds.size > 0}
              >
                <LuCheckCheck size={14} aria-hidden="true" />
                Acknowledge all
              </button>
            )}
          </header>

          <div className={styles.content} aria-live="polite">
            {alerts === null && !error && (
              <div className={styles.state}>
                <LuLoaderCircle size={16} className={styles.spinner} aria-hidden="true" />
                Loading open alerts…
              </div>
            )}

            {error && alerts === null && (
              <div className={`${styles.state} ${styles.errorState}`}>
                <LuCircleAlert size={16} aria-hidden="true" />
                Alert ledger unavailable. Open alerts could not be loaded.
              </div>
            )}

            {alerts !== null && !error && notificationCount === 0 && (
              <div className={styles.state}>No open alerts for this provider.</div>
            )}

            {notifications.length > 0 && (
              <ul className={styles.list} aria-label="Open operational alerts">
                {notifications.map((alert) => {
                  const isAcknowledging = acknowledgingIds.has(alert.id);
                  const target = alert.providerId ? `/provider/${encodeURIComponent(alert.providerId)}` : '/';
                  const targetLabel = alert.modelId ?? alert.providerId ?? 'Catalog service';
                  return (
                    <li key={alert.id} className={styles.item}>
                      <span className={styles.itemTone} data-severity={alert.severity} aria-hidden="true" />
                      <span className={styles.itemBody}>
                        <span className={styles.itemTopline}>
                          <span className={styles.itemLabel}>{alert.title}</span>
                          <time className={styles.time} dateTime={alert.lastSeenAt}>{formatAgo(alert.lastSeenAt)}</time>
                        </span>
                        <span className={styles.itemTarget}>{targetLabel}</span>
                        <span className={styles.itemDetail}>{alert.detail}</span>
                      </span>
                      <span className={styles.itemActions}>
                        <Link
                          to={target}
                          className={styles.targetLink}
                          onClick={() => setOpen(false)}
                          aria-label={`Open details for ${targetLabel}`}
                          title="Open alert target"
                        >
                          <LuChevronRight size={15} aria-hidden="true" />
                        </Link>
                        <button
                          type="button"
                          className={styles.acknowledgeButton}
                          onClick={() => void acknowledgeAlerts([alert])}
                          disabled={isAcknowledging || acknowledgingIds.size > 0}
                          aria-label={`Acknowledge ${alert.title}`}
                        >
                          {isAcknowledging ? '…' : 'Ack'}
                        </button>
                      </span>
                    </li>
                  );
                })}
              </ul>
            )}

            {actionError && <p className={styles.actionError} role="alert">{actionError}</p>}
            {error && alerts !== null && <p className={styles.refreshError} role="status">Unable to refresh alerts. Showing the latest loaded state.</p>}
          </div>

          <Link to="/" className={styles.viewAll} onClick={() => setOpen(false)}>
            Open alerts dashboard
            <LuChevronRight size={15} aria-hidden="true" />
          </Link>
        </section>
      )}
    </div>
  );
}

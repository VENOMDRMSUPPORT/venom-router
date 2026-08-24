import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  LuBell,
  LuCheckCheck,
  LuChevronRight,
  LuCircleAlert,
  LuCircleCheck,
  LuCircleX,
  LuLoaderCircle,
  LuTriangleAlert,
} from 'react-icons/lu';
import {
  fetchCatalogNotifications,
  formatAgo,
  markCatalogNotificationsRead,
  type CatalogNotification,
  type CatalogNotificationsResponse,
} from '../../api/client';
import styles from './NotificationCenter.module.css';

export const NOTIFICATION_REFRESH_INTERVAL_MS = 30_000;

interface NotificationCenterProps {
  providerId?: string;
}

function NotificationIcon({ category }: { category: CatalogNotification['category'] }) {
  if (category === 'success') return <LuCircleCheck size={16} aria-hidden="true" />;
  if (category === 'error') return <LuCircleX size={16} aria-hidden="true" />;
  return <LuTriangleAlert size={16} aria-hidden="true" />;
}

export function NotificationCenter({ providerId }: NotificationCenterProps) {
  const [rows, setRows] = useState<CatalogNotification[] | null>(null);
  const [summary, setSummary] = useState<CatalogNotificationsResponse['summary'] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [open, setOpen] = useState(false);
  const [markingRead, setMarkingRead] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const latestRequest = useRef(0);

  const refreshNotifications = useCallback(async (signal?: AbortSignal) => {
    const requestNumber = ++latestRequest.current;
    try {
      const result = await fetchCatalogNotifications(providerId, signal);
      if (signal?.aborted || requestNumber !== latestRequest.current) return;
      setRows(result.notifications);
      setSummary(result.summary);
      setError(null);
    } catch (reason) {
      if (signal?.aborted || requestNumber !== latestRequest.current) return;
      setError(reason instanceof Error ? reason.message : String(reason));
    }
  }, [providerId]);

  useEffect(() => {
    const controller = new AbortController();
    const refreshIfVisible = () => {
      if (document.visibilityState === 'visible') void refreshNotifications(controller.signal);
    };
    void refreshNotifications(controller.signal);
    const interval = window.setInterval(refreshIfVisible, NOTIFICATION_REFRESH_INTERVAL_MS);
    document.addEventListener('visibilitychange', refreshIfVisible);
    window.addEventListener('focus', refreshIfVisible);
    return () => {
      controller.abort();
      window.clearInterval(interval);
      document.removeEventListener('visibilitychange', refreshIfVisible);
      window.removeEventListener('focus', refreshIfVisible);
    };
  }, [refreshNotifications]);

  useEffect(() => {
    if (!open) return;
    const closeOnOutsidePress = (event: PointerEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) setOpen(false);
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

  const notifications = useMemo(
    () => (rows ?? []).slice().sort((a, b) => new Date(b.observedAt).getTime() - new Date(a.observedAt).getTime()),
    [rows],
  );
  const unread = notifications.filter((notification) => notification.readAt === null);
  const unreadCount = summary?.unread ?? unread.length;
  const total = summary?.total ?? notifications.length;
  const withheld = total - notifications.length;
  const triggerLabel = unreadCount > 0 ? `Notifications, ${unreadCount} unread` : 'Notifications';

  const markRead = async (notificationsToMark: CatalogNotification[], markAllInScope = false) => {
    const ids = notificationsToMark.filter((notification) => notification.readAt === null).map((notification) => notification.id);
    if ((!markAllInScope && ids.length === 0) || (markAllInScope && unreadCount === 0)) return;
    setActionError(null);
    setMarkingRead(true);
    try {
      await markCatalogNotificationsRead(markAllInScope ? null : ids, providerId);
      const readAt = new Date().toISOString();
      setRows((current) => current?.map((notification) => (markAllInScope || ids.includes(notification.id))
        ? { ...notification, readAt }
        : notification) ?? null);
      setSummary((current) => current
        ? { total: current.total, unread: markAllInScope ? 0 : Math.max(0, current.unread - ids.length), read: markAllInScope ? current.total : Math.min(current.total, current.read + ids.length) }
        : current);
    } catch {
      setActionError('Notifications could not be marked as read. Please try again.');
    } finally {
      setMarkingRead(false);
    }
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
        {unreadCount > 0 && <span className={styles.badge} aria-hidden="true">{unreadCount > 9 ? '9+' : unreadCount}</span>}
      </button>

      {open && (
        <section id="catalog-notifications" className={styles.popover} role="dialog" aria-label="Catalog notifications">
          <header className={styles.popoverHeader}>
            <div>
              <p className={styles.eyebrow}>Catalog updates</p>
              <h2 className={styles.title}>Notifications</h2>
            </div>
            {unreadCount > 0 && (
              <button type="button" className={styles.acknowledgeAll} onClick={() => void markRead(unread, true)} disabled={markingRead}>
                <LuCheckCheck size={14} aria-hidden="true" />
                Mark all as read
              </button>
            )}
          </header>

          <div className={styles.content} aria-live="polite">
            {rows === null && !error && <div className={styles.state}><LuLoaderCircle size={16} className={styles.spinner} aria-hidden="true" />Loading notifications…</div>}
            {error && rows === null && <div className={`${styles.state} ${styles.errorState}`}><LuCircleAlert size={16} aria-hidden="true" />Notifications are unavailable right now.</div>}
            {rows !== null && !error && notifications.length === 0 && <div className={styles.state}>No catalog notifications yet.</div>}
            {notifications.length > 0 && (
              <ul className={styles.list} aria-label="Catalog notification history">
                {notifications.map((notification) => {
                  const target = notification.providerId ? `/provider/${encodeURIComponent(notification.providerId)}` : '/';
                  const targetLabel = notification.modelId ?? notification.providerId ?? 'Catalog';
                  return (
                    <li key={notification.id} className={`${styles.item} ${notification.readAt !== null ? styles.itemRead : ''}`}>
                      <span className={styles.itemTone} data-category={notification.category} aria-hidden="true"><NotificationIcon category={notification.category} /></span>
                      <span className={styles.itemBody}>
                        <span className={styles.itemTopline}>
                          <span className={styles.itemLabel}>{notification.title}</span>
                          <time className={styles.time} dateTime={notification.observedAt}>{formatAgo(notification.observedAt)}</time>
                        </span>
                        <span className={styles.itemTarget}>{targetLabel}</span>
                        <span className={styles.itemDetail}>{notification.detail}</span>
                      </span>
                      <Link
                        to={target}
                        className={styles.targetLink}
                        onClick={() => { void markRead([notification]); setOpen(false); }}
                        aria-label={`Open details for ${targetLabel}`}
                        title="Open notification target"
                      >
                        <LuChevronRight size={15} aria-hidden="true" />
                      </Link>
                    </li>
                  );
                })}
              </ul>
            )}
            {withheld > 0 && (
              // Not an action — a statement. Above the service's page ceiling the
              // oldest notifications cannot be shown, and a panel that stays
              // silent about that is the 5-of-135 bug wearing a bigger number.
              <p className={styles.pageNote} role="status">
                Showing the {notifications.length} most recent of {total}.
              </p>
            )}
            {actionError && <p className={styles.actionError} role="alert">{actionError}</p>}
            {error && rows !== null && <p className={styles.refreshError} role="status">Unable to refresh notifications. Showing the latest loaded history.</p>}
          </div>
        </section>
      )}
    </div>
  );
}

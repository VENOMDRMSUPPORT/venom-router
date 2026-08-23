import { useEffect, useState, useMemo } from 'react';
import { fetchChanges, formatAgo, type Change } from '../../api/client';
import { Toolbar } from '../../components/Toolbar/Toolbar';
import styles from './ChangesPage.module.css';

/**
 * "What's new", built from the events the sync recorded inside the same
 * transaction that applied each change.
 */
const LABEL: Record<string, { text: string; tone: 'add' | 'remove' | 'change' | 'score' }> = {
  added: { text: 'Added', tone: 'add' },
  readded: { text: 'Back', tone: 'add' },
  retired: { text: 'Retired', tone: 'remove' },
  became_missing: { text: 'Missing', tone: 'remove' },
  excluded: { text: 'Excluded', tone: 'remove' },
  price_changed: { text: 'Price', tone: 'change' },
  context_changed: { text: 'Context', tone: 'change' },
  capability_changed: { text: 'Capability', tone: 'change' },
  quality_became_available: { text: 'Now scored', tone: 'score' },
  quality_evidence_upgraded: { text: 'Better evidence', tone: 'score' },
  quality_changed: { text: 'Score moved', tone: 'score' },
  quality_lost: { text: 'Score withdrawn', tone: 'score' },
};

/**
 * The filters this page actually has.
 *
 * Derived from the same map that labels the events, so a class can never be
 * offered as a filter without a label or labelled without being filterable. It
 * used to share the model filters — "Free Models", "1M+ Context" — which
 * compared a change class against a model predicate and emptied the page.
 */
const CHANGE_FILTERS = [
  { value: 'all', label: 'All Events' },
  ...Object.entries(LABEL).map(([value, meta]) => ({ value, label: meta.text })),
];

export function ChangesPage() {
  const [changes, setChanges] = useState<Change[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState<string>('');
  const [filter, setFilter] = useState<string>('all');
  const [view, setView] = useState<'grid' | 'table'>('grid');

  useEffect(() => {
    fetchChanges()
      .then((r) => {
        setChanges(r.changes);
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)));
  }, []);

  const filteredChanges = useMemo(() => {
    if (!changes) return [];
    let list = changes;
    if (query.trim()) {
      const q = query.toLowerCase();
      list = list.filter(
        (c) =>
          c.modelId.toLowerCase().includes(q) ||
          c.providerId.toLowerCase().includes(q)
      );
    }
    if (filter !== 'all') {
      list = list.filter((c) => c.class === filter);
    }
    return list;
  }, [changes, query, filter]);

  if (error) return <div className={styles.state}>Change history unavailable: {error}</div>;
  if (!changes) return <div className={styles.state}>Loading…</div>;

  const days = new Map<string, Change[]>();
  for (const c of filteredChanges) {
    const day = c.observedAt.slice(0, 10);
    days.set(day, [...(days.get(day) ?? []), c]);
  }

  return (
    <div>
      <header className={styles.header}>
        <h1 className={styles.title}>What's new</h1>
        <p className={styles.subtitle}>
          Every change the catalog observed, recorded at the moment it was
          applied. A sync that finds nothing different adds nothing here.
        </p>
      </header>

      {/* Unified Toolbar */}
      <Toolbar
        query={query}
        onQueryChange={setQuery}
        filter={filter}
        onFilterChange={setFilter}
        options={CHANGE_FILTERS}
        view={view}
        onViewChange={setView}
      />

      {filteredChanges.length === 0 && (
        <div className={styles.state}>
          No changes match your search or filter.
        </div>
      )}

      {view === 'grid' ? (
        [...days.entries()].map(([day, items]) => (
          <section key={day} className={styles.day}>
            <h2 className={styles.dayTitle}>
              {day} <span className={styles.dayAgo}>· {formatAgo(items[0].observedAt)}</span>
            </h2>
            <ul className={styles.list}>
              {items.map((c, i) => {
                const meta = LABEL[c.class] ?? { text: c.class, tone: 'change' as const };
                return (
                  <li key={`${c.providerId}/${c.modelId}/${c.class}/${i}`} className={styles.item}>
                    <span className={`${styles.tag} ${styles[meta.tone]}`}>{meta.text}</span>
                    <div className={styles.body}>
                      <span className={styles.model}>{c.modelId}</span>
                      <span className={styles.provider}>{c.providerId}</span>
                      {c.from !== null && c.to !== null && (
                        <span className={styles.delta}>
                          <span className={styles.old}>{c.from}</span> → <span className={styles.new}>{c.to}</span>
                          {c.field && <span className={styles.field}>{c.field}</span>}
                        </span>
                      )}
                      {c.note && !c.from && <span className={styles.note}>{c.note}</span>}
                    </div>
                    <time className={styles.time} dateTime={c.observedAt}>{c.observedAt.slice(11, 16)}</time>
                  </li>
                );
              })}
            </ul>
          </section>
        ))
      ) : (
        <div className={styles.tableScroll}>
          <table className={styles.table}>
            <thead>
              <tr>
                <th>Event</th>
                <th>Provider</th>
                <th>Model</th>
                <th>Change Details</th>
                <th>Time</th>
              </tr>
            </thead>
            <tbody>
              {filteredChanges.map((c, i) => {
                const meta = LABEL[c.class] ?? { text: c.class, tone: 'change' as const };
                return (
                  <tr key={`${c.providerId}/${c.modelId}/${c.class}/${i}`}>
                    <td>
                      <span className={`${styles.tag} ${styles[meta.tone]}`}>{meta.text}</span>
                    </td>
                    <td><span className={styles.provider}>{c.providerId}</span></td>
                    <td><span className={styles.model}>{c.modelId}</span></td>
                    <td>
                      {c.from !== null && c.to !== null && (
                        <span className={styles.delta}>
                          <span className={styles.old}>{c.from}</span> → <span className={styles.new}>{c.to}</span>
                          {c.field && <span className={styles.field}>{c.field}</span>}
                        </span>
                      )}
                      {c.note && !c.from && <span className={styles.note}>{c.note}</span>}
                    </td>
                    <td className={styles.timeTd}>{c.observedAt.slice(0, 16).replace('T', ' ')}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

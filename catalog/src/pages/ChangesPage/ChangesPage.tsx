import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { LuArrowLeft } from 'react-icons/lu';
import { fetchChanges, formatAgo, type Change } from '../../api/client';
import styles from './ChangesPage.module.css';

/**
 * "What's new", built from the events the sync recorded inside the same
 * transaction that applied each change.
 *
 * Nothing here is inferred by diffing two API responses in the browser: a change
 * the service did not record is a change that did not happen, and one it did
 * record cannot be missed.
 */
const LABEL: Record<string, { text: string; tone: 'add' | 'remove' | 'change' | 'score' }> = {
  added: { text: 'Added', tone: 'add' },
  readded: { text: 'Back', tone: 'add' },
  retired: { text: 'Retired', tone: 'remove' },
  became_missing: { text: 'Missing', tone: 'remove' },
  price_changed: { text: 'Price', tone: 'change' },
  context_changed: { text: 'Context', tone: 'change' },
  capability_changed: { text: 'Capability', tone: 'change' },
  quality_became_available: { text: 'Now scored', tone: 'score' },
  quality_evidence_upgraded: { text: 'Better evidence', tone: 'score' },
  quality_changed: { text: 'Score moved', tone: 'score' },
  quality_lost: { text: 'Score withdrawn', tone: 'score' },
};

export function ChangesPage() {
  const [changes, setChanges] = useState<Change[] | null>(null);
  const [byClass, setByClass] = useState<Record<string, number>>({});
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState<string>('all');

  useEffect(() => {
    fetchChanges()
      .then((r) => { setChanges(r.changes); setByClass(r.byClass); })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)));
  }, []);

  if (error) return <div className={styles.state}>Change history unavailable: {error}</div>;
  if (!changes) return <div className={styles.state}>Loading…</div>;

  const shown = filter === 'all' ? changes : changes.filter((c) => c.class === filter);
  const days = new Map<string, Change[]>();
  for (const c of shown) {
    const day = c.observedAt.slice(0, 10);
    days.set(day, [...(days.get(day) ?? []), c]);
  }

  return (
    <div>
      <Link to="/" className={styles.back}><LuArrowLeft size={14} /><span>All providers</span></Link>

      <header className={styles.header}>
        <h1 className={styles.title}>What's new</h1>
        <p className={styles.subtitle}>
          Every change the catalog observed, recorded at the moment it was
          applied. A sync that finds nothing different adds nothing here.
        </p>
      </header>

      <div className={styles.filters}>
        <button className={`${styles.chip} ${filter === 'all' ? styles.on : ''}`} onClick={() => setFilter('all')}>
          All {changes.length}
        </button>
        {Object.entries(byClass).sort((a, b) => b[1] - a[1]).map(([cls, n]) => (
          <button key={cls} className={`${styles.chip} ${filter === cls ? styles.on : ''}`} onClick={() => setFilter(cls)}>
            {LABEL[cls]?.text ?? cls} {n}
          </button>
        ))}
      </div>

      {shown.length === 0 && <div className={styles.state}>No changes recorded yet. The first sync writes the initial inventory.</div>}

      {[...days.entries()].map(([day, items]) => (
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
      ))}
    </div>
  );
}

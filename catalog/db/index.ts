/**
 * Database access. Uses node:sqlite (built into Node 22+), so the service has
 * no native dependency and no build step.
 */
import { DatabaseSync } from 'node:sqlite';
import { readFileSync, mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));

export type Db = DatabaseSync;

export function openDb(path = join(HERE, '..', 'data', 'catalog.db')): Db {
  if (path !== ':memory:') mkdirSync(dirname(path), { recursive: true });
  const db = new DatabaseSync(path);
  db.exec(readFileSync(join(HERE, 'schema.sql'), 'utf8'));
  migrate(db);
  return db;
}

/**
 * Bring an existing database up to the current schema.
 *
 * `CREATE TABLE IF NOT EXISTS` silently does nothing when the table already
 * exists, so a column added to schema.sql never reaches a database created
 * before it. Adding them here keeps a live database and a fresh one identical —
 * without this, the difference only shows up as a confusing NULL much later.
 */
function migrate(db: Db): void {
  const ADDED: Record<string, string[]> = {
    model_scores: [
      'raw_value REAL',
      'raw_field TEXT',
      'transformation TEXT',
      'source_fetched_at TEXT',
      'unrated_reason TEXT',
    ],
    models: [
      'ref_cost_in_per_m REAL',
      'ref_cost_out_per_m REAL',
      'cost_kind TEXT',
      'exclusion_reason TEXT',
    ],
    evaluation_samples: [
      'response_json TEXT',
    ],
    model_facts: [
      'source_url TEXT',
      'evidence_state TEXT',
      'raw_value TEXT',
      'resolver_version TEXT',
      'probe_version TEXT',
    ],
  };
  for (const [table, columns] of Object.entries(ADDED)) {
    const existing = new Set(
      (db.prepare(`PRAGMA table_info(${table})`).all() as unknown as { name: string }[]).map((c) => c.name),
    );
    for (const def of columns) {
      const name = def.split(' ')[0];
      if (!existing.has(name)) db.exec(`ALTER TABLE ${table} ADD COLUMN ${def}`);
    }
  }
  retireSupersededFacts(db);
}

/**
 * Drop fact rows whose field no longer exists.
 *
 * `cost` was one blob holding the billing kind, the effective price and the
 * reference price under a single source label. It is now three facts with three
 * sources, and the old row is not history worth keeping — it is a field with no
 * provenance at all, which is exactly what the split exists to eliminate. Every
 * value it held is fully represented by its replacements.
 *
 * This deletes a *superseded projection*, not a fact about the world: unlike
 * `models` and `model_events`, `model_facts` is current-resolution state that
 * the enrichment pass already overwrites on every run.
 */
const SUPERSEDED_FACT_FIELDS = ['cost'];

function retireSupersededFacts(db: Db): void {
  const placeholders = SUPERSEDED_FACT_FIELDS.map(() => '?').join(',');
  db.prepare(`DELETE FROM model_facts WHERE field IN (${placeholders})`).run(...SUPERSEDED_FACT_FIELDS);
}

/**
 * Run `fn` inside one transaction. Either every write lands or none does —
 * a half-applied sync must be impossible, not merely unlikely.
 */
export function transaction<T>(db: Db, fn: () => T): T {
  db.exec('BEGIN');
  try {
    const out = fn();
    db.exec('COMMIT');
    return out;
  } catch (err) {
    db.exec('ROLLBACK');
    throw err;
  }
}

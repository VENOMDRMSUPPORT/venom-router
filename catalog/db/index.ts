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
  return db;
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

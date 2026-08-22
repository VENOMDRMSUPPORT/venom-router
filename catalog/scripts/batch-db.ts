import { openDb, type Db } from '../db/index.ts';
import { assertServiceNotListening } from './service-guard.ts';

/**
 * The only entrypoint for terminal batches that open the catalog database.
 *
 * The service owns the database while it is listening. Keep the guard here so
 * new batch scripts cannot accidentally open a second writer by omitting it.
 * The guard probes the default service port; it is a safety heuristic, not a
 * proof that every non-default service instance is absent.
 */
export async function openBatchDb(dbPath?: string): Promise<Db> {
  await assertServiceNotListening();
  return openDb(dbPath);
}

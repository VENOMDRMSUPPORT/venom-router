/**
 * A verification instance of the catalog service.
 *
 * Runs against `data/verify.db` — a COPY — on its own port, so a change can be
 * inspected in a browser while the live service keeps the real database open.
 * Two writers on one SQLite file is the one way to genuinely corrupt it.
 *
 *   npm run serve:verify        ->  http://127.0.0.1:8792
 *
 * It opens `data/verify.db` and NOTHING else: the path below is hardcoded, so
 * this can never be pointed at the live database by an environment variable or
 * a stray argument. That is the whole point of the file.
 *
 * Refresh the copy yourself, with the live service STOPPED — a copy taken while
 * a writer holds the file can miss everything still sitting in the WAL, so take
 * all three parts together:
 *
 *   cp data/catalog.db data/verify.db
 *   cp data/catalog.db-wal data/verify.db-wal
 *   cp data/catalog.db-shm data/verify.db-shm
 *
 * Opening it migrates the copy to the current schema, which is usually the
 * reason to run this at all. If the port is taken, an instance is already up:
 * node exits with EADDRINUSE rather than sharing it.
 */
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const HERE = dirname(fileURLToPath(import.meta.url));
const PORT = 8792;

const { createApp, BIND_HOST } = await import('../server/index.ts');
const app = createApp(PORT, join(HERE, '..', 'data', 'verify.db'));
app.server.listen(PORT, BIND_HOST, () => {
  console.log(`catalog verification instance on http://${BIND_HOST}:${PORT}`);
});

/**
 * The UI half of the verification instance.
 *
 * `serve:verify` has run a service against `data/verify.db` on port 8792 since
 * it was written, and `vite.config.ts` has carried a `CATALOG_API` override
 * "so a verification instance on another port can be inspected without stopping
 * the live one" — but nothing paired the two. Inspecting a change in a browser
 * meant remembering to export the variable by hand, and forgetting meant a
 * verification UI quietly reading the LIVE service instead. That is the worst
 * possible failure for this file: a change that looks verified and was not.
 *
 *   npm run serve:verify     ->  API  http://127.0.0.1:8792   (data/verify.db)
 *   npm run dev:verify       ->  UI   http://127.0.0.1:5174   (proxies to 8792)
 *
 * Both ports are hardcoded here for the same reason `serve-verify.mjs`
 * hardcodes its database path: there must be no argument or variable that can
 * point this at the live instance.
 */
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { createServer } from 'vite';

// `new URL(...).pathname` yields "/C:/..." on Windows, which vite then resolves
// against the cwd into "C:\C:\...". Same reason `serve-verify.mjs` uses
// fileURLToPath rather than a URL pathname.
const HERE = dirname(fileURLToPath(import.meta.url));
const ROOT = join(HERE, '..');

const API_PORT = 8792;
const UI_PORT = 5174;

// Set before vite's config is loaded, because the proxy target is read at
// config evaluation time and not again afterwards.
process.env.CATALOG_API = `http://127.0.0.1:${API_PORT}`;

const server = await createServer({
  configFile: join(ROOT, 'vite.config.ts'),
  root: ROOT,
  server: { port: UI_PORT, strictPort: true },
});

await server.listen();
console.log(`catalog verification UI on http://127.0.0.1:${UI_PORT} -> API ${process.env.CATALOG_API}`);
console.log('  run `npm run serve:verify` in another terminal if the API is not up yet');

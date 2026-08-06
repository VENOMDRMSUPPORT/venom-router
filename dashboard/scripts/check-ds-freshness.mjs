#!/usr/bin/env node
// Guards the ONE real staleness trap: @venom/design-system is a
// "file:../Design_System" dependency whose package.json `exports` map points
// every subpath at Design_System/dist/*.mjs — there is no "source" export
// condition. `dist/` is gitignored and nothing rebuilds it automatically.
//
// This is NOT "CI could ship stale design-system code" — it cannot: CI always
// runs `task dashboard:build-embed` (which builds Design_System, THEN the
// dashboard) before any test/build step touches dist/, and a genuinely fresh
// checkout has no dist/ at all, so a direct build fails module resolution
// outright — a self-announcing hard failure, not a silent stale pass.
//
// The one dangerous case: a developer who already built once (dist/ exists),
// then edits Design_System source, then runs `npm --prefix dashboard test` or
// `npm --prefix dashboard run build` directly instead of through
// `task dashboard:build-embed`. They get the OLD compiled behaviour with no
// warning at all.
//
// This script only CHECKS — it never rebuilds. An unconditional rebuild
// (rejected: see task-3-report.md's fix-round-1 addendum) would make CI build
// the design system twice per job (once via `task dashboard:build-embed`,
// once again via this hook nested inside the dashboard's own `npm run
// build`/`test`) for zero benefit, since CI's dist/ is already fresh at that
// point. A stat-based mtime comparison costs milliseconds whether run once or
// three times in one job, and failing loudly beats silently "fixing" staleness
// the developer never noticed they had.
//
// SCOPE (whole-branch review, 2026-08-06): an earlier version of this check
// scanned all of Design_System/ minus a small deny-list of build OUTPUT
// directories (dist, dist-explorer, node_modules, ...), treating EVERYTHING
// else as source dist/ depends on. It doesn't. `dist/` is built by
// `vite.config.ts`'s lib build, whose only entry points are `src/*.ts` — and
// those, transitively, only ever import from `src/` and `components/`
// (`grep -rl '\.css[\'"]' src/ components/` finds nothing: no component
// imports a stylesheet). Everything else under Design_System/ — css/,
// themes/, tokens/*.css & *.json, icons/, assets/, tests/, storybook/,
// README.md, ... — is either consumed VERBATIM at runtime (styles.css's
// @import lines resolve straight to css/, themes/, tokens/, icons/ — never
// through dist/) or is tooling/docs the build never reads at all. Scanning
// those trees for staleness produced false failures for real, ordinary work:
// this branch edited Design_System/css/components-domain.css and the change
// was live in the dashboard with no rebuild needed, yet the old check called
// it stale; the DS visual-snapshot suite writes new files under tests/ and
// tripped the same false failure. So the scanned set is narrowed to an
// ALLOW-list of the two directories dist/ is actually compiled from, filtered
// to the TypeScript/TSX files tsc/vite actually read (components/ also holds
// `*.card.html` demo pages and `*.prompt.md` design docs that are source for
// nothing dist/ contains).
import { existsSync, readdirSync, statSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const DASHBOARD_ROOT = path.resolve(__dirname, "..");
const REPO_ROOT = path.resolve(DASHBOARD_ROOT, "..");
const DS_ROOT = path.join(REPO_ROOT, "Design_System");
const DIST_DIR = path.join(DS_ROOT, "dist");

// The only two directories dist/ is compiled from (see vite.config.ts's lib
// entries under src/, which import their component trees from components/).
const SOURCE_DIRS = ["src", "components"];
// The two extensions tsc/vite actually compile. Everything else under
// SOURCE_DIRS (*.card.html demo pages, *.prompt.md design docs) is never
// read by the build and must not be able to trip this check.
const SOURCE_EXTENSIONS = new Set([".ts", ".tsx"]);

/** Newest mtime under `dir`, recursively. `extensions`, when given, restricts
 * which FILES count (directories are always walked); omit it to consider
 * every file (used for dist/, whose output extensions are its own concern). */
function newestMtimeMs(dir, extensions) {
  let newest = 0;
  if (!existsSync(dir)) return newest;
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === "node_modules") continue;
      newest = Math.max(newest, newestMtimeMs(full, extensions));
    } else if (entry.isFile()) {
      if (extensions && !extensions.has(path.extname(entry.name))) continue;
      newest = Math.max(newest, statSync(full).mtimeMs);
    }
  }
  return newest;
}

function fail(message) {
  console.error(
    "\ncheck-ds-freshness: " +
      message +
      "\n\nRun `npm --prefix Design_System run build` (or `task dashboard:build-embed`" +
      " from the repo root) and retry.\n",
  );
  process.exit(1);
}

if (!existsSync(DIST_DIR)) {
  fail("Design_System/dist is missing — nothing has built the design system in this checkout yet.");
}

const sourceNewest = Math.max(
  ...SOURCE_DIRS.map((dir) => newestMtimeMs(path.join(DS_ROOT, dir), SOURCE_EXTENSIONS)),
);
const distNewest = newestMtimeMs(DIST_DIR);

if (sourceNewest > distNewest) {
  fail(
    "Design_System/dist is STALE — a Design_System source file changed after the last build " +
      "(dist/'s newest output predates it). The dashboard would silently consume the OLD compiled behaviour.",
  );
}

console.log("check-ds-freshness: OK — Design_System/dist is up to date with its source.");

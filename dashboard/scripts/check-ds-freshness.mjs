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

import { existsSync, readdirSync, statSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const DASHBOARD_ROOT = path.resolve(__dirname, "..");
const REPO_ROOT = path.resolve(DASHBOARD_ROOT, "..");
const DS_ROOT = path.join(REPO_ROOT, "Design_System");
const DIST_DIR = path.join(DS_ROOT, "dist");

// Directories under Design_System/ that are build OUTPUT or noise, never
// SOURCE — excluded so editing a build artifact can never itself trip the
// "source is newer than dist" check.
const SKIP_DIRS = new Set(["dist", "dist-explorer", "node_modules", "test-results", "playwright-report", ".git"]);

function newestMtimeMs(dir) {
  let newest = 0;
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.isDirectory()) {
      if (SKIP_DIRS.has(entry.name)) continue;
      newest = Math.max(newest, newestMtimeMs(path.join(dir, entry.name)));
    } else if (entry.isFile()) {
      newest = Math.max(newest, statSync(path.join(dir, entry.name)).mtimeMs);
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

const sourceNewest = newestMtimeMs(DS_ROOT);
const distNewest = newestMtimeMs(DIST_DIR);

if (sourceNewest > distNewest) {
  fail(
    "Design_System/dist is STALE — a Design_System source file changed after the last build " +
      "(dist/'s newest output predates it). The dashboard would silently consume the OLD compiled behaviour.",
  );
}

console.log("check-ds-freshness: OK — Design_System/dist is up to date with its source.");

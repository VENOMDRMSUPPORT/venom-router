// Venom Design System — handoff-manifest gate.
// Enforces validation/handoff-manifest.json against the real repository state:
//   1. every top-level entry is classified in the manifest (no unclassified newcomers);
//   2. every canonical source path exists;
//   3. every generated-required artifact exists (incl. a sibling .d.ts for every
//      components/ .tsx/.ts module);
//   4. no forbidden stale legacy duplicate has reappeared (src/*.d.ts, *.jsx,
//      *.entry.d.ts, the old browser-Babel card runtime, leftover export-check probe);
//   5. every excluded transient pattern is covered by .gitignore;
//   6. src/ stays entry-point-only (no .tsx, no direct react import — component
//      implementation logic lives only in components/);
//   7. dist/ and dist-explorer/ are classified generated+excluded, never authored.
// Unlike the token/contrast gates this is inherently a filesystem-structure check,
// so it uses node:fs directly; the injected `io` is used for logging only.
const fs = require("node:fs");
const path = require("node:path");

const ROOT = path.resolve(__dirname, "..");

async function checkVenomHandoff(io) {
  const errors = [];
  const manifest = JSON.parse(fs.readFileSync(path.join(ROOT, "validation", "handoff-manifest.json"), "utf8"));

  const exists = (rel) => fs.existsSync(path.join(ROOT, rel.replace(/\/$/, "")));
  const isDir = (rel) => exists(rel) && fs.statSync(path.join(ROOT, rel.replace(/\/$/, ""))).isDirectory();

  // ---- 1. top-level classification completeness
  const classified = new Set();
  for (const list of [manifest.authored, manifest.generated_required, manifest.generated_reproducible, manifest.test_baselines, manifest.excluded]) {
    for (const entry of list) classified.add(entry.split("/")[0].split("**")[0]);
  }
  const topLevel = fs.readdirSync(ROOT);
  const unclassified = topLevel.filter((f) => {
    if (classified.has(f)) return false;
    // pattern-classified entries (e.g. "*.log")
    return !manifest.excluded.some((p) => p.startsWith("*.") && f.endsWith(p.slice(1)));
  });
  if (unclassified.length) errors.push("Unclassified top-level entr(ies): " + unclassified.join(", ") + " — classify them in validation/handoff-manifest.json.");

  // ---- 2. canonical sources exist
  for (const [concern, paths] of Object.entries(manifest.canonical_sources)) {
    for (const p of paths) {
      if (!exists(p)) errors.push(`Canonical source missing for '${concern}': ${p}`);
    }
  }

  // ---- 3. generated-required artifacts exist
  for (const p of manifest.generated_required) {
    if (p === "components/**/*.d.ts") continue; // handled structurally below
    if (p.endsWith("/")) {
      if (!isDir(p) || fs.readdirSync(path.join(ROOT, p)).length === 0) errors.push("Generated-required directory missing or empty: " + p);
    } else if (!exists(p)) {
      errors.push("Generated-required artifact missing: " + p);
    }
  }
  // every components/ module (.tsx or .ts, excluding entries/declarations) has a sibling .d.ts
  const walk = (dir, out = []) => {
    for (const f of fs.readdirSync(dir)) {
      const full = path.join(dir, f);
      if (fs.statSync(full).isDirectory()) walk(full, out);
      else out.push(full);
    }
    return out;
  };
  const componentFiles = walk(path.join(ROOT, "components"));
  let declChecked = 0;
  for (const f of componentFiles) {
    if (!/\.(tsx|ts)$/.test(f) || f.endsWith(".d.ts") || f.endsWith(".entry.tsx")) continue;
    declChecked++;
    const sibling = f.replace(/\.(tsx|ts)$/, ".d.ts");
    if (!fs.existsSync(sibling)) errors.push("Missing generated sibling declaration: " + path.relative(ROOT, sibling) + " (run npm run build:declarations)");
  }

  // ---- 4. forbidden stale duplicates
  const srcFiles = fs.existsSync(path.join(ROOT, "src")) ? fs.readdirSync(path.join(ROOT, "src")) : [];
  for (const f of srcFiles) if (f.endsWith(".d.ts")) errors.push("Stale declaration duplicate reappeared: src/" + f + " (declarations ship only from dist/types — delete it).");
  const sourceDirs = ["components", "src", "ui_kits", "states", "storybook", "foundations", "scripts", "tests"].filter((d) => fs.existsSync(path.join(ROOT, d)));
  for (const d of sourceDirs) {
    for (const f of walk(path.join(ROOT, d))) {
      const rel = path.relative(ROOT, f).replace(/\\/g, "/");
      if (rel.endsWith(".jsx")) errors.push("Stale .jsx source reappeared: " + rel);
      if (rel.endsWith(".entry.d.ts")) errors.push("Stale *.entry.d.ts artifact reappeared: " + rel);
    }
  }
  if (fs.existsSync(path.join(ROOT, "storybook", "card-runtime.js"))) errors.push("Legacy browser-Babel card runtime reappeared: storybook/card-runtime.js");
  if (fs.existsSync(path.join(ROOT, "dist", ".export-check.mjs"))) errors.push("Leftover temp probe: dist/.export-check.mjs (scripts/validate.js must remove it after the export gate).");

  // ---- 5. exclusion rules cover every transient pattern
  const gitignore = fs.readFileSync(path.join(ROOT, ".gitignore"), "utf8").split(/\r?\n/).map((l) => l.trim());
  for (const p of manifest.excluded) {
    if (!gitignore.includes(p)) errors.push(".gitignore does not exclude '" + p + "' — transient/reproducible artifacts must never enter the version-control or handoff set.");
  }

  // ---- 6. src/ stays entry-point-only
  for (const f of srcFiles) {
    if (f.endsWith(".tsx")) errors.push("src/ gained a .tsx file (" + f + ") — component implementation logic belongs in components/.");
    if (f.endsWith(".ts")) {
      const body = fs.readFileSync(path.join(ROOT, "src", f), "utf8");
      if (/from\s+["']react["']|require\(["']react["']\)/.test(body)) errors.push("src/" + f + " imports react directly — component implementation logic belongs in components/.");
    }
  }

  // ---- 7. build outputs are never authored
  for (const d of ["dist/", "dist-explorer/"]) {
    if (!manifest.generated_reproducible.includes(d)) errors.push(d + " must be classified generated_reproducible.");
    if (!manifest.excluded.includes(d)) errors.push(d + " must be in the excluded (handoff/VCS) set.");
    if (manifest.authored.some((a) => a === d || a.startsWith(d))) errors.push(d + " must never appear in the authored set.");
  }

  if (errors.length) throw new Error("HANDOFF GATE FAILED:\n  - " + errors.join("\n  - "));
  io.log(`HANDOFF: ${topLevel.length} top-level entries classified, ${Object.keys(manifest.canonical_sources).length} canonical concerns present, ${declChecked} component modules with generated declarations, 0 stale duplicates, exclusions covered.`);
  return {
    topLevelEntries: topLevel.length,
    canonicalConcerns: Object.keys(manifest.canonical_sources).length,
    componentModulesWithDeclarations: declChecked,
    errors: [],
  };
}

if (typeof module !== "undefined") module.exports = { checkVenomHandoff };

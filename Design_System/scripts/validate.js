#!/usr/bin/env node
// Venom Design System — the one Node-based validation harness. Runs every local
// mechanical gate against the real filesystem (no undocumented `io` object to supply
// yourself), prints a readable summary, writes validation/report.json (machine-readable)
// and regenerates validation/report.md from the actual run. Exit code is 0 only when every
// mandatory gate passes.
//
//   node scripts/validate.js
//   npm run validate
import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(__dirname, "..");
const pkg = JSON.parse(fs.readFileSync(path.join(ROOT, "package.json"), "utf8"));

function nowIso() {
  return new Date().toISOString();
}

const fsIo = {
  readFile: async (p) => fs.readFileSync(path.join(ROOT, p), "utf8"),
  saveFile: async (p, content) => {
    fs.mkdirSync(path.dirname(path.join(ROOT, p)), { recursive: true });
    fs.writeFileSync(path.join(ROOT, p), content);
  },
  ls: async (p) => {
    try {
      return fs.readdirSync(path.join(ROOT, p));
    } catch (e) {
      return [];
    }
  },
  log: () => {}, // captured per-gate below instead of writing straight to stdout
};

const gates = [];

function runIoGate(name, command, fn) {
  const logs = [];
  const io = { ...fsIo, log: (...a) => logs.push(a.map(String).join(" ")) };
  const startedAt = nowIso();
  return fn(io)
    .then((summary) => {
      gates.push({ name, command, status: "PASS", startedAt, finishedAt: nowIso(), logs, summary });
    })
    .catch((err) => {
      gates.push({
        name,
        command,
        status: "FAIL",
        startedAt,
        finishedAt: nowIso(),
        logs,
        error: String(err && err.message ? err.message : err),
      });
    });
}

function runProcessGate(name, command, args, opts = {}) {
  const startedAt = nowIso();
  try {
    // `npx`/`npm` resolve to .cmd shims on Windows, which only cmd.exe can execute
    // directly — shell:true is required there. Every arg here is a fixed, developer-
    // authored string (no user input), so the lack of shell-escaping is not a concern.
    const output = execFileSync(command, args, {
      cwd: ROOT,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
      shell: process.platform === "win32",
      ...opts,
    });
    gates.push({ name, command: [command, ...args].join(" "), status: "PASS", startedAt, finishedAt: nowIso(), output: output.trim() });
  } catch (err) {
    const output = [err.stdout, err.stderr].filter(Boolean).join("\n").trim();
    gates.push({ name, command: [command, ...args].join(" "), status: "FAIL", startedAt, finishedAt: nowIso(), output, error: err.message });
  }
}

async function main() {
  const runStartedAt = nowIso();

  // ---- 1-3: token build, contrast, guardrails (real fs io — no more "undocumented io object")
  const { buildVenomTokens } = require(path.join(ROOT, "validation", "build-tokens.cjs"));
  const { checkVenomContrast } = require(path.join(ROOT, "validation", "check-contrast.cjs"));
  const { checkVenomGuardrails } = require(path.join(ROOT, "validation", "check-guardrails.cjs"));
  const { checkVenomHandoff } = require(path.join(ROOT, "validation", "check-handoff.cjs"));

  await runIoGate("Token build (schema, completeness, reference legality)", "buildVenomTokens(io)", buildVenomTokens);
  await runIoGate("Contrast (WCAG AA; AAA core text in venom-hc)", "checkVenomContrast(io)", checkVenomContrast);
  await runIoGate(
    "Guardrails (raw-color, terminology, secrets, icon map, state coverage, story coverage, CDN scan, required components)",
    "checkVenomGuardrails(io)",
    checkVenomGuardrails
  );

  // ---- 4: type check (strict, over src/ + components/*.tsx public surface)
  runProcessGate("Type check (strict)", "npx", ["tsc", "-p", "tsconfig.json", "--noEmit"]);

  // ---- 5: declaration generation (regenerates every component's sibling .d.ts from its .tsx).
  // Snapshot every .d.ts before regenerating and diff after: if any byte changed, the
  // checked-in file was NOT what the compiler actually produces from the current .tsx —
  // i.e. it had drifted (hand-edited, or stale from a source change that skipped
  // regeneration). A clean repo must regenerate byte-identical output.
  function findDeclarationFiles(dir, out = []) {
    for (const f of fs.readdirSync(dir)) {
      const full = path.join(dir, f);
      const stat = fs.statSync(full);
      if (stat.isDirectory()) findDeclarationFiles(full, out);
      else if (f.endsWith(".d.ts") && !f.endsWith(".entry.d.ts")) out.push(full);
    }
    return out;
  }
  function snapshotDeclarations() {
    const snap = new Map();
    for (const f of findDeclarationFiles(path.join(ROOT, "components"))) {
      snap.set(f, fs.existsSync(f) ? fs.readFileSync(f, "utf8") : null);
    }
    return snap;
  }
  const beforeDecl = snapshotDeclarations();
  runProcessGate("Declaration generation (components/**/*.tsx -> sibling .d.ts)", "npx", ["tsc", "-p", "tsconfig.declarations.json"]);
  await runIoGate("Declaration drift check (checked-in .d.ts vs freshly generated)", "diff before/after tsc -p tsconfig.declarations.json", async () => {
    const afterFiles = findDeclarationFiles(path.join(ROOT, "components"));
    const drifted = [];
    const allPaths = new Set([...beforeDecl.keys(), ...afterFiles]);
    for (const f of allPaths) {
      const before = beforeDecl.get(f) ?? null;
      const after = fs.existsSync(f) ? fs.readFileSync(f, "utf8") : null;
      if (before !== after) drifted.push(path.relative(ROOT, f));
    }
    if (drifted.length) {
      throw new Error(
        "Declaration(s) did not match freshly-generated output before this run (now corrected): " + drifted.join(", ")
      );
    }
    return { declarationFilesChecked: allPaths.size, drifted: 0 };
  });
  // dist/ is rebuilt from scratch for the build gates below: the library build keeps
  // emptyOutDir:false (so vite never wipes dist/types), which means stale chunks from an
  // earlier source state would otherwise survive and could mask a broken current build.
  fs.rmSync(path.join(ROOT, "dist"), { recursive: true, force: true });
  runProcessGate("Declaration generation (package entry points -> dist/types)", "npx", ["tsc", "-p", "tsconfig.build.json"]);

  // ---- 6: explorer production build — a broken import/export fails this outright
  runProcessGate("Explorer production build (Vite, no in-browser transpilation)", "npx", ["vite", "build", "--config", "vite.explorer.config.ts"]);

  // ---- 7: library build (the shipped dist/*.mjs|cjs)
  runProcessGate("Library build (Vite, dist/*.mjs + dist/*.cjs)", "npx", ["vite", "build"]);

  // ---- 8: package export completeness — every symbol src/index.ts re-exports must be
  // resolvable from the built library output (catches a re-export of a name that doesn't exist).
  const exportCheckScript = path.join(ROOT, "dist", ".export-check.mjs");
  fs.mkdirSync(path.dirname(exportCheckScript), { recursive: true });
  fs.writeFileSync(
    exportCheckScript,
    [
      "import * as index from './index.mjs';",
      "import * as tokens from './tokens.mjs';",
      "import * as themes from './themes.mjs';",
      "import * as density from './density.mjs';",
      "import * as icons from './icons.mjs';",
      "import * as primitives from './primitives.mjs';",
      "import * as domain from './domain.mjs';",
      "import * as tailwind from './tailwind.mjs';",
      "const modules = { index, tokens, themes, density, icons, primitives, domain, tailwind };",
      "for (const [name, mod] of Object.entries(modules)) {",
      "  const keys = Object.keys(mod);",
      "  if (!keys.length) throw new Error(name + ' exports nothing');",
      "  console.log(name + ': ' + keys.length + ' symbols');",
      "}",
    ].join("\n")
  );
  runProcessGate("Package export completeness (every dist/*.mjs entry point is importable and non-empty)", "node", [
    path.relative(ROOT, exportCheckScript),
  ]);
  fs.rmSync(exportCheckScript, { force: true }); // temp probe script — never left behind in dist/

  // ---- 9: no stale/hand-maintained gate artifacts. validation/report.md and report.json
  // are ALWAYS fully regenerated by this script and never hand-edited; any other
  // "_gate-*"-style artifact in validation/ is leftover hand-maintained drift.
  await runIoGate("No stale validation artifacts", "ls('validation') + filename check", async (io) => {
    const files = await io.ls("validation");
    const stale = files.filter((f) => /^_gate-.*\.json$/.test(f));
    if (stale.length) throw new Error("Stale hand-maintained gate artifact(s) found: " + stale.join(", ") + " — delete them; scripts/validate.js is the only writer of validation state.");
    return { scannedFiles: files.length, staleFound: stale.length };
  });

  // ---- 10: handoff manifest — classification completeness, canonical-source presence,
  // generated-required presence, no stale legacy duplicates, transient exclusion rules.
  await runIoGate(
    "Handoff manifest (classification, canonical sources, generated artifacts, stale duplicates, exclusions)",
    "checkVenomHandoff(io)",
    checkVenomHandoff
  );

  const finishedAt = nowIso();
  const failed = gates.filter((g) => g.status === "FAIL");
  const passed = gates.filter((g) => g.status === "PASS");

  const report = {
    package: pkg.name,
    version: pkg.version,
    runAt: runStartedAt,
    finishedAt,
    gateCount: gates.length,
    passed: passed.length,
    failed: failed.length,
    gates,
  };
  fs.writeFileSync(path.join(ROOT, "validation", "report.json"), JSON.stringify(report, null, 2));
  fs.writeFileSync(path.join(ROOT, "validation", "report.md"), renderMarkdown(report));

  console.log("");
  for (const g of gates) {
    console.log((g.status === "PASS" ? "PASS  " : "FAIL  ") + g.name);
    if (g.status === "FAIL") {
      const detail = g.error || g.output || "";
      console.log(
        "      " +
          detail
            .split("\n")
            .slice(0, 12)
            .join("\n      ")
      );
    }
  }
  console.log("");
  console.log(`${passed.length}/${gates.length} gates passed. Evidence: validation/report.md, validation/report.json.`);

  if (failed.length) process.exit(1);
}

function renderMarkdown(report) {
  let md = `# Validation report — Venom Design System\n\n`;
  md += `Run at \`${report.runAt}\` (finished \`${report.finishedAt}\`) against package \`${report.package}@${report.version}\` by \`scripts/validate.js\`. Every row below is an actual gate output from this run — not a hand-maintained estimate. Regenerate with \`npm run validate\`; never hand-edit this file.\n\n`;
  md += `## Gate results\n\n`;
  md += `| # | Gate | Status | Command |\n|---|------|--------|---------|\n`;
  report.gates.forEach((g, i) => {
    md += `| ${i + 1} | ${g.name} | ${g.status === "PASS" ? "**PASS**" : "**FAIL**"} | \`${g.command}\` |\n`;
  });
  md += `\n${report.passed}/${report.gateCount} gates passed.\n\n`;

  md += `## Gate detail\n\n`;
  for (const g of report.gates) {
    md += `### ${g.name}\n\n- Status: **${g.status}**\n- Command: \`${g.command}\`\n- Started: ${g.startedAt} · Finished: ${g.finishedAt}\n`;
    if (g.summary) md += `- Summary: \`${JSON.stringify(g.summary)}\`\n`;
    if (g.logs && g.logs.length) md += `- Log: ${g.logs.join(" · ")}\n`;
    if (g.status === "FAIL") {
      md += `- Error:\n\n\`\`\`\n${(g.error || "").slice(0, 4000)}\n\`\`\`\n`;
      if (g.output) md += `\n<details><summary>Raw output</summary>\n\n\`\`\`\n${g.output.slice(0, 6000)}\n\`\`\`\n\n</details>\n`;
    } else if (g.output) {
      md += `- Output: \`${g.output.slice(0, 500).replace(/\n/g, " ")}\`\n`;
    }
    md += `\n`;
  }

  md += `## Not covered by this command\n\n`;
  md += `\`npm run test:a11y\` and \`npm run test:visual\` (Playwright + axe-core, and the visual-regression suite) are separate commands — they drive a real browser and are not run as part of \`npm run validate\`. See \`tests/\` and the accessibility-evidence section of the remediation report for their status.\n`;

  return md;
}

main();

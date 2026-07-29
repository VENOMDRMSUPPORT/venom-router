import { readFileSync } from "node:fs";

const ci = readFileSync(new URL("../workflows/ci.yml", import.meta.url), "utf8");
const race = readFileSync(new URL("../workflows/race.yml", import.meta.url), "utf8");
const failures = [];

function requireText(source, text, label) {
  if (!source.includes(text)) failures.push(`${label}: missing ${JSON.stringify(text)}`);
}

function forbid(source, pattern, label) {
  if (pattern.test(source)) failures.push(`${label}: forbidden ${pattern}`);
}

function requireCount(source, text, count, label) {
  const actual = source.split(text).length - 1;
  if (actual !== count) failures.push(`${label}: expected ${count} occurrences of ${JSON.stringify(text)}, found ${actual}`);
}

for (const [source, label] of [[ci, "ci"], [race, "race"]]) {
  forbid(source, /(?:ubuntu|windows|macos)-latest/i, label);
  forbid(source, /actions\/cache@/i, label);
  forbid(source, /\b(?:winget|choco|chocolatey)\b/i, label);
  forbid(source, /\bgo install\b/i, label);
  requireCount(source, "uses: actions/setup-go@v5", 1, label);
  requireCount(source, "cache: false", 1, label);
  forbid(source, /^\s+cache:\s*true\s*$/mi, label);
}

requireText(ci, "runs-on: [self-hosted, Linux, X64, venom-linux]", "ci");
requireText(ci, "runs-on: [self-hosted, Windows, X64, venom]", "ci");
requireText(ci, "timeout-minutes: 8", "ci");
requireText(ci, "timeout-minutes: 6", "ci");
requireText(ci, "run: task gate", "ci");
requireText(ci, "run: task dashboard:build-embed", "ci");
requireText(ci, "CGO_ENABLED: \"0\"", "ci");
requireText(ci, "cancel-in-progress: true", "ci");
forbid(ci, /test-race/i, "ci");

requireText(race, "workflow_dispatch:", "race");
requireText(race, "cron: \"0 1 * * *\"", "race");
requireText(race, "runs-on: [self-hosted, Linux, X64, venom-linux]", "race");
requireText(race, "runs-on: [self-hosted, Windows, X64, venom]", "race");
requireCount(race, "timeout-minutes: 20", 2, "race");
requireCount(race, "test-race", 2, "race");
requireText(race, "C:\\ProgramData\\VenomCI\\mingw64\\bin\\gcc.exe", "race");
forbid(race, /^\s+(?:push|pull_request):/m, "race");

if (failures.length) {
  console.error(failures.join("\n"));
  process.exit(1);
}

console.log("CI contract: zero-hosted fast path and non-blocking race verified");

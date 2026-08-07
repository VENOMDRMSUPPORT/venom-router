import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, symlinkSync, writeFileSync } from "node:fs";
import { mkdtemp, readFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { npmCommandSpec, prepareDependencies } from "./dev-bootstrap.mjs";

const INSTALL_ARGS = ["ci", "--prefer-offline", "--no-audit", "--no-fund"];

test("Windows npm commands run through cmd instead of spawning npm.cmd directly", () => {
  assert.deepEqual(npmCommandSpec("win32", ["--version"], "C:\\Windows\\System32\\cmd.exe"), {
    command: "C:\\Windows\\System32\\cmd.exe",
    args: ["/d", "/s", "/c", "npm", "--version"],
  });
});

test("non-Windows npm commands execute npm directly", () => {
  assert.deepEqual(npmCommandSpec("linux", ["--version"]), {
    command: "npm",
    args: ["--version"],
  });
});

async function makeFixture({
  vite = false,
  matchingStamp = false,
  linkedDesignSystem = false,
  designSystemPackage = true,
} = {}) {
  const root = await mkdtemp(path.join(os.tmpdir(), "venom-dev-bootstrap-"));
  const dashboard = path.join(root, "dashboard");
  const designSystem = path.join(root, "Design_System");
  const lock = '{"name":"fixture","lockfileVersion":3}\n';
  const originalPackage = '{"name":"@venom/design-system","version":"1.0.0"}\n';
  const vitePath = path.join(
    dashboard,
    "node_modules",
    ".bin",
    process.platform === "win32" ? "vite.cmd" : "vite",
  );
  const stamp = path.join(dashboard, "node_modules", ".venom-dev-install.sha256");
  const designSystemPackagePath = path.join(designSystem, "package.json");

  mkdirSync(dashboard, { recursive: true });
  mkdirSync(designSystem, { recursive: true });
  writeFileSync(path.join(dashboard, "package.json"), '{"name":"fixture"}\n');
  writeFileSync(path.join(dashboard, "package-lock.json"), lock);
  if (designSystemPackage) writeFileSync(designSystemPackagePath, originalPackage);

  const installVite = async () => {
    mkdirSync(path.dirname(vitePath), { recursive: true });
    writeFileSync(vitePath, "fixture vite executable\n");
  };
  if (vite) await installVite();

  if (matchingStamp) {
    mkdirSync(path.dirname(stamp), { recursive: true });
    writeFileSync(stamp, createHash("sha256").update(lock).digest("hex") + "\n");
  }

  let dependencyLink = null;
  if (linkedDesignSystem) {
    const scope = path.join(dashboard, "node_modules", "@venom");
    mkdirSync(scope, { recursive: true });
    dependencyLink = path.join(scope, "design-system");
    symlinkSync(designSystem, dependencyLink, process.platform === "win32" ? "junction" : "dir");
  }

  return {
    dashboard,
    dependencyLink,
    designSystemPackage: designSystemPackagePath,
    installVite,
    originalPackage,
    stamp,
    vitePath,
  };
}

test("matching stamp and vite executable skip installation", async () => {
  const fixture = await makeFixture({ vite: true, matchingStamp: true });
  let installs = 0;

  const result = await prepareDependencies({
    dashboardRoot: fixture.dashboard,
    platform: process.platform,
    runInstall: async () => {
      installs += 1;
      return 0;
    },
  });

  assert.equal(result.repaired, false);
  assert.equal(installs, 0);
});

test("missing vite performs one deterministic repair", async () => {
  const fixture = await makeFixture();
  const calls = [];

  const result = await prepareDependencies({
    dashboardRoot: fixture.dashboard,
    platform: process.platform,
    runInstall: async (args) => {
      calls.push(args);
      await fixture.installVite();
      return 0;
    },
  });

  assert.equal(result.repaired, true);
  assert.deepEqual(calls, [INSTALL_ARGS]);
  assert.equal(existsSync(fixture.stamp), true);
});

test("changed lockfile triggers repair even when vite exists", async () => {
  const fixture = await makeFixture({ vite: true, matchingStamp: true });
  writeFileSync(path.join(fixture.dashboard, "package-lock.json"), '{"name":"changed"}\n');
  let installs = 0;

  await prepareDependencies({
    dashboardRoot: fixture.dashboard,
    platform: process.platform,
    runInstall: async () => {
      installs += 1;
      await fixture.installVite();
      return 0;
    },
  });

  assert.equal(installs, 1);
});

test("repair unlinks the dependency junction without touching its target", async () => {
  const fixture = await makeFixture({ linkedDesignSystem: true });

  await prepareDependencies({
    dashboardRoot: fixture.dashboard,
    platform: process.platform,
    runInstall: async () => {
      assert.equal(existsSync(fixture.dependencyLink), false);
      await fixture.installVite();
      return 0;
    },
  });

  assert.equal(await readFile(fixture.designSystemPackage, "utf8"), fixture.originalPackage);
});

test("failed install writes no success stamp", async () => {
  const fixture = await makeFixture();

  await assert.rejects(
    prepareDependencies({
      dashboardRoot: fixture.dashboard,
      platform: process.platform,
      runInstall: async () => 7,
    }),
    /npm ci failed with exit code 7/,
  );
  assert.equal(existsSync(fixture.stamp), false);
});

test("missing Design System package fails before installation", async () => {
  const fixture = await makeFixture({ designSystemPackage: false });
  let installs = 0;

  await assert.rejects(
    prepareDependencies({
      dashboardRoot: fixture.dashboard,
      platform: process.platform,
      runInstall: async () => {
        installs += 1;
        return 0;
      },
    }),
    /Design_System.package.json is missing/,
  );
  assert.equal(installs, 0);
});

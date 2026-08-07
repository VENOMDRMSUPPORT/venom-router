#!/usr/bin/env node
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { constants } from "node:fs";
import { access, lstat, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const INSTALL_ARGS = ["ci", "--prefer-offline", "--no-audit", "--no-fund"];
const STAMP_NAME = ".venom-dev-install.sha256";

async function fileExists(file) {
  try {
    await access(file, constants.F_OK);
    return true;
  } catch {
    return false;
  }
}

async function requireFile(file, label) {
  if (!(await fileExists(file))) {
    throw new Error(`development prerequisite missing: ${label} is missing (${file})`);
  }
}

function viteExecutable(dashboardRoot, platform) {
  return path.join(
    dashboardRoot,
    "node_modules",
    ".bin",
    platform === "win32" ? "vite.cmd" : "vite",
  );
}

export async function inspectInstall({ dashboardRoot, platform, ignoreStamp = false }) {
  const packagePath = path.join(dashboardRoot, "package.json");
  const lockPath = path.join(dashboardRoot, "package-lock.json");
  const designSystemPackage = path.resolve(dashboardRoot, "../Design_System/package.json");
  const stampPath = path.join(dashboardRoot, "node_modules", STAMP_NAME);
  const vitePath = viteExecutable(dashboardRoot, platform);

  await requireFile(packagePath, "dashboard/package.json");
  await requireFile(lockPath, "dashboard/package-lock.json");
  await requireFile(designSystemPackage, "Design_System/package.json");

  const lockHash = createHash("sha256")
    .update(await readFile(lockPath))
    .digest("hex");
  const vitePresent = await fileExists(vitePath);
  let stampMatches = false;
  if (!ignoreStamp && (await fileExists(stampPath))) {
    stampMatches = (await readFile(stampPath, "utf8")).trim() === lockHash;
  }

  return {
    lockHash,
    stampMatches,
    stampPath,
    valid: vitePresent && stampMatches,
    vitePath,
    vitePresent,
  };
}

async function unlinkLocalDesignSystemLink(dashboardRoot) {
  const dependency = path.join(dashboardRoot, "node_modules", "@venom", "design-system");
  let info;
  try {
    info = await lstat(dependency);
  } catch (error) {
    if (error?.code === "ENOENT") return;
    throw error;
  }
  if (info.isSymbolicLink()) {
    await rm(dependency, { force: true, recursive: false });
  }
}

export async function prepareDependencies({ dashboardRoot, platform, runInstall }) {
  const state = await inspectInstall({ dashboardRoot, platform });
  if (state.valid) return { repaired: false, lockHash: state.lockHash };

  await unlinkLocalDesignSystemLink(dashboardRoot);
  await requireFile(
    path.resolve(dashboardRoot, "../Design_System/package.json"),
    "Design_System/package.json",
  );

  const code = await runInstall(INSTALL_ARGS);
  if (code !== 0) {
    throw new Error(`dependency repair failed: npm ci failed with exit code ${code}`);
  }

  const repaired = await inspectInstall({ dashboardRoot, platform, ignoreStamp: true });
  if (!repaired.vitePresent) {
    throw new Error("dependency repair failed: Vite executable is still missing after npm ci");
  }
  await mkdir(path.dirname(repaired.stampPath), { recursive: true });
  await writeFile(repaired.stampPath, repaired.lockHash + "\n", "utf8");
  return { repaired: true, lockHash: repaired.lockHash };
}

function run(command, args) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { stdio: "inherit", shell: false });
    child.once("error", reject);
    child.once("exit", (code, signal) => {
      if (signal) {
        reject(new Error(`${command} terminated by ${signal}`));
        return;
      }
      resolve(code ?? 1);
    });
  });
}

export function npmCommandSpec(platform, args, comspec = process.env.ComSpec) {
  if (platform === "win32") {
    return {
      command: comspec || "cmd.exe",
      args: ["/d", "/s", "/c", "npm", ...args],
    };
  }
  return { command: "npm", args };
}

function runNpm(args) {
  const spec = npmCommandSpec(process.platform, args);
  return run(spec.command, spec.args);
}

async function main() {
  const dashboardRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

  const result = await prepareDependencies({
    dashboardRoot,
    platform: process.platform,
    runInstall: runNpm,
  });
  console.log(
    result.repaired
      ? "venom-dev: dashboard dependencies repaired successfully"
      : "venom-dev: dashboard dependencies are valid",
  );

  const code = await runNpm(["run", "dev", "--", ...process.argv.slice(2)]);
  process.exitCode = code;
}

const invokedPath = process.argv[1] ? pathToFileURL(path.resolve(process.argv[1])).href : "";
if (import.meta.url === invokedPath) {
  main().catch((error) => {
    console.error(`venom-dev: ${error instanceof Error ? error.message : String(error)}`);
    process.exitCode = 1;
  });
}

# Zero-Cost Fast CI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a zero-billed, two-OS self-hosted CI system that returns normal push and pull-request feedback within five minutes on a warm, online host.

**Architecture:** Add one Ubuntu 24.04 GitHub Actions runner inside WSL2 for the authoritative Linux gate and dashboard verification, while retaining the current Windows service runner for a parallel `CGO_ENABLED=0` smoke job. Move Linux and Windows race detection to a separate nightly/manual workflow with hard timeouts, and provision every required tool outside CI.

**Tech Stack:** GitHub Actions, GitHub CLI, WSL 2.7.10, Ubuntu 24.04 LTS, systemd, Windows Task Scheduler, PowerShell, Bash, Go 1.26.5, Node 20, Task 3.52.0, golangci-lint v2.12.2, goimports v0.48.0.

## Global Constraints

- GitHub-hosted runners must not be used. Every workflow job must target a self-hosted runner.
- Do not modify, rebuild outside its existing validation flow, copy, or vendor content from `Design_System/`.
- Preserve the pinned toolchain: Go `1.26.5`, golangci-lint `v2.12.2`, goimports `v0.48.0`, and Task `3.52.0`.
- `task gate` remains the single authoritative static gate; gate logic must not be duplicated in workflow YAML.
- Do not install or repair host tools from inside a CI job. CI may validate exact versions and fail fast, but provisioning is a separate host step.
- Do not run the race detector in the blocking push/pull-request workflow.
- Keep `venom-win-selfhosted-2` stopped and do not modify or delete `C:\actions-runner-2`.
- Do not run `wsl --unregister`, delete a WSL distribution, or alter the existing `docker-desktop` distribution.
- Registration tokens remain ephemeral and must not be printed, saved in the repository, or written to shell history.
- Do not cancel an active GitHub Actions job to begin this rollout. Wait for the repository to become idle.
- The GitHub Free private-repository API currently returns HTTP 403 for branch protection. Do not upgrade or enable paid features for this rollout; no required-check configuration write is in scope.
- Use `superpowers:using-git-worktrees` before implementation and create the branch `codex/zero-cost-fast-ci` from the latest local `main` containing this plan. Commit `9b98833b725059bb7560060f51b84cb0093b17c6` must be an ancestor.

---

### Task 1: Capture the safety baseline

**Files:**
- Read: `.github/workflows/ci.yml`
- Read: `Taskfile.yml`
- Read: `docs/superpowers/specs/2026-07-28-zero-cost-fast-ci-design.md`
- Verify: `Design_System/` and current GitHub runner state

**Interfaces:**
- Consumes: the latest local `main` containing this plan and design commit `9b98833b725059bb7560060f51b84cb0093b17c6`, the existing `venom-win-selfhosted` service, and GitHub CLI authentication.
- Produces: a clean, idle, reproducible starting point; later tasks must not proceed without it.

- [ ] **Step 1: Confirm the implementation branch and clean worktree**

Run from the isolated worktree:

```powershell
git branch --show-current
git rev-parse HEAD
$dirty = git status --porcelain
if ((git branch --show-current) -ne 'codex/zero-cost-fast-ci') { throw 'Wrong implementation branch' }
git merge-base --is-ancestor 9b98833b725059bb7560060f51b84cb0093b17c6 HEAD
if ($LASTEXITCODE -ne 0) { throw 'Design commit is not an ancestor of the implementation branch' }
if (-not (Test-Path -LiteralPath 'docs\superpowers\plans\2026-07-29-zero-cost-fast-ci.md')) { throw 'Implementation plan is missing from the branch' }
if ($dirty) { throw "Worktree is not clean:`n$dirty" }
```

Expected: branch `codex/zero-cost-fast-ci`, design and plan both present in history, and no dirty paths.

- [ ] **Step 2: Verify GitHub authentication and workflow idleness**

```powershell
gh auth status
$runs = gh run list --limit 30 --json databaseId,status,workflowName,url | ConvertFrom-Json
$active = @($runs | Where-Object status -In @('queued','in_progress','waiting','pending','requested'))
if ($active.Count -ne 0) {
  $active | Format-Table databaseId,workflowName,status,url
  throw 'Wait for active CI runs; do not cancel them for this rollout'
}
```

Expected: authentication succeeds and `$active.Count` is zero.

- [ ] **Step 3: Capture the current runner boundary**

```powershell
$runners = gh api repos/VENOMDRMSUPPORT/venom-router/actions/runners | ConvertFrom-Json
$runners.runners | ForEach-Object {
  [pscustomobject]@{
    Name   = $_.name
    OS     = $_.os
    Status = $_.status
    Busy   = $_.busy
    Labels = $_.labels.name -join ','
  }
} | Format-Table -AutoSize

$win1 = $runners.runners | Where-Object name -eq 'venom-win-selfhosted'
$win2 = $runners.runners | Where-Object name -eq 'venom-win-selfhosted-2'
if ($win1.status -ne 'online' -or $win1.busy) { throw 'Primary Windows runner is not online and idle' }
if ($win2.status -ne 'offline') { throw 'Second Windows runner must remain offline' }
```

Expected: primary Windows runner online/idle; second runner offline.

- [ ] **Step 4: Prove the protected Design System baseline is clean**

```powershell
npm --prefix dashboard run check:ds-adherence
$designDrift = git status --short -- Design_System
if ($designDrift) { throw "Design_System is not clean:`n$designDrift" }
```

Expected: adherence succeeds and no tracked `Design_System/` change exists.

---

### Task 2: Install and initialize Ubuntu 24.04 under WSL2

**Files:**
- Host create: WSL distribution `Ubuntu-24.04`
- Host create: `/etc/wsl.conf` inside `Ubuntu-24.04`
- Host create: Linux user `venomci`
- Preserve: `C:\Users\hamee\.wslconfig`

**Interfaces:**
- Consumes: WSL 2.7.10, Hyper-V, and the existing `.wslconfig` limits of 16 GB RAM, 8 processors, and 4 GB swap.
- Produces: an Ubuntu 24.04 WSL2 distribution whose default unprivileged user is `venomci` and whose PID 1 is systemd.

- [ ] **Step 1: Re-verify WSL capacity without changing it**

```powershell
wsl --version
wsl --status
wsl --list --verbose
Get-Content -Raw -LiteralPath "$env:USERPROFILE\.wslconfig"
```

Expected: WSL 2.7.10 or newer, default version 2, only `docker-desktop` currently listed, and the existing 16GB/8-processor/4GB limits remain unchanged.

- [ ] **Step 2: Install the exact Ubuntu distribution**

```powershell
wsl --install -d Ubuntu-24.04 --no-launch
```

Expected: `Ubuntu-24.04` installs successfully. If Windows explicitly reports that a restart is required, stop at this checkpoint, request approval for the restart, and resume with Step 3 after Windows returns. If the command fails for elevation, stop and request an Administrator PowerShell session; do not bypass UAC.

- [ ] **Step 3: Create the dedicated unprivileged CI user**

```powershell
wsl -d Ubuntu-24.04 -u root -- bash -lc 'set -euo pipefail; id -u venomci >/dev/null 2>&1 || useradd --create-home --shell /bin/bash venomci; passwd --lock venomci; id venomci'
```

Expected: `uid`, `gid`, and group output for `venomci`; the account has no interactive password.

- [ ] **Step 4: Enable systemd and select the default user**

```powershell
wsl -d Ubuntu-24.04 -u root -- bash -lc 'printf "[boot]\nsystemd=true\n\n[user]\ndefault=venomci\n" > /etc/wsl.conf; chmod 0644 /etc/wsl.conf; cat /etc/wsl.conf'
wsl --shutdown
wsl -d Ubuntu-24.04 -- true
```

Expected: `/etc/wsl.conf` contains only the shown boot and user sections. `wsl --shutdown` does not unregister or delete either distribution.

- [ ] **Step 5: Verify WSL2, systemd, and the default identity**

```powershell
wsl --list --verbose
$identity = wsl -d Ubuntu-24.04 -- id -un
$init = wsl -d Ubuntu-24.04 -- ps -p 1 -o comm=
if (($identity.Trim()) -ne 'venomci') { throw "Unexpected WSL user: $identity" }
if (($init.Trim()) -ne 'systemd') { throw "systemd is not PID 1: $init" }
```

Expected: Ubuntu is version 2, default user is `venomci`, and PID 1 is `systemd`.

---

### Task 3: Provision and verify the pinned Linux toolchain

**Files:**
- Host create: `/opt/go/1.26.5`
- Host create: `/opt/venom-ci/bin`
- Host create: NodeSource APT key and Node 20 repository configuration
- Host symlink: `/usr/local/bin/{go,gofmt,task,golangci-lint,goimports}`

**Interfaces:**
- Consumes: the `Ubuntu-24.04` distribution and user `venomci` from Task 2.
- Produces: Go 1.26.5, Node 20, GCC, Task 3.52.0, golangci-lint v2.12.2, and goimports v0.48.0 resolvable by a fresh `venomci` process.

- [ ] **Step 1: Install the base Linux packages**

```powershell
wsl -d Ubuntu-24.04 -u root -- bash -lc 'set -euo pipefail; apt-get update; DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl git gnupg jq build-essential tar gzip; git --version; gcc --version | head -n 1'
```

Expected: package installation exits zero; Git and GCC print versions.

- [ ] **Step 2: Install Go 1.26.5 from its checksum-verified official archive**

```powershell
wsl -d Ubuntu-24.04 -u root -- bash -lc 'set -euo pipefail
archive=/tmp/go1.26.5.linux-amd64.tar.gz
curl -fsSL https://go.dev/dl/go1.26.5.linux-amd64.tar.gz -o "$archive"
echo "5c2c3b16caefa1d968a94c1daca04a7ca301a496d9b086e17ad77bb81393f053  $archive" | sha256sum --check
test ! -e /opt/go/1.26.5
install -d -m 0755 /opt/go/1.26.5
tar -C /opt/go/1.26.5 --strip-components=1 -xzf "$archive"
ln -sfn /opt/go/1.26.5/bin/go /usr/local/bin/go
ln -sfn /opt/go/1.26.5/bin/gofmt /usr/local/bin/gofmt
rm -f "$archive"
go version'
```

Expected: checksum reports `OK`; `go version go1.26.5 linux/amd64` is printed. If `/opt/go/1.26.5` already exists, stop and inspect it rather than deleting or overwriting it.

- [ ] **Step 3: Install Node 20 from the signed NodeSource repository**

```powershell
wsl -d Ubuntu-24.04 -u root -- bash -lc 'set -euo pipefail
install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key | gpg --dearmor --yes -o /etc/apt/keyrings/nodesource.gpg
echo "deb [signed-by=/etc/apt/keyrings/nodesource.gpg] https://deb.nodesource.com/node_20.x nodistro main" > /etc/apt/sources.list.d/nodesource.list
apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y nodejs
node --version
npm --version'
```

Expected: Node reports a version matching `v20.*`; npm resolves from the fresh root process.

- [ ] **Step 4: Install the pinned Go tools once into a machine-readable directory**

```powershell
wsl -d Ubuntu-24.04 -u root -- bash -lc 'set -euo pipefail
install -d -o venomci -g venomci -m 0755 /opt/venom-ci/bin
runuser -u venomci -- env HOME=/home/venomci GOBIN=/opt/venom-ci/bin go install github.com/go-task/task/v3/cmd/task@v3.52.0
runuser -u venomci -- env HOME=/home/venomci GOBIN=/opt/venom-ci/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
runuser -u venomci -- env HOME=/home/venomci GOBIN=/opt/venom-ci/bin go install golang.org/x/tools/cmd/goimports@v0.48.0
ln -sfn /opt/venom-ci/bin/task /usr/local/bin/task
ln -sfn /opt/venom-ci/bin/golangci-lint /usr/local/bin/golangci-lint
ln -sfn /opt/venom-ci/bin/goimports /usr/local/bin/goimports'
```

Expected: all three binaries exist in `/opt/venom-ci/bin`; no repository workflow performed the installation.

- [ ] **Step 5: Verify every pin from a fresh unprivileged process**

```powershell
wsl --shutdown
wsl -d Ubuntu-24.04 -- bash -lc 'set -euo pipefail
test "$(id -un)" = venomci
test "$(go version)" = "go version go1.26.5 linux/amd64"
node --version | grep -E "^v20\."
task --version | grep -F "3.52.0"
golangci-lint --version | grep -F "2.12.2"
go version -m "$(command -v goimports)" | grep -E "golang.org/x/tools[[:space:]]+v0.48.0"
git --version
gcc --version | head -n 1'
```

Expected: every command exits zero from a fresh `venomci` shell. If Node verification fails because the printed major is not 20, stop and repair the host package selection before runner registration.

---

### Task 4: Register the Linux runner and make startup recoverable

**Files:**
- Host create: `/opt/actions-runner-venom`
- Host create: systemd unit generated by `svc.sh`
- Host create: Windows scheduled task `Venom-CI-WSL-Runner`

**Interfaces:**
- Consumes: the verified Ubuntu toolchain from Task 3 and a one-hour repository runner registration token.
- Produces: online runner `venom-linux-selfhosted` with labels `self-hosted,Linux,X64,venom-linux`, plus cold-start recovery at Windows logon.

- [ ] **Step 1: Prove the runner is not registered yet**

```powershell
$before = gh api repos/VENOMDRMSUPPORT/venom-router/actions/runners | ConvertFrom-Json
if ($before.runners.name -contains 'venom-linux-selfhosted') { throw 'Linux runner name is already registered; inspect before using --replace' }
```

Expected: the runner name is absent.

- [ ] **Step 2: Download and verify Actions runner 2.336.0**

```powershell
wsl -d Ubuntu-24.04 -u root -- bash -lc 'set -euo pipefail
test ! -e /opt/actions-runner-venom
install -d -o venomci -g venomci -m 0755 /opt/actions-runner-venom
archive=/tmp/actions-runner-linux-x64-2.336.0.tar.gz
curl -fsSL https://github.com/actions/runner/releases/download/v2.336.0/actions-runner-linux-x64-2.336.0.tar.gz -o "$archive"
echo "04cf0be1aff4c3ec3554466c39124ca250e3effd8873bb7e8d68535aa9505d5d  $archive" | sha256sum --check
tar -C /opt/actions-runner-venom -xzf "$archive"
rm -f "$archive"
chown -R venomci:venomci /opt/actions-runner-venom
/opt/actions-runner-venom/bin/installdependencies.sh'
```

Expected: checksum reports `OK`; dependencies install; the directory is owned by `venomci`. If the target exists, stop and inspect it rather than deleting it.

- [ ] **Step 3: Register without persisting or printing the token**

Run this PowerShell block in a terminal whose transcript/logging is disabled:

```powershell
$registrationToken = (gh api --method POST repos/VENOMDRMSUPPORT/venom-router/actions/runners/registration-token | ConvertFrom-Json).token
try {
  $configCommand = 'cd /opt/actions-runner-venom && ./config.sh --unattended --replace --url https://github.com/VENOMDRMSUPPORT/venom-router --token ' + [Management.Automation.Language.CodeGeneration]::EscapeSingleQuotedStringContent($registrationToken) + ' --name venom-linux-selfhosted --labels venom-linux --work _work'
  wsl -d Ubuntu-24.04 -u venomci -- bash -lc $configCommand
  if ($LASTEXITCODE -ne 0) { throw 'Runner registration failed' }
}
finally {
  $registrationToken = $null
  $configCommand = $null
}
```

Expected: registration succeeds and the token variables are cleared. Do not echo either variable.

- [ ] **Step 4: Install and start the runner as a systemd service**

```powershell
wsl -d Ubuntu-24.04 -u root -- bash -lc 'set -euo pipefail; cd /opt/actions-runner-venom; ./svc.sh install venomci; ./svc.sh start; ./svc.sh status'
```

Expected: the generated `actions.runner.VENOMDRMSUPPORT-venom-router.venom-linux-selfhosted.service` is active and running as `venomci`.

- [ ] **Step 5: Register an idempotent hidden Windows logon task**

```powershell
$taskName = 'Venom-CI-WSL-Runner'
$service = 'actions.runner.VENOMDRMSUPPORT-venom-router.venom-linux-selfhosted.service'
$action = New-ScheduledTaskAction -Execute "$env:SystemRoot\System32\wsl.exe" -Argument "-d Ubuntu-24.04 -u root -- /bin/systemctl start $service"
$trigger = New-ScheduledTaskTrigger -AtLogOn -User "$env:USERDOMAIN\$env:USERNAME"
$settings = New-ScheduledTaskSettingsSet -StartWhenAvailable -MultipleInstances IgnoreNew -ExecutionTimeLimit (New-TimeSpan -Minutes 2)
$principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" -LogonType Interactive -RunLevel Limited
Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Description 'Start the Venom GitHub Actions runner inside Ubuntu WSL' -Force
```

Expected: task registration succeeds under the current Windows user and does not store an additional password.

- [ ] **Step 6: Prove cold-start recovery without rebooting Windows**

```powershell
wsl --shutdown
Start-ScheduledTask -TaskName 'Venom-CI-WSL-Runner'
$deadline = (Get-Date).AddSeconds(90)
do {
  Start-Sleep -Seconds 3
  $linux = (gh api repos/VENOMDRMSUPPORT/venom-router/actions/runners | ConvertFrom-Json).runners | Where-Object name -eq 'venom-linux-selfhosted'
} until (($linux.status -eq 'online' -and -not $linux.busy) -or (Get-Date) -ge $deadline)
if ($linux.status -ne 'online' -or $linux.busy) { throw 'WSL runner did not recover online within 90 seconds' }
$linux | Select-Object name,os,status,busy,@{n='labels';e={$_.labels.name -join ','}}
```

Expected: WSL cold-starts and GitHub reports `venom-linux-selfhosted` online and idle within 90 seconds.

---

### Task 5: Provision fixed Windows tools for the non-blocking race job

**Files:**
- Host create: `C:\ProgramData\VenomCI\mingw64`
- Host create: `C:\ProgramData\VenomCI\bin\task.exe`
- Preserve: the user-scoped WinLibs installation

**Interfaces:**
- Consumes: the existing user-scoped WinLibs toolchain and machine Go installation.
- Produces: an absolute GCC path and Task executable readable/executable by `NT AUTHORITY\NETWORK SERVICE`.

- [ ] **Step 1: Prove the fixed target paths do not already exist**

```powershell
$ciRoot = 'C:\ProgramData\VenomCI'
$mingwTarget = Join-Path $ciRoot 'mingw64'
$gccTarget = Join-Path $ciRoot 'mingw64\bin\gcc.exe'
$taskTarget = Join-Path $ciRoot 'bin\task.exe'
if (Test-Path -LiteralPath $mingwTarget) { throw "$mingwTarget already exists; inspect it instead of overwriting" }
if (Test-Path -LiteralPath $taskTarget) { throw "$taskTarget already exists; inspect it instead of overwriting" }
```

Expected: both target files are absent.

- [ ] **Step 2: Resolve exactly one existing WinLibs source**

```powershell
$sources = @(Get-ChildItem -LiteralPath 'C:\Users\hamee\AppData\Local\Microsoft\WinGet\Packages' -Directory -Filter 'BrechtSanders.WinLibs.POSIX.UCRT*' | ForEach-Object {
  Join-Path $_.FullName 'mingw64'
} | Where-Object { Test-Path -LiteralPath (Join-Path $_ 'bin\gcc.exe') })
if ($sources.Count -ne 1) { throw "Expected exactly one WinLibs source, found $($sources.Count)" }
$mingwSource = $sources[0]
& (Join-Path $mingwSource 'bin\gcc.exe') --version
```

Expected: exactly one source and a valid GCC version.

- [ ] **Step 3: Copy the portable toolchain to the machine-readable location**

```powershell
New-Item -ItemType Directory -Path 'C:\ProgramData\VenomCI' -Force | Out-Null
Copy-Item -LiteralPath $mingwSource -Destination 'C:\ProgramData\VenomCI\mingw64' -Recurse
if (-not (Test-Path -LiteralPath 'C:\ProgramData\VenomCI\mingw64\bin\gcc.exe')) { throw 'GCC copy is incomplete' }
```

Expected: the original source remains untouched and the complete portable tree exists under `ProgramData`.

- [ ] **Step 4: Install the pinned Task binary once**

```powershell
New-Item -ItemType Directory -Path 'C:\ProgramData\VenomCI\bin' -Force | Out-Null
$previousGobin = $env:GOBIN
try {
  $env:GOBIN = 'C:\ProgramData\VenomCI\bin'
  go install github.com/go-task/task/v3/cmd/task@v3.52.0
}
finally {
  $env:GOBIN = $previousGobin
}
& 'C:\ProgramData\VenomCI\bin\task.exe' --version
```

Expected: Task reports 3.52.0.

- [ ] **Step 5: Grant read/execute access and verify absolute commands**

```powershell
icacls 'C:\ProgramData\VenomCI' /grant '*S-1-5-20:(OI)(CI)RX' /T /C
& 'C:\ProgramData\VenomCI\mingw64\bin\gcc.exe' --version
& 'C:\ProgramData\VenomCI\bin\task.exe' --version
```

Expected: the Network Service SID receives inherited read/execute permissions; both absolute commands succeed. No PATH, winget, or Chocolatey change is made.

---

### Task 6: Write the CI contract test and replace workflow routing

**Files:**
- Create: `.github/scripts/verify-ci-contract.mjs`
- Modify: `.github/workflows/ci.yml`
- Create: `.github/workflows/race.yml`
- Modify: `Taskfile.yml:72-88` description only
- Test: `.github/scripts/verify-ci-contract.mjs`

**Interfaces:**
- Consumes: runner labels `venom-linux` and `venom`, Task targets `gate`, `dashboard:build-embed`, and `test-race`, and fixed Windows tool paths from Task 5.
- Produces: blocking `CI` with Linux gate/dashboard plus parallel Windows smoke, non-blocking `Race`, and a dependency-free workflow invariant test.

- [ ] **Step 1: Write the failing workflow contract test**

Create `.github/scripts/verify-ci-contract.mjs`:

```javascript
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
```

- [ ] **Step 2: Run the contract test and verify the red state**

```powershell
node .github/scripts/verify-ci-contract.mjs
```

Expected: FAIL because `.github/workflows/race.yml` is absent and the current `ci.yml` contains race/install/cache logic.

- [ ] **Step 3: Replace `.github/workflows/ci.yml` with the exact fast workflow**

```yaml
name: CI

on:
  push:
    branches: [main]
    paths-ignore:
      - "**/*.md"
  pull_request:
    paths-ignore:
      - "**/*.md"

permissions:
  contents: read

concurrency:
  group: ci-${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: true

env:
  GO_VERSION: "1.26.5"

jobs:
  gate:
    name: gate (self-hosted Linux)
    runs-on: [self-hosted, Linux, X64, venom-linux]
    timeout-minutes: 8
    steps:
      - name: Checkout with pinned Bifrost submodule
        uses: actions/checkout@v4
        with:
          submodules: recursive

      - name: Verify CI workflow contract
        run: node .github/scripts/verify-ci-contract.mjs

      - name: Verify pinned Linux toolchain
        shell: bash
        run: |
          set -euo pipefail
          test "$(go version)" = "go version go1.26.5 linux/amd64"
          node --version | grep -E "^v20\."
          task --version | grep -F "3.52.0"
          golangci-lint --version | grep -F "2.12.2"
          go version -m "$(command -v goimports)" | grep -E "golang.org/x/tools[[:space:]]+v0.48.0"
          git --version

      - name: Run the authoritative static gate
        run: task gate

      - name: Check Design System adherence
        working-directory: dashboard
        run: npm run check:ds-adherence

      - name: Install Design System dependencies
        working-directory: Design_System
        run: npm ci

      - name: Install dashboard dependencies
        working-directory: dashboard
        run: npm ci

      - name: Build and embed dashboard once
        run: task dashboard:build-embed

      - name: Validate Design System
        working-directory: Design_System
        run: npm run validate

      - name: Lint dashboard
        working-directory: dashboard
        run: npm run lint

      - name: Test dashboard
        working-directory: dashboard
        run: npm run test

      - name: Prove the embedded dashboard is served
        run: go test ./internal/httpui/... ./internal/app/... -run 'TestBoot_ServesEmbeddedDashboardSPA|TestNew_UsesEmbeddedDist' -v

  windows-smoke:
    name: windows smoke (self-hosted)
    runs-on: [self-hosted, Windows, X64, venom]
    timeout-minutes: 6
    env:
      CGO_ENABLED: "0"
    steps:
      - name: Checkout with pinned Bifrost submodule
        uses: actions/checkout@v4
        with:
          submodules: recursive

      - name: Set up pinned Go without Actions cache
        uses: actions/setup-go@v5
        with:
          go-version: ${{ env.GO_VERSION }}
          cache: false

      - name: Verify Windows prerequisites
        shell: powershell
        run: |
          if ((go version) -ne 'go version go1.26.5 windows/amd64') { throw "Unexpected Go: $(go version)" }
          git --version

      - name: Run Windows smoke tests
        run: go test ./...
```

- [ ] **Step 4: Create `.github/workflows/race.yml` with the exact non-blocking workflow**

```yaml
name: Race

on:
  workflow_dispatch:
  schedule:
    - cron: "0 1 * * *"

permissions:
  contents: read

concurrency:
  group: race-${{ github.ref }}
  cancel-in-progress: true

env:
  GO_VERSION: "1.26.5"

jobs:
  linux-race:
    name: race detector (self-hosted Linux)
    runs-on: [self-hosted, Linux, X64, venom-linux]
    timeout-minutes: 20
    steps:
      - name: Checkout with pinned Bifrost submodule
        uses: actions/checkout@v4
        with:
          submodules: recursive

      - name: Verify CI workflow contract
        run: node .github/scripts/verify-ci-contract.mjs

      - name: Verify Linux race prerequisites
        shell: bash
        run: |
          set -euo pipefail
          test "$(go version)" = "go version go1.26.5 linux/amd64"
          task --version | grep -F "3.52.0"
          gcc --version | head -n 1

      - name: Run Linux race detector
        run: task test-race

  windows-race:
    name: race detector (self-hosted Windows)
    runs-on: [self-hosted, Windows, X64, venom]
    timeout-minutes: 20
    env:
      CGO_ENABLED: "1"
      CC: 'C:\ProgramData\VenomCI\mingw64\bin\gcc.exe'
    steps:
      - name: Checkout with pinned Bifrost submodule
        uses: actions/checkout@v4
        with:
          submodules: recursive

      - name: Set up pinned Go without Actions cache
        uses: actions/setup-go@v5
        with:
          go-version: ${{ env.GO_VERSION }}
          cache: false

      - name: Verify Windows race prerequisites
        shell: powershell
        run: |
          if ((go version) -ne 'go version go1.26.5 windows/amd64') { throw "Unexpected Go: $(go version)" }
          & $env:CC --version
          & 'C:\ProgramData\VenomCI\bin\task.exe' --version

      - name: Run Windows race detector
        shell: powershell
        run: '& ''C:\ProgramData\VenomCI\bin\task.exe'' test-race'
```

- [ ] **Step 5: Align the authoritative gate description with the new routing**

In `Taskfile.yml`, replace only the `gate.desc` sentences that currently say CI invokes `task gate` on both Windows and Linux. Preserve every command under `gate.cmds` and use this exact description text:

```yaml
  gate:
    desc: >
      The single blocking static-invariants gate (P0-TEST-001): gofmt,
      goimports, go vet, golangci-lint (incl. the no-panic/no-fmt.Print*/
      no-os.Getenv-outside-config-or-platform forbidigo rules), the
      import-layering test, the no-slug-switch check, and the P1-SEC-006
      secret canary (the latter three run as part of `go test ./...`,
      since they are regular Go tests — under internal/staticgate,
      internal/execution, and internal/secrets [package secrets_test]
      respectively). CI invokes exactly this one command on the genuine
      Linux self-hosted gate; a separate Windows smoke job runs
      `CGO_ENABLED=0 go test ./...`. No gate logic is duplicated in the CI
      workflow file. The race detector (CGO_ENABLED=1) remains separate
      and non-blocking because it needs a C toolchain. schema-lint and
      no-hardcoding remain reserved placeholders (see the `schema-lint`
      and `no-hardcoding-lint` tasks below) — not part of this gate until
      populated in P3.
```

Expected: only descriptive text changes; `gate.cmds` and every other task remain byte-identical.

- [ ] **Step 6: Run the contract test and verify the green state**

```powershell
node .github/scripts/verify-ci-contract.mjs
```

Expected: `CI contract: zero-hosted fast path and non-blocking race verified` and exit zero.

- [ ] **Step 7: Confirm the workflow diff is limited to intended files**

```powershell
git status --short
git diff --check
git diff -- .github/scripts/verify-ci-contract.mjs .github/workflows/ci.yml .github/workflows/race.yml Taskfile.yml
git status --short -- Design_System
```

Expected: only the three declared `.github/` files and `Taskfile.yml` description changed; no `Design_System/` change.

---

### Task 7: Validate workflow syntax and command contracts locally

**Files:**
- Test: `.github/scripts/verify-ci-contract.mjs`
- Test: `.github/workflows/ci.yml`
- Test: `.github/workflows/race.yml`
- Verify: Go and dashboard command surfaces

**Interfaces:**
- Consumes: the workflow files from Task 6 and both online self-hosted runners.
- Produces: a locally validated workflow commit ready for one controlled pull-request run.

- [ ] **Step 1: Run the dependency-free contract test**

```powershell
node .github/scripts/verify-ci-contract.mjs
```

Expected: PASS.

- [ ] **Step 2: Run pinned Actionlint against both workflows**

```powershell
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 .github/workflows/ci.yml .github/workflows/race.yml
```

Expected: no syntax, expression, shell, or workflow-structure diagnostics.

- [ ] **Step 3: Run the Windows smoke command locally**

```powershell
$env:CGO_ENABLED = '0'
try { go test ./... } finally { Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue }
```

Expected: all Go tests pass without GCC.

- [ ] **Step 4: Verify the Linux preflight surface from a fresh WSL process**

```powershell
wsl --shutdown
Start-ScheduledTask -TaskName 'Venom-CI-WSL-Runner'
wsl -d Ubuntu-24.04 -- bash -lc 'set -euo pipefail; go version; node --version; task --version; golangci-lint --version; command -v goimports; git --version; gcc --version | head -n 1'
```

Expected: all required commands resolve after a cold WSL start.

- [ ] **Step 5: Re-run repository hygiene checks**

```powershell
git diff --check
npm --prefix dashboard run check:ds-adherence
if (git status --short -- Design_System) { throw 'Design_System drift detected' }
```

Expected: all checks pass.

- [ ] **Step 6: Commit the workflow change**

```powershell
git add -- .github/scripts/verify-ci-contract.mjs .github/workflows/ci.yml .github/workflows/race.yml Taskfile.yml
git commit -m "ci: add zero-cost Linux and Windows fast path"
```

Expected: one focused commit containing exactly the three `.github/` files and the `Taskfile.yml` description change.

---

### Task 8: Prove the fast path in a controlled pull request

**Files:**
- External write: push branch `codex/zero-cost-fast-ci`
- External write: create draft pull request
- Read: GitHub Actions run/job API responses

**Interfaces:**
- Consumes: the committed workflow change and two online/idle runners.
- Produces: a green commissioning run, then live proof of correct OS routing, zero hosted jobs, and a warm second-attempt duration of five minutes or less.

- [ ] **Step 1: Reconfirm both runners are online and idle immediately before push**

```powershell
$runners = (gh api repos/VENOMDRMSUPPORT/venom-router/actions/runners | ConvertFrom-Json).runners
$required = @('venom-linux-selfhosted','venom-win-selfhosted')
foreach ($name in $required) {
  $runner = $runners | Where-Object name -eq $name
  if ($runner.status -ne 'online' -or $runner.busy) { throw "$name is not online and idle" }
}
```

Expected: both runners ready. If not, repair runner availability before pushing; do not loosen `runs-on` labels.

- [ ] **Step 2: Push only the implementation branch**

```powershell
git push --set-upstream origin codex/zero-cost-fast-ci
```

Expected: branch push succeeds; no direct push to `main` occurs.

- [ ] **Step 3: Create a draft pull request**

```powershell
$prUrl = gh pr create --draft --base main --head codex/zero-cost-fast-ci --title 'ci: zero-cost fast Linux and Windows verification' --body 'Moves blocking CI to a free WSL Linux gate plus parallel Windows smoke. Race detection becomes nightly/manual and remains fully self-hosted.'
$prUrl
```

Expected: one draft PR URL. The pull request triggers only `CI`; `Race` is not triggered by pull requests.

- [ ] **Step 4: Wait only for the bounded commissioning run**

```powershell
$deadline = (Get-Date).AddMinutes(9)
do {
  Start-Sleep -Seconds 10
  $run = gh run list --workflow CI --branch codex/zero-cost-fast-ci --limit 1 --json databaseId,status,conclusion,createdAt,updatedAt,url | ConvertFrom-Json | Select-Object -First 1
} until (($run.status -eq 'completed') -or (Get-Date) -ge $deadline)
if ($run.status -ne 'completed') { throw 'Commissioning CI did not complete within the bounded observation window' }
$run
```

Expected: the first, potentially cold-cache run completes; no indefinite watcher is left running.

- [ ] **Step 5: Verify commissioning routing and conclusions from the Jobs API**

```powershell
$repo = 'VENOMDRMSUPPORT/venom-router'
$jobs = (gh api "repos/$repo/actions/runs/$($run.databaseId)/jobs" | ConvertFrom-Json).jobs
$jobs | Select-Object name,status,conclusion,runner_name,runner_group_name,started_at,completed_at | Format-Table -AutoSize

$linux = $jobs | Where-Object name -eq 'gate (self-hosted Linux)'
$windows = $jobs | Where-Object name -eq 'windows smoke (self-hosted)'
if ($linux.runner_name -ne 'venom-linux-selfhosted') { throw 'Linux gate routed to the wrong runner' }
if ($windows.runner_name -ne 'venom-win-selfhosted') { throw 'Windows smoke routed to the wrong runner' }
if ($linux.conclusion -ne 'success' -or $windows.conclusion -ne 'success') { throw 'Fast CI is not green' }
```

Expected: correct runner names and both jobs green. This first run primes repository, Go, and npm caches and is not used for the warm five-minute acceptance threshold.

- [ ] **Step 6: Rerun the same commit and enforce the warm five-minute target**

```powershell
gh run rerun $run.databaseId
$deadline = (Get-Date).AddMinutes(9)
do {
  Start-Sleep -Seconds 10
  $attempt = gh api "repos/$repo/actions/runs/$($run.databaseId)" | ConvertFrom-Json
} until (($attempt.run_attempt -ge 2 -and $attempt.status -eq 'completed') -or (Get-Date) -ge $deadline)
if ($attempt.run_attempt -lt 2 -or $attempt.status -ne 'completed') { throw 'Warm CI attempt did not complete within the bounded observation window' }
if ($attempt.conclusion -ne 'success') { throw "Warm CI attempt failed: $($attempt.html_url)" }

$warm = gh api "repos/$repo/actions/runs/$($run.databaseId)/attempts/2" | ConvertFrom-Json
$warmJobs = (gh api "repos/$repo/actions/runs/$($run.databaseId)/attempts/2/jobs" | ConvertFrom-Json).jobs
$warmJobs | Select-Object name,conclusion,runner_name,started_at,completed_at | Format-Table -AutoSize
$elapsed = [datetime]$warm.updated_at - [datetime]$warm.run_started_at
if ($elapsed.TotalMinutes -gt 5) { throw "Warm fast CI exceeded 5 minutes: $elapsed" }
```

Expected: attempt 2 is green and completes in five minutes or less. If it exceeds five minutes, inspect the attempt-2 step durations and change only the single measured bottleneck before repeating the PR run.

- [ ] **Step 7: Confirm the PR did not trigger race detection or billed runners**

```powershell
$unexpectedRace = gh run list --limit 30 --json workflowName,headBranch,event,status,url | ConvertFrom-Json | Where-Object {
  $_.workflowName -eq 'Race' -and $_.headBranch -eq 'codex/zero-cost-fast-ci' -and $_.event -eq 'pull_request'
}
if ($unexpectedRace) { throw 'Race workflow incorrectly triggered for the pull request' }
$hosted = $warmJobs | Where-Object { ($_.labels -join ',') -match '(ubuntu|windows|macos)-latest' }
if ($hosted) { throw 'Hosted runner label detected' }
```

Expected: no PR-triggered Race run and no hosted label.

---

### Task 9: Merge, validate race once, and close the rollout

**Files:**
- External write: mark the PR ready and merge after the fast checks pass
- Verify: one manual `Race` run on `main`
- Verify: scheduled-task cold start and protected-file cleanliness

**Interfaces:**
- Consumes: a green draft PR from Task 8.
- Produces: merged zero-cost CI, one-time proof that both non-blocking race jobs work, and a clean final host/repository state.

- [ ] **Step 1: Mark the PR ready and merge with a merge commit**

```powershell
gh pr ready $prUrl
gh pr merge $prUrl --merge --delete-branch
```

Expected: the workflow commit and design history remain individually visible on `main`; the remote feature branch is deleted after merge.

- [ ] **Step 2: Synchronize the local main checkout without destructive reset**

```powershell
git -C 'C:\Users\hamee\Desktop\venom-router' pull --ff-only origin main
```

Expected: the original checkout's `main` fast-forwards cleanly while the implementation worktree remains on its feature branch. If fast-forward is impossible, stop and inspect; do not use `git reset --hard`.

- [ ] **Step 3: Dispatch one non-blocking race validation from default branch**

```powershell
gh workflow run Race --ref main
$raceRun = $null
$deadline = (Get-Date).AddMinutes(2)
do {
  Start-Sleep -Seconds 5
  $raceRun = gh run list --workflow Race --branch main --event workflow_dispatch --limit 1 --json databaseId,status,conclusion,url,createdAt,updatedAt | ConvertFrom-Json | Select-Object -First 1
} until ($raceRun -or (Get-Date) -ge $deadline)
if (-not $raceRun) { throw 'Manual Race run was not created' }
$raceRun
```

Expected: a manual Race run exists. It is observational and does not block the already-proven fast path.

- [ ] **Step 4: Observe the one-time race run with the declared 20-minute bound**

```powershell
$deadline = (Get-Date).AddMinutes(21)
do {
  Start-Sleep -Seconds 15
  $raceRun = gh run view $raceRun.databaseId --json databaseId,status,conclusion,url,createdAt,updatedAt | ConvertFrom-Json
} until (($raceRun.status -eq 'completed') -or (Get-Date) -ge $deadline)
if ($raceRun.status -ne 'completed') { throw 'Race validation exceeded its declared bound' }
if ($raceRun.conclusion -ne 'success') { throw "Race validation failed: $($raceRun.url)" }
```

Expected: both race jobs pass once. Future race runs remain nightly/manual and non-blocking.

- [ ] **Step 5: Repeat cold-start recovery after the merged workflow is live**

```powershell
wsl --shutdown
Start-ScheduledTask -TaskName 'Venom-CI-WSL-Runner'
$deadline = (Get-Date).AddSeconds(90)
do {
  Start-Sleep -Seconds 3
  $linux = (gh api repos/VENOMDRMSUPPORT/venom-router/actions/runners | ConvertFrom-Json).runners | Where-Object name -eq 'venom-linux-selfhosted'
} until (($linux.status -eq 'online' -and -not $linux.busy) -or (Get-Date) -ge $deadline)
if ($linux.status -ne 'online' -or $linux.busy) { throw 'Merged WSL runner did not recover after cold start' }
```

Expected: Linux runner returns online and idle.

- [ ] **Step 6: Run final repository and cost-boundary checks**

```powershell
node .github/scripts/verify-ci-contract.mjs
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 .github/workflows/ci.yml .github/workflows/race.yml
npm --prefix dashboard run check:ds-adherence
git diff --check
git status --short
gh api repos/VENOMDRMSUPPORT/venom-router/actions/runners | ConvertFrom-Json | Select-Object -ExpandProperty runners | Select-Object name,os,status,busy
```

Expected: contract and syntax checks pass, `Design_System/` remains clean, worktree is clean, both primary runners are online/idle, and the second Windows runner remains offline.

- [ ] **Step 7: Remove temporary execution artifacts**

Remove only temporary archives or scripts created outside the repository after resolving their absolute paths. Preserve Ubuntu, `/opt/actions-runner-venom`, `C:\ProgramData\VenomCI`, the scheduled task, both existing Windows runner directories, and every repository file.

Expected: no registration-token file, download archive, scratch script, or background watcher remains.

## Rollback Procedure

Use this only if the merged fast workflow cannot be repaired with one focused change:

1. Revert the merge commit through a new PR; do not reset `main`.
2. Verify the restored `.github/workflows/ci.yml` routes to the existing Windows runner.
3. Stop and disable the WSL runner service with `wsl -d Ubuntu-24.04 -u root -- bash -lc 'cd /opt/actions-runner-venom && ./svc.sh stop && ./svc.sh uninstall'`.
4. Disable the scheduled task with `Disable-ScheduledTask -TaskName 'Venom-CI-WSL-Runner'`.
5. Preserve the Ubuntu distribution and `C:\ProgramData\VenomCI`; deletion requires separate owner approval.
6. Keep `venom-win-selfhosted` running and `venom-win-selfhosted-2` stopped.

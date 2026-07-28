# Zero-Cost Fast CI Architecture Design

**Status:** Approved direction. The owner requires a completely free GitHub Actions design and requested the implementation plan after selecting the WSL-based approach.

**Date:** 2026-07-28

## Objective

Replace the current all-Windows, single-runner CI layout with a zero-billed, two-OS self-hosted design that gives normal push and pull-request feedback within five minutes on a warm, online host while preserving Linux and Windows coverage.

## Current Evidence

- The repository is on `main` at `75ac410`, tracking `origin/main` with a clean worktree.
- The current workflow sends `gate`, `race`, and `dashboard` to the same `[self-hosted, venom]` Windows runner, so jobs described as parallel are actually serialized.
- Run `30397485450` took 8m57s. Its race job spent 4m33s in checkout, then failed before running tests because `winget` was unavailable to the `NETWORK SERVICE` runner account.
- Run `30379063991` waited for almost four hours before its jobs started; the jobs themselves completed in under four minutes. Queue starvation, not the test suite size, caused the extreme delay.
- The Windows service runner is online as `venom-win-selfhosted`; the second Windows runner is stopped and remains out of scope.
- WSL 2.7.10 is installed and Hyper-V is active. The host has 47.8 GB RAM and 16 logical processors. `.wslconfig` already caps WSL at 16 GB RAM, 8 processors, and 4 GB swap.
- No general-purpose Linux distribution is installed yet; only the stopped `docker-desktop` distribution exists.

## Global Constraints

- GitHub-hosted runners must not be used. Every workflow job must target a self-hosted runner.
- Do not modify, rebuild outside its existing validation flow, copy, or vendor content from `Design_System/`.
- Preserve the pinned toolchain: Go `1.26.5`, golangci-lint `v2.12.2`, goimports `v0.48.0`, and Task `3.52.0`.
- `task gate` remains the single authoritative static gate; gate logic must not be duplicated in workflow YAML.
- Do not install or repair host tools from inside a CI job. CI may validate exact versions and fail fast, but provisioning is a separate host step.
- Do not run the race detector in the blocking push/pull-request workflow.
- Keep the stopped second Windows runner stopped; it is not part of this design.
- No destructive WSL operation such as `wsl --unregister` is permitted.
- Registration tokens must remain ephemeral and must never be written to the repository, plan artifacts, shell history, or CI logs.

## Alternatives Considered

### 1. WSL Linux runner plus the existing Windows runner — selected

Ubuntu 24.04 runs a dedicated repository-level GitHub Actions runner for the Linux gate and dashboard verification. The existing Windows service runner runs only a fast Windows build/test smoke job. The two jobs execute in parallel on separate OS environments while sharing the same physical host.

This is the only zero-billed option that restores genuine Linux execution without depending on Docker Desktop. It isolates Linux tooling from Windows service-account PATH and package-manager problems.

### 2. Windows-only consolidated workflow — rejected

Combining all quick checks into one Windows job would be the smallest change, but it retains the known Linux coverage gap and the fragile Windows service environment. It cannot meet the reliability requirement.

### 3. Docker-based Linux runner — rejected for this rollout

Docker isolation could work later, but the Docker daemon is currently stopped and has previously required WSL/Docker recovery. Making Docker a prerequisite would add another failure layer without improving the first rollout.

## Architecture

### Linux verification runner

- Distribution: Ubuntu 24.04 on WSL 2.
- Runner name: `venom-linux-selfhosted`.
- Labels: default `self-hosted`, `Linux`, `X64`, plus custom `venom-linux`.
- Runner directory: `/opt/actions-runner-venom`.
- Work directory: `_work` beneath the runner directory.
- Service management: GitHub runner `svc.sh` under systemd.
- Startup recovery: a Windows scheduled task launches the Ubuntu distribution at user logon and starts/verifies the runner service. The task must run hidden and must not depend on Docker Desktop.
- Tooling is provisioned once and verified before runner registration: Git, GCC/build-essential, Go 1.26.5, Node 20, Task 3.52.0, golangci-lint v2.12.2, and goimports v0.48.0.

### Windows smoke runner

- Reuse `venom-win-selfhosted` with labels `[self-hosted, Windows, X64, venom]`.
- Keep the Windows service under `NT AUTHORITY\NETWORK SERVICE` for this rollout.
- The blocking job performs checkout, Go setup with Actions cache disabled, and `go test ./...` with production-compatible `CGO_ENABLED=0`.
- The blocking Windows job does not invoke Task, Node, GCC, winget, Chocolatey, or the race detector.

### Fast workflow

`.github/workflows/ci.yml` remains the blocking workflow and preserves the existing `CI` workflow identity.

It contains two jobs with no dependency edge so they run concurrently:

1. `gate` on `[self-hosted, Linux, X64, venom-linux]`
   - Checkout with recursive submodules once.
   - Fail-fast tool version preflight.
   - Run `task gate`.
   - Run the existing dashboard adherence, dependency installation, build, validation, lint, test, embed, and targeted Go embed-test sequence.
   - Disable GitHub Actions caches; the persistent WSL user caches are reused locally.
   - Hard timeout: 8 minutes.

2. `windows-smoke` on `[self-hosted, Windows, X64, venom]`
   - Checkout with recursive submodules.
   - Set up pinned Go with Actions cache disabled.
   - Run `go test ./...` with `CGO_ENABLED=0`.
   - Hard timeout: 6 minutes.

The workflow retains markdown-only `paths-ignore` rules and `cancel-in-progress: true`. A newer push cancels stale work for the same ref.

### Non-blocking race workflow

`.github/workflows/race.yml` is a separate workflow triggered by `workflow_dispatch` and a nightly UTC schedule.

- Linux race targets `[self-hosted, Linux, X64, venom-linux]` and runs `task test-race`.
- Windows race targets `[self-hosted, Windows, X64, venom]`, uses an explicitly provisioned machine-readable GCC path, and runs `task test-race`.
- Both jobs have a 20-minute timeout and may run in parallel.
- Race checks are not required for pull-request merge and do not block the fast workflow.
- If the host is offline at schedule time, the result is operationally missed/queued work, not a fast-CI failure. Manual dispatch remains available.

## Data and Control Flow

1. A push or pull request triggers `CI`.
2. GitHub routes the Linux gate to `venom-linux-selfhosted` and Windows smoke to `venom-win-selfhosted` using OS and custom labels.
3. Both runners checkout independent work directories and execute concurrently.
4. The overall blocking result is green only when both quick jobs pass.
5. Nightly/manual race runs are recorded separately and never hold a developer push open.

## Failure Handling

- Missing or wrong Linux tool versions fail during a preflight step before package installation or tests.
- Windows smoke never attempts self-repair; a missing Go/Git prerequisite fails immediately with the resolved executable paths in the log.
- Each blocking job has a hard timeout, preventing another multi-hour in-progress job.
- `cancel-in-progress` removes obsolete runs after newer pushes.
- Runner health verification checks Windows service state, WSL distribution state, GitHub runner online/busy status, and the newest runner diagnostic log.
- The scheduled startup task restores the WSL runner after Windows sign-in and verifies that GitHub reports it online.

## Security and Isolation

- The Linux runner is repository-scoped to `VENOMDRMSUPPORT/venom-router`.
- The private repository is the only allowed workload for both runners.
- No repository workflow receives administrator or sudo credentials.
- Runner registration uses a one-hour GitHub registration token held only in process memory during setup.
- The Linux checkout stays inside the WSL ext4 filesystem, not `/mnt/c`, avoiding Windows filesystem performance and permission coupling.

## Rollout Sequence

1. Capture the current runner/workflow baseline and confirm no jobs are running.
2. Install Ubuntu 24.04 without changing or unregistering `docker-desktop`.
3. Enable and verify systemd, provision the pinned Linux toolchain, and run local preflight commands.
4. Register `venom-linux-selfhosted`, install its service, and verify it online and idle through the GitHub API.
5. Configure and verify Windows logon startup recovery for the WSL service.
6. Provision a fixed Windows GCC path for the non-blocking nightly race only.
7. Rewrite the fast workflow and add the separate race workflow.
8. Validate YAML and command contracts locally without triggering Actions.
9. Push one controlled workflow change and verify the live fast run, job routing, elapsed time, and race workflow availability.
10. Update branch protection only if it currently requires the old race check.

## Rollback

- Revert only the workflow commit to restore the previous CI routing.
- Stop and disable the WSL runner service, but do not unregister or delete the Ubuntu distribution without separate owner approval.
- The existing Windows runner remains installed and running throughout rollout, so rollback does not require runner reconstruction.
- The stopped second Windows runner remains untouched.

## Acceptance Criteria

- GitHub shows both `venom-linux-selfhosted` and `venom-win-selfhosted` online and idle before the workflow change is pushed.
- Every job in `ci.yml` and `race.yml` uses self-hosted labels; no `ubuntu-latest`, `windows-latest`, or other GitHub-hosted label remains.
- A controlled code-changing push completes the warm fast workflow in 5 minutes or less while both runners are online and idle.
- `gate` runs on genuine Linux and includes the existing dashboard validation pipeline.
- Windows smoke completes without Task, GCC, winget, Chocolatey, or race setup.
- A deliberately unavailable prerequisite fails in the preflight stage within 30 seconds; it does not start an installer.
- The race workflow is manually dispatchable, scheduled, bounded by 20-minute job timeouts, and not required for merging.
- A fresh Windows sign-in brings the WSL runner online without opening a visible console.
- GitHub Actions billed minutes remain zero because all jobs use self-hosted runners.
- `Design_System/` remains byte-clean before and after rollout.

## Authoritative References

- Microsoft WSL installation: <https://learn.microsoft.com/en-us/windows/wsl/install>
- Microsoft systemd on WSL: <https://learn.microsoft.com/en-us/windows/wsl/systemd>
- GitHub adding self-hosted runners: <https://docs.github.com/en/actions/how-tos/manage-runners/self-hosted-runners/add-runners>
- GitHub self-hosted runner services and management: <https://docs.github.com/en/actions/how-tos/manage-runners/self-hosted-runners>
- GitHub self-hosted runner labels: <https://docs.github.com/en/actions/how-tos/manage-runners/self-hosted-runners/use-in-a-workflow>

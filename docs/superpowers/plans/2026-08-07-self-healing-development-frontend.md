# Self-Healing Development Frontend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Start Development repair an incomplete dashboard dependency installation safely and expose the exact frontend failure through the control window and a dedicated log.

**Architecture:** A dependency-free Node bootstrap validates repository prerequisites, a lockfile-hash install stamp, and the Vite executable; it safely unlinks the local Design System junction before a deterministic repair and then launches the existing Vite command. The Go supervisor captures bounded child output in a dedicated development log and carries a concise error detail into the authenticated control window.

**Tech Stack:** Go 1.24, Node.js ESM and built-in `node:test`, npm, Windows Job Objects, embedded HTML/JavaScript.

## Global Constraints

- Stay on `main`; create no branch.
- Keep development backend and API on `127.0.0.1:8081` with one canonical database and no `VENOM_DATA_DIR` override.
- Keep Vite on `127.0.0.1:8088` with `--strictPort` and proxy `/api` to 8081.
- Do not edit, rebuild, or copy `Design_System`; the bootstrap may only inspect its package marker and unlink a junction located under `dashboard/node_modules`.
- Never publish unexplained `Design_System` deletions.
- Use deterministic `npm ci --prefer-offline --no-audit --no-fund` only when validation fails, never unconditionally.
- Push only verified changes to `main` after an explicit scope review.

---

### Task 1: Dependency-safe frontend bootstrap

**Files:**
- Create: `dashboard/scripts/dev-bootstrap.mjs`
- Create: `dashboard/scripts/dev-bootstrap.test.mjs`
- Modify: `internal/tray/devsupervisor.go`
- Test: `internal/tray/devsupervisor_test.go`

**Interfaces:**
- Produces: `inspectInstall({ dashboardRoot, platform }) -> { valid, reason, lockHash, stampPath }`.
- Produces: `prepareDependencies({ dashboardRoot, platform, runInstall }) -> { repaired, lockHash }`.
- Produces: CLI `node scripts/dev-bootstrap.mjs --port 8088 --strictPort --host 127.0.0.1`.
- Consumes: `VENOM_DEV_API_TARGET=http://127.0.0.1:8081` from `ProcessSpec.ExtraEnv`.

- [ ] **Step 1: Write failing Node tests for valid, incomplete, stale, unsafe-link, and install-failure behavior**

```js
test("matching stamp and vite executable skip installation", async () => {
  const fixture = await makeFixture({ vite: true, matchingStamp: true });
  let installs = 0;
  const result = await prepareDependencies({
    dashboardRoot: fixture.dashboard,
    platform: "win32",
    runInstall: async () => { installs += 1; return 0; },
  });
  assert.equal(result.repaired, false);
  assert.equal(installs, 0);
});

test("missing vite performs one deterministic repair", async () => {
  const fixture = await makeFixture({ vite: false });
  const calls = [];
  await prepareDependencies({
    dashboardRoot: fixture.dashboard,
    platform: "win32",
    runInstall: async (args) => {
      calls.push(args);
      await fixture.installVite();
      return 0;
    },
  });
  assert.deepEqual(calls, [["ci", "--prefer-offline", "--no-audit", "--no-fund"]]);
});

test("repair unlinks the dependency junction without touching its target", async () => {
  const fixture = await makeFixture({ vite: false, linkedDesignSystem: true });
  await prepareDependencies({
    dashboardRoot: fixture.dashboard,
    platform: "win32",
    runInstall: async () => { await fixture.installVite(); return 0; },
  });
  assert.equal(await readFile(fixture.designSystemPackage, "utf8"), fixture.originalPackage);
});

test("failed install writes no success stamp", async () => {
  const fixture = await makeFixture({ vite: false });
  await assert.rejects(
    prepareDependencies({ dashboardRoot: fixture.dashboard, platform: "win32", runInstall: async () => 1 }),
    /npm ci failed/,
  );
  assert.equal(existsSync(fixture.stamp), false);
});
```

- [ ] **Step 2: Run the Node tests and verify RED**

Run: `node --test dashboard/scripts/dev-bootstrap.test.mjs`

Expected: FAIL because `dev-bootstrap.mjs` and its exports do not exist.

- [ ] **Step 3: Implement the minimal bootstrap**

```js
export async function prepareDependencies({ dashboardRoot, platform, runInstall }) {
  const state = await inspectInstall({ dashboardRoot, platform });
  if (state.valid) return { repaired: false, lockHash: state.lockHash };
  await unlinkLocalDesignSystemLink(dashboardRoot);
  await requireFile(path.resolve(dashboardRoot, "../Design_System/package.json"));
  const code = await runInstall(["ci", "--prefer-offline", "--no-audit", "--no-fund"]);
  if (code !== 0) throw new Error(`dependency repair failed: npm ci failed with exit code ${code}`);
  const repaired = await inspectInstall({ dashboardRoot, platform, ignoreStamp: true });
  if (!repaired.vitePresent) throw new Error("dependency repair failed: Vite executable is still missing");
  await writeFile(repaired.stampPath, repaired.lockHash + "\n", "utf8");
  return { repaired: true, lockHash: repaired.lockHash };
}
```

The CLI uses `npm.cmd` on Windows and `npm` elsewhere, inherits stdout/stderr,
then launches `npm run dev -- --port 8088 --strictPort --host 127.0.0.1`.

- [ ] **Step 4: Run Node tests and verify GREEN**

Run: `node --test dashboard/scripts/dev-bootstrap.test.mjs`

Expected: all bootstrap tests PASS with no protected target-file changes.

- [ ] **Step 5: Change the supervisor spec test to require the bootstrap command and verify RED**

```go
wantArgs := []string{"scripts/dev-bootstrap.mjs", "--port", "8088", "--strictPort", "--host", "127.0.0.1"}
```

The corresponding command name is `node`, a real executable that does not
need the `cmd /c` wrapper required by `npm.cmd`.

Run: `go test ./internal/tray -run '^TestDevSupervisor_StartSpawnsFrontendWithApprovedSpec$'`

Expected: FAIL because the supervisor still runs `npm run dev`.

- [ ] **Step 6: Point `frontendSpec` at the bootstrap and verify GREEN**

Run: `go test ./internal/tray -run '^TestDevSupervisor_StartSpawnsFrontendWithApprovedSpec$'`

Expected: PASS; API target remains exactly `http://127.0.0.1:8081`.

### Task 2: Bounded child diagnostics and control-window error detail

**Files:**
- Modify: `internal/tray/devsupervisor.go`
- Modify: `internal/tray/devsupervisor_test.go`
- Modify: `internal/tray/devprocess_windows.go`
- Modify: `internal/tray/devprocess_windows_test.go`
- Modify: `internal/cli/cli.go`
- Modify: `internal/tray/controlserver.go`
- Modify: `internal/tray/controlserver_test.go`
- Modify: `internal/tray/traycontrols.go`
- Modify: `internal/tray/controlpage.html`

**Interfaces:**
- Extends: `ProcessSpec.OutputPath string`.
- Extends: `DevSupervisorOptions.LogPath string`.
- Extends: `DevStatusView.FrontendDetail string` and `DevStatusView.LogPath string`.
- Extends: `ControlState.DevError string` and `ControlState.DevLogAvailable bool`.
- Adds: `TrayControls.OpenDevLogs()` and authenticated `POST /dev/logs`.

- [ ] **Step 1: Write failing Windows runner test for log capture and bounded returned detail**

```go
func TestWinRunner_CapturesFailureOutput(t *testing.T) {
    logPath := filepath.Join(t.TempDir(), "development.log")
    h, err := NewProcessRunner().Start(ProcessSpec{
        Name: "cmd", Args: []string{"/c", "echo actionable failure 1>&2 & exit /b 7"},
        Dir: t.TempDir(), OutputPath: logPath,
    })
    if err != nil { t.Fatal(err) }
    err = h.Wait()
    if err == nil || !strings.Contains(err.Error(), "actionable failure") { t.Fatalf("Wait error = %v", err) }
    body, _ := os.ReadFile(logPath)
    if !bytes.Contains(body, []byte("actionable failure")) { t.Fatalf("log = %q", body) }
}
```

- [ ] **Step 2: Run the runner test and verify RED**

Run: `go test ./internal/tray -run '^TestWinRunner_CapturesFailureOutput$'`

Expected: compile failure because `OutputPath` does not exist.

- [ ] **Step 3: Implement append-only output capture with a 4 KiB tail**

Open `OutputPath` with `O_CREATE|O_WRONLY|O_APPEND` and mode `0600`. Route both
stdout and stderr through `io.MultiWriter(file, tail)`. `tailWriter` retains only
the last 4096 bytes. `Wait` closes the file and wraps a non-nil process error
with one sanitized, 240-character output line.

- [ ] **Step 4: Run Windows process tests and verify GREEN**

Run: `go test ./internal/tray -run '^TestWinRunner_'`

Expected: PASS including process-tree termination and captured output.

- [ ] **Step 5: Write failing supervisor/control tests for retained error and dev-log action**

```go
func TestDevSupervisor_UnexpectedFrontendExitRetainsDetail(t *testing.T) {
    r := &fakeRunner{}
    s := newTestSupervisor(t, r, probeAlways(false))
    s.Start()
    r.handle(0).exit(errors.New("exit status 1: Vite executable is still missing"))
    eventually(t, func() bool { return s.Status().Frontend == DevError }, "frontend never errored")
    if got := s.Status().FrontendDetail; !strings.Contains(got, "Vite executable") { t.Fatalf("detail = %q", got) }
}
```

Extend `TestControlServer_PostRoutesDispatch` with
`{"/dev/logs", "OpenDevLogs"}` and extend the state JSON fixture with the exact
error and log-availability values.

- [ ] **Step 6: Run focused supervisor/control tests and verify RED**

Run: `go test ./internal/tray -run 'TestDevSupervisor_UnexpectedFrontendExitRetainsDetail|TestControlServer_(PostRoutesDispatch|StateReturnsJSON)'`

Expected: compile/test failures for the missing fields and route.

- [ ] **Step 7: Implement status propagation and the authenticated log action**

Pass `filepath.Join(filepath.Dir(logPath), "development.log")` from `cli.go` to
the supervisor. Store start/wait errors on the component, clear them on a fresh
Start/Stop, expose them through `ControlState`, render the bounded text below
the component states, and show `View Development Log` only when its path is
available.

- [ ] **Step 8: Run focused tray tests and verify GREEN**

Run: `go test ./internal/tray`

Expected: PASS with existing lifecycle, generation, and enablement behavior unchanged.

### Task 3: Repair, bundle, live verification, and publish

**Files:**
- Modify through generated output: `dashboard/node_modules` and ignored build artifacts only.
- Build output: `dist/venom.exe` (ignored).
- Embedded assets: `internal/httpui/dist/*` as produced by the existing bundle task.
- Documentation: `docs/tray-dev-environment.md`.

**Interfaces:**
- Consumes: completed bootstrap and diagnostics from Tasks 1-2.
- Produces: verified `dist/venom.exe`, live 8081/8088 listeners, and a scoped `main` commit.

- [ ] **Step 1: Prove current failure before repair**

Run: `cmd /c npm run dev -- --port 8088 --strictPort --host 127.0.0.1` in `dashboard`.

Expected: FAIL with `vite is not recognized` before dependency repair.

- [ ] **Step 2: Run the bootstrap once and verify repair path**

Run: `node scripts/dev-bootstrap.mjs --port 8088 --strictPort --host 127.0.0.1` in `dashboard`, with a bounded observation window.

Expected: logs dependency repair, creates Vite executable and matching stamp,
then listens on 8088. Confirm `git status --short -- Design_System` remains empty.

- [ ] **Step 3: Stop the bounded manual process, run again, and verify fast path**

Expected: second start reports dependencies valid and does not invoke `npm ci`.

- [ ] **Step 4: Run complete automated verification**

Run:

```text
node --test dashboard/scripts/dev-bootstrap.test.mjs
go test ./internal/tray
go test ./...
npm --prefix dashboard run typecheck
npm --prefix dashboard test
```

Expected: all commands exit 0 with zero test failures.

- [ ] **Step 5: Update the development runbook**

Document automatic repair, the install stamp, the dedicated development log,
the protected local-package junction rule, and the exact recovery behavior.

- [ ] **Step 6: Rebuild the shipped executable**

Run: `task bundle`

Expected: Design System build, dashboard build, embedded-asset copy, GUI-subsystem
verification, and `dist/venom.exe` creation all exit 0.

- [ ] **Step 7: Start the rebuilt executable and verify the actual UI**

Stop only the currently running old `dist/venom.exe`, start the rebuilt one,
use its control window to Start Development, and verify:

```text
GET http://127.0.0.1:8081/health -> HTTP response
GET http://127.0.0.1:8088/ -> Vite dashboard HTML
GET http://127.0.0.1:8088/api/... -> shared 8081 backend response
```

Verify the control window shows both components Running, then Stop/Start again
and verify the fast path plus no new frontend error.

- [ ] **Step 8: Review Git scope, commit, and push main**

Run `git status --short`, `git diff --check`, and inspect every diff. Confirm
`Design_System` has no changes and no unrelated file is staged. Commit only the
spec, plan, bootstrap, tests, tray changes, and runbook with:

```text
fix(tray): make development frontend startup self-healing
```

Push the current `main` to `origin/main`, then verify local `HEAD` equals
`origin/main`.

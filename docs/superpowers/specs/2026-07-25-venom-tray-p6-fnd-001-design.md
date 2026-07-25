# Design — System Tray (P6-FND-001), pulled forward

- **Date:** 2026-07-25
- **Roadmap task:** `P6-FND-001 — System tray` (see `docs/11-implementation-plan.md:2355`)
- **Status:** design, pending owner review of this spec
- **Governance:** this document is a governor deliverable (spec). Implementation
  is handed to a separate implementer per the project's no-self-implement rule.
  This spec does not update the roadmap tracker; STATUS moves only after a
  verified implementation approval.
- **Revision r2 (2026-07-25):** three correctness fixes after review. (a)
  `fyne.io/systray.Run` returns no error (confirmed via library docs), so the
  fallback is restructured to **boot-first / tray-additive** with an `onReady`
  provable signal instead of a caught error or a loop-stopping timeout. (b) the
  console window is hidden only when `GetConsoleProcessList` proves sole
  ownership, never blindly. (c) a single `sync.Once`-guarded bounded shutdown,
  entered **first** on `ctx.Done()`, then `systray.Quit()`. See §4.5.
- **Revision r3 (2026-07-25):** boot-first kept the server alive but did not by
  itself guarantee process *exit* if `systray.Run` hangs before creating its
  window (`Quit` cannot release a windowless loop). Added a **spike-proven
  bounded-exit backstop**: after graceful `Controller.Quit`, attempt
  `systray.Quit` + `PostThreadMessageW(WM_QUIT)`, then `os.Exit(0)` after a
  margin. Evidence in Appendix A.
- **Revision r4 (2026-07-25):** r3's backstop fired *after* `Controller.Quit`
  returned — but `Server.Shutdown` (`boot.go:86`) is unbounded past
  `http.Shutdown`, so a hung `db.Close`/`lock.Release` meant `os.Exit` was never
  reached. Fixed by **arming the absolute watchdog first, before any cleanup**
  (§4.5), with a non-zero exit code when shutdown does not complete in time.
  Proven by deterministic child-process tests that hang shutdown and the UI on
  purpose (Appendix A.2) — the r3 instant-shutdown spike could not exercise this.
- **Revision r5 (2026-07-25):** folded the owner's three start-conditions (§11):
  port the A.2 tests into the repo (DoD); Restart skips the new Boot if its
  pre-Boot Shutdown timed out/errored (§4.2); the log is append-only until
  ownership can be proven without a core change (§4.3).

## 1. Context and problem

Today the control plane + embedded dashboard are reachable at
`http://127.0.0.1:8081/` **only while a `venom` process is actively running**.
The two ways to get there both hurt for a desktop owner:

- `venom serve` is a foreground process that holds the terminal
  (`internal/cli/cli.go:154` prints `serving on ... (waiting for shutdown
  signal)`); closing the terminal or Ctrl+C kills port 8081.
- There is no built, double-clickable entry point; standing the server up means
  building the design system + dashboard, embedding, `go build`, then keeping a
  terminal open.

The approved architecture already anticipates the fix as **tray mode**:

- `docs/01-architecture.md:39` — **"One process. One node. One SQLite writer.
  One owner."** No separate gateway process.
- `docs/01-architecture.md:46-53` — a **single executable** `cmd/venom` with two
  run modes: bare `venom` → tray mode (starts the server, shows a tray icon,
  hides the console); `venom serve` → headless.
- `docs/11-implementation-plan.md:2355-2364` — `P6-FND-001`, boundaries
  `internal/tray` + `internal/cli`, precondition `P0-FND-004`, failure/rollback
  "tray failure falls back to headless with a clear log".

`P0-FND-004` (CLI dispatch & graceful shutdown, `docs/11-implementation-plan.md:438`)
is **complete** — bare→tray dispatch already exists as a documented stub
(`internal/cli/cli.go:60-68`). So `P6-FND-001`'s only precondition is satisfied
and this task is **dependency-unblocked**; pulling it forward from phase P6 is a
sequencing decision the owner is making. The P6 gate itself (`P6-TEST-002`,
operate-without-terminal) is **not** in scope here — only the standalone unit.

## 2. Decision: single process, rejected alternative

**Adopt the approved single-binary model.** Bare `venom` runs tray mode *and* the
server in the same process; the tray menu operates the in-process server via
direct `app.Boot` / `Server.Shutdown` calls.

A separate supervisor executable (`venomtray.exe` in its own Go module, outside
`task gate`, driving `venom serve` as a child) was considered and **rejected**: it
violates the "one process / one executable" invariant (`01 §1`, `01 §2`) and would
remove the tray from the static-invariants gate. It is also *more* complex, not
less: driving graceful shutdown into a windowless child on Windows requires a
console-control-event dance (`AllocConsole` + `CREATE_NEW_PROCESS_GROUP` +
`GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT)`). In the single-process model that
problem disappears entirely — **Restart = `Shutdown()` then `Boot()` in-process;
Quit = `Shutdown()` then exit.** No child, no signals, no console events.

Adopting a two-process model would require an explicit architecture-doc amendment
first; this spec does not pursue it.

## 3. Scope (owner-approved)

Menu, in order: **Open Dashboard / Status / Restart / View Logs / Quit.**

- The first four minus "View Logs" are exactly the `P6-FND-001` approved set
  (*Open Dashboard / Status / Restart / Quit*).
- **View Logs + log-to-file** is a small, justified addition the owner approved:
  tray mode hides the console, which orphans the structured logs that
  `observability.Default()` writes to `os.Stderr`
  (`internal/observability/logger.go:57-60`). Routing logs to a file is nearly
  free because the seam already exists — `observability.New(handler)` takes any
  `slog.Handler` and `app.BootConfig.Logger` accepts an injected logger
  (`internal/app/boot.go:46-48`).

### Out of scope (v1)

Explicit Start/Stop-as-separate-items; "Start with Windows" toggle; focus-first-
instance IPC (the `app.FocusFirstInstance` stub at `internal/app/lock.go:96-104`
stays a stub); Windows toast notifications; a live log-tailing window; Linux/macOS
tray; anything in the P6 UI or the P6 gate.

## 4. Component design

### 4.1 `internal/cli` — dispatch change

Bare mode (`case "":` in `internal/cli/cli.go:61`) changes from
`runServeLoop(...)` to a new `runTrayLoop(...)`. `venom serve` and every other
mode are **unchanged** (headless keeps logging to stderr and shutting down on
SIGINT/SIGTERM). `runTrayLoop`:

1. Resolves config via `config.Load(nil)` (bind, etc.) — no direct env reads.
2. Builds a **file-backed** logger (§4.3) and a real `ServerLifecycle` (§4.2)
   bound to `app.Boot` with that logger.
3. **Boots the server first** via `ServerLifecycle.Boot` — the headless core is
   now running (dashboard reachable, logs to file) independently of any tray UI.
   A Boot failure is a hard error returned to the caller (process exits non-zero
   with a clear log), exactly like `venom serve`.
4. Calls `tray.RunUI(ctx, controller)` on the **main goroutine**. The tray is
   *additive UI on top of an already-running server*; the exact
   startup/shutdown/fallback control flow is §4.5.

### 4.2 `internal/tray` — testable controller + platform adapter

The package separates lifecycle logic (CI-testable, pure Go, cross-platform) from
the systray UI (Windows-only, manual-evidence tested), matching the repo's
injected-seam / ports-and-adapters discipline.

- **`ServerLifecycle` interface** (port):
  `Boot(ctx) error`, `Shutdown(ctx) error`, `Healthy(ctx) bool`, `DashboardURL() string`.
  Real implementation wraps `app.Boot` / `Server.Shutdown` (bounded by
  `app.ShutdownTimeout`) and a loopback `GET /health`. Tests inject a fake.
- **`Opener` interface** (port): `Open(target string) error` for the dashboard URL
  and the log file. Real impl is a Windows `ShellExecute` adapter
  (`*_windows.go`); tests inject a recording fake.
- **`Controller`** (pure Go): holds the current `*Server` behind a mutex and a
  small state enum (`Stopped` / `Running` / `Error`). Two distinct teardown
  concepts, deliberately not conflated:
  - `Quit()` — the single **`sync.Once`-guarded final teardown** (bounded
    `ServerLifecycle.Shutdown`, then mark done). Every *exit* path funnels
    through it (§4.5). Not re-startable.
  - `Restart()` — a **re-runnable** cycle: `ServerLifecycle.Shutdown`
    (`ShutdownTimeout`) then `ServerLifecycle.Boot`, guarded against concurrent
    invocation. Uses the lifecycle's shutdown, *not* the once-guarded `Quit`, so
    the server can come back up. **If that Shutdown times out or errors, the new
    Boot is skipped entirely** and the controller goes to `Error` — a dirty
    shutdown must never be followed by a Boot that could race a still-held lock
    or a still-open DB. Recovery is a later explicit Restart, whose Boot benefits
    from the lock's stale-lock self-heal (`internal/app/lock.go:38-42`).

  Other methods: `OpenDashboard()`, `OpenLogs()`, `Status() StatusView`. No
  systray import; fully unit-tested in CI on both OSes.
- **`tray_windows.go`** (`//go:build windows`): the only file importing
  `fyne.io/systray`. Calls `systray.Run(onReady, onExit)`; `onReady` builds the
  menu, sets a `ready` flag, and starts a ~2s status ticker (updates the
  `Status` item text + tooltip + icon); each item's click runs its `Controller`
  method in a goroutine so the event loop never blocks; `onExit` calls
  `Controller.Quit`. Hides the console only per the §5 ownership check. Embeds
  the icon(s) via `//go:embed`.
- **`tray_other.go`** (`//go:build !windows`): `RunUI` logs "tray unsupported;
  running headless" and blocks on `ctx.Done()` (the server is already running),
  then returns. Imports no systray — this is the compile-time-provable fallback
  that keeps `CGO_ENABLED=0` builds green on Linux.

### 4.3 Logging to file (tray mode only)

- Path: `<dataDir>/logs/venom.log`, `dataDir` from `platform.EnsureDataDir()`
  (never hardcoded).
- Build `observability.New(slog.NewJSONHandler(f, nil))` over that file and pass
  it as `app.BootConfig.Logger`. Headless mode still uses the stderr default.
- **Append-only, no rotation.** The log file is opened `O_APPEND|O_CREATE` and
  never truncated or renamed. Rationale: the file must be opened *before* `Boot`,
  but single-instance ownership is only proven *inside* `Boot` (the `acquire_lock`
  stage) — rotating/truncating at open could clobber a running instance's log,
  and proving ownership earlier would require a core change (out of scope).
  Append-only sidesteps the ownership question entirely. A future core change
  could expose ownership for size-based rotation; not v1.

### 4.4 Icon

Embed a Venom `.ico` in `internal/tray` (reuse a brand asset from
`Design_System/` if a suitable one exists; otherwise a placeholder). Optionally a
second "stopped/error" icon; if only one is provided, state is conveyed by the
`Status` item text + tooltip.

### 4.5 Startup, shutdown, and fallback control flow

Grounded in the library's actual contract (verified via the systray docs):
`systray.Run(onReady, onExit func())` **returns no error** and blocks until
`Quit()`; `systray.Quit()` is `sync.Once`-guarded and safe from any goroutine.
The design therefore never depends on a `Run` error, nor on a timeout to stop the
message loop.

- **Server independent of the UI.** `Boot` runs before `RunUI` (§4.1). If the
  tray never initializes, the server is still up — that *is* the headless
  fallback, so no caught-error path is required.
- **Provable init signal.** Tray success is observed by `onReady` firing (sets
  `ready`), not by timing anything. Both failure sub-cases are covered: if `Run`
  *returns* without `ready` set, `RunUI` logs and blocks on `ctx.Done()`
  (headless); if `Run` *never returns* (stuck windowless loop), the server still
  runs and the exit backstop below bounds shutdown once `ctx` is canceled.
- **Absolute watchdog FIRST, then best-effort graceful teardown (spike-proven,
  Appendix A.2).** The Quit menu item and `systray` `onExit` simply **cancel the
  root context**, so every shutdown — menu, external SIGINT/SIGTERM, onExit —
  funnels into the single `ctx.Done()` watcher goroutine, which runs, in this
  order:
  1. **Arm the absolute watchdog before any cleanup.** A goroutine sleeps
     `ShutdownTimeout + margin`, then `os.Exit`: code **0** if graceful shutdown
     had completed (`shutdownDone` set), else a **non-zero** code (shutdown
     itself hung). It waits on nothing that can hang; `os.Exit`→`ExitProcess`
     ends even a goroutine stuck in `db.Close()` or a thread stuck in
     `GetMessage`. This is the unconditional guarantee.
  2. **Bounded graceful shutdown** — `Controller.Quit` runs `Server.Shutdown` in
     a goroutine and `select`s it against `ShutdownTimeout`, tolerating a leaked
     goroutine if it hangs. This wrapper is **required** because `Server.Shutdown`
     (`internal/app/boot.go:86-98`) bounds **only** `http.Shutdown(ctx)`; its
     `db.Close()` and `lock.Release()` are **unbounded**. On clean return, set
     `shutdownDone`.
  3. Best-effort loop release: `systray.Quit()`, then `PostThreadMessageW(WM_QUIT)`
     to the captured loop thread id (§5).
  4. Normal exit: `os.Exit(0)` if shutdown was clean, else the non-zero code —
     racing (and almost always beating) the watchdog to the same outcome.

  Worst-case exit ≈ `ShutdownTimeout + margin`, **guaranteed even if graceful
  shutdown never returns**. Making `Server.Shutdown` internally bounded is a
  possible future core change but is **out of P6-FND-001 scope** (boundaries:
  `internal/tray`, `internal/cli`); the tray layer compensates here.

## 5. Windows specifics

- **Console hiding (ownership-checked, never blind):** the binary stays a
  console-subsystem build so `venom serve` keeps a working stdout in a terminal.
  Tray mode must **not** blindly hide `GetConsoleWindow()`: when launched from an
  existing PowerShell/cmd the process *shares* that console, so hiding it would
  hide the user's own terminal window. Instead call `GetConsoleProcessList`; hide
  the window (`ShowWindow(SW_HIDE)` via `golang.org/x/sys/windows`) **only when
  exactly one process is attached** — i.e. Windows gave us a private console, the
  Explorer double-click case. When more than one process shares the console,
  leave it untouched. A brief console flash on double-click is accepted for v1; a
  shortcut can minimize it later.
- **CGO / build isolation (critical for the gate):** `fyne.io/systray` is pure-Go
  on Windows (`golang.org/x/sys/windows`, no cgo) but needs cgo on Linux. Because
  the gate builds `CGO_ENABLED=0` on **both** OSes, systray must never be imported
  on non-Windows. The `//go:build windows` / `//go:build !windows` split
  guarantees `go build/vet/test ./...` on Linux never compiles the systray
  backend, keeping `task gate` green on Linux.
- **Threading & ordering:** `app.Boot` serves HTTP on a background goroutine
  (`internal/app/boot.go:267`) and returns immediately, so `runTrayLoop` boots
  first and then hands the **main goroutine** to `systray.Run` (required — it
  owns the OS message loop). That goroutine must `runtime.LockOSThread()` and
  capture `windows.GetCurrentThreadId()` *before* `Run`, so §4.5's
  `PostThreadMessageW(WM_QUIT)` targets the loop thread (spike: `ret=1`, `Run`
  released). Shutdown routing on `ctx.Done()` is per §4.5.

## 6. Behavior table

| Menu item | Action |
|---|---|
| `● Status` (disabled) | Live text: `Running — 127.0.0.1:8081` / `Stopped` / `Error (see Logs)`, refreshed ~2s; drives icon/tooltip. |
| Open Dashboard | `Opener.Open("http://<bind>/")` via ShellExecute. |
| Restart | `Controller.Restart()` = re-runnable `ServerLifecycle.Shutdown(ShutdownTimeout)` then `Boot`; on Boot failure → `Error` state, tray stays alive, logged to file. |
| View Logs | `Opener.Open("<dataDir>/logs/venom.log")` (default editor). |
| Quit | Once-guarded `Controller.Quit` (bounded `ShutdownTimeout`) then `systray.Quit()` (§4.5); process exits 0. |

External SIGINT/SIGTERM (`ctx.Done()`) uses the same once-guarded `Controller.Quit`,
entered **before** `systray.Quit()` (§4.5).

Second-instance behavior: `app.Boot` fails at `acquire_lock` with
`ErrAlreadyRunning`; tray mode logs "already running" and exits. Real
focus-first-instance is out of scope (stub retained).

## 7. Error handling & fallback

- **Fallback is structural, not a caught error** (§4.5): because the server boots
  before and independently of the tray UI, any tray failure — an unsupported
  platform (compile-time `!windows` stub) or a Windows init failure that
  `systray.Run` swallows internally — still leaves a running, dashboard-reachable
  server. Bare `venom` on a headless Linux host thus behaves like `venom serve`.
  A clear log line is written in the Windows `!ready` case. Process exit on `ctx`
  cancel is guaranteed and bounded regardless of tray *or* shutdown state by the
  §4.5 absolute watchdog (spike-proven, Appendix A.2), with a **non-zero exit
  code** if graceful shutdown itself hung.
- Boot failure inside Restart → `Error` state surfaced in `Status`; the tray does
  not crash; the underlying error is in the log file. Stale single-instance locks
  self-heal on the next Boot (`internal/app/lock.go:38-42`), so Restart is
  reliable even after an abnormal prior exit.

## 8. Gate & governance impact

- **New dependency** `fyne.io/systray` (+ its `golang.org/x/sys` usage, already an
  indirect dep) enters the **core** `go.mod` — expected, since `P6-FND-001` scope
  says "pure-Go systray". Compiled on Windows only (§5).
- **forbidigo** (`.golangci.yml`): `internal/tray` and `internal/cli` are inside
  the gate, so no `fmt.Print*`, no `panic`, and no `os.Getenv/LookupEnv` (config
  and data-dir come from `internal/config` / `internal/platform`). `os/exec` and
  `os.OpenFile` are not forbidden; the Windows browser/log open prefers
  `ShellExecute` to avoid a console flash. `os.Exit` is **not** in the forbid
  list (and `cmd/venom/main.go` already uses it) — §4.5's watchdog uses it
  deliberately as the bounded-exit backstop, consistent with `app.Run`'s
  documented "caller force-exits on timeout" contract (`internal/app/app.go:46`).
- **Import layering** (`internal/staticgate`): new edges are `cli → tray → app`
  (cli already depends on app). Verify against the layering test before merge; no
  cycle is introduced.
- **Tests:** `Controller`/`Opener`/`ServerLifecycle` unit tests run in CI on both
  OSes with fakes; the systray adapter is covered by Windows manual-evidence
  recording (matches `P6-FND-001` "Evidence"). **Bounded-exit child-process tests
  (Appendix A.2) are a DoD requirement** — they re-exec the test binary with the
  shutdown/UI deliberately hung and assert exit within `ShutdownTimeout + margin`
  with the correct code; being systray-independent they run in CI on both OSes.
  Existing headless cli/serve tests must remain green (headless unaffected — the
  DoD's second half).

## 9. Supporting convenience (Taskfile only, not code)

Add a `Taskfile.yml` task (e.g. `bundle`) chaining
`dashboard:build-embed` → `go build -o venom.exe ./cmd/venom`, producing a single
double-clickable `venom.exe` with the dashboard embedded. This directly answers
the "commands are complicated" pain. It touches only `Taskfile.yml`, outside the
`P6-FND-001` code boundaries.

## 10. Risks / open questions

1. **Bounded exit resolved and proven (Appendix A).** The absolute watchdog
   (§4.5) guarantees process exit within `ShutdownTimeout + margin` even when
   graceful shutdown *itself* hangs (`db.Close`/`lock.Release` unbounded) — proven
   deterministically by child-process tests (A.2): `hang-shutdown`/`hang-both`
   exit `2` at ~`ShutdownTimeout`, `hang-quit` (even the wrapper broken) exits `2`
   at the watchdog deadline, `normal`/`hang-ui` exit `0` promptly. Loop release
   (`systray.Quit`/`WM_QUIT`) is separately proven (A.1). Honest limitation: the
   A.1 host had a desktop session so the true systray init-*failure* path was not
   reproduced — the watchdog makes the exit guarantee independent of it. Remaining:
   pin the `fyne.io/systray` version, confirm icon format + disabled-item support,
   and **port the A.2 child-process tests into `internal/tray`/`internal/cli`** at
   implementation start.
2. Console-flash acceptability on double-click — accepted for v1; revisit with a
   shortcut if the owner dislikes it.
3. Whether a suitable `.ico` exists in `Design_System/` or a placeholder is
   needed for v1.

## 11. Owner-mandated acceptance conditions (r5)

Binding on the implementation plan and its DoD:

1. **Tests in the repo.** The five Appendix A.2 bounded-exit child-process tests
   are ported into `internal/tray` / `internal/cli` and run under `task gate` on
   both Windows and Linux (they are systray-independent). The spike proof does
   not count as completion until these live in the repo and pass in CI.
2. **Restart never boots after a dirty shutdown.** If the pre-Boot `Shutdown`
   during `Restart` times out or errors, the new `Boot` is skipped and the
   controller enters `Error` (§4.2).
3. **Append-only log.** No rotation/truncation until instance ownership can be
   proven without modifying the core (§4.3).

**Completion gate (before any "done" claim):** `task gate` green on both OSes +
existing headless cli/serve tests green + the Windows tray manual-evidence
recording captured (per `P6-FND-001` "Evidence").

## Appendix A — Windows spike evidence (2026-07-25)

Both spikes are throwaway isolated modules (`go1.26.5`, own `go.mod`, **not**
committed to the repo).

### A.1 — systray loop release (`fyne.io/systray v1.12.2`)

A watcher goroutine simulates a `ctx` cancel at t+3s, attempts a loop release per
mode, then `os.Exit(0)` at a +2s margin. `main` calls `runtime.LockOSThread()`
and captures its thread id via `windows.GetCurrentThreadId()` for
`PostThreadMessageW`.

| Mode | Release attempt | Result | Exit |
|---|---|---|---|
| `quit` | `systray.Quit()` | `onExit` fired, `Run` returned | t+3.01s (clean release) |
| `wmquit` | `PostThreadMessageW(WM_QUIT)` to captured tid | `ret=1`, `Run` returned | t+3.00s (clean release) |
| `both` | `Quit()` then `WM_QUIT` | `Run` returned | t+3.01s |
| `exitguard` | none | loop not released | t+5.00s (`os.Exit` backstop) |

With the tray initialized, both `systray.Quit()` and `PostThreadMessageW(WM_QUIT)`
(to the `LockOSThread`-captured id) release `Run` immediately. Limitation: the
host had a desktop session, so `onReady` always fired; the true init-*failure*
path was not reproduced — A.2's watchdog makes the exit guarantee independent of
it.

### A.2 — bounded exit when shutdown ITSELF hangs (stdlib only, deterministic)

This spike closes r3's gap: it models `Server.Shutdown` genuinely hanging (not an
instant fake) and proves exit via the watchdog-first design of §4.5. **Five
child-process tests** re-exec the test binary (`SPIKE_SHUTDOWN=300ms`,
`SPIKE_MARGIN=200ms`; deadline 500ms) and assert real exit code + wall time. All
pass:

| Test mode | What hangs | Exit code | Elapsed |
|---|---|---|---|
| `normal` | nothing | 0 | ~9 ms (watchdog cancelled — no deadline wait) |
| `hang-ui` | tray UI only (shutdown clean) | 0 | ~7 ms |
| `hang-shutdown` | `db.Close`/`lock.Release` | **2** | ~306 ms (bounded by `ShutdownTimeout`) |
| `hang-both` | shutdown + UI | **2** | ~308 ms |
| `hang-quit` | even `Controller.Quit`'s own bound | **2** | ~508 ms (absolute watchdog) |

Conclusions: (1) exit is bounded under *every* hang, including graceful shutdown
never returning; (2) the exit code is non-zero iff shutdown did not complete in
time; (3) `hang-quit` proves the watchdog is an independent backstop armed
**before** cleanup — not dependent on `Controller.Quit` returning; (4) `normal`
proves the watchdog does not delay a clean exit. These tests are systray-
independent and must be ported into `internal/tray`/`internal/cli` (DoD, §8).

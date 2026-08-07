# Self-Healing Development Frontend Design

## Problem

Starting Development from `dist\venom.exe` starts the watched backend but the
frontend exits immediately. Direct reproduction shows that `npm run dev` fails
with:

```text
'vite' is not recognized as an internal or external command
```

`dashboard\node_modules\vite` exists but is incomplete and
`dashboard\node_modules\.bin\vite.cmd` is absent. The current tray supervisor
assumes an existing dependency directory is usable, starts `npm run dev`
directly, discards child output, and therefore reports only `Frontend: Error`.

The checkout initially also had eleven uncommitted deletions at the root of
`Design_System`, including `package.json`, `package-lock.json`, and the build
configuration. Git history contained no commit or approved task that removed
them, while recent commits still depended on those files. They were therefore
restored before implementation and are excluded from the publish scope.

## Approved Outcome

Starting Development from the tray must recover automatically from a missing,
partial, or stale dashboard dependency installation. If recovery cannot
complete, the application must preserve the exact diagnostic in the Venom log
and present an actionable frontend error instead of an unexplained state.

Development keeps the existing architecture:

- watched source backend on `127.0.0.1:8081`;
- Vite frontend on `127.0.0.1:8088`;
- one canonical database and no `VENOM_DATA_DIR` override;
- frontend `/api` proxy to `http://127.0.0.1:8081`;
- both child trees contained by Windows Job Objects.

## Design

### Managed frontend bootstrap

Add a dependency-free Node bootstrap under `dashboard/scripts`. The tray starts
this script instead of invoking `npm run dev` directly.

The bootstrap checks all of the following before starting Vite:

1. `dashboard/package.json` and `dashboard/package-lock.json` exist.
2. `Design_System/package.json` exists because the dashboard declares the local
   `file:../Design_System` package.
3. `node_modules/.bin/vite.cmd` exists on Windows (`node_modules/.bin/vite` on
   other platforms).
4. An install stamp contains the SHA-256 of the current dashboard lockfile.

If any dependency check fails, the bootstrap runs:

```text
npm ci --prefer-offline --no-audit --no-fund
```

Immediately before that command, the bootstrap inspects
`node_modules/@venom/design-system`. When it is a symbolic link or Windows
junction, it unlinks that directory entry without following it and then proves
that `Design_System/package.json` still exists. This prevents npm's destructive
`node_modules` cleanup from ever traversing the local-package link into the
protected source tree. A real directory at that dependency path is left for
`npm ci` to replace normally.

Only a successful deterministic install writes the lockfile-hash stamp. The
bootstrap validates Vite again after installation, then replaces itself with
the normal Vite development command and forwards termination signals. A valid
installation takes the fast path and performs no network or package mutation.

The bootstrap never builds, edits, copies, or repairs `Design_System`. Missing
protected source files produce a precise prerequisite error. Restoring the
currently deleted protected files requires explicit user authorization.

### Diagnostics

Extend `ProcessSpec` with a component-specific output path. The Windows process
runner appends both stdout and stderr to a development log inside the existing
Venom log directory and closes the file after the child exits. The supervisor
records the same path in its structured unexpected-exit message.

The control view retains the stable component states but adds a concise last
error for a failed component and a `View Development Log` action. It must not
render unbounded command output or expose environment values in the UI.

Expected messages distinguish:

- missing repository prerequisite;
- dependency repair started;
- dependency repair failed;
- Vite executable still missing after repair;
- Vite exited after a successful start;
- port 8088 already occupied.

### Concurrency and lifecycle

Only one bootstrap runs for a supervisor generation. Existing
Start/Stop/Restart generation guards remain authoritative. Stopping Development
closes the Job Object, terminating an in-progress `npm ci` or Vite process tree.
The next Start performs the checks again; a partial install cannot be accepted
because the executable and matching stamp are both required.

Backend startup remains independent so a frontend repair does not create a
second backend or database writer.

## Tests

Use Node's built-in test runner for the dependency-free bootstrap logic and Go
tests for supervisor/process integration.

Required regression cases:

- valid Vite executable plus matching lock hash skips `npm ci`;
- missing executable triggers exactly one deterministic install;
- changed lockfile triggers reinstall even when Vite exists;
- dependency repair unlinks the local Design System junction without deleting
  or changing its target files;
- failed install exits non-zero and does not write a success stamp;
- missing `Design_System/package.json` fails before package installation with
  an actionable error;
- successful repair starts Vite with port 8088, strict-port, loopback host, and
  the production API target;
- supervisor output is appended to the expected development log;
- deliberate Stop remains `Stopped`, while an unexpected bootstrap/Vite exit
  remains `Error` and retains its diagnostic path;
- the backend specification remains the watched source backend on 8081 with no
  `VENOM_DATA_DIR` override.

## Acceptance

1. Restore the unexplained protected-file deletions from the current `HEAD`
   only after explicit user authorization; do not publish the deletions.
2. Repair the current dashboard dependency installation without otherwise
   editing or rebuilding `Design_System`.
3. Run the focused Node and Go regression suites.
4. Run the full Go suite and dashboard checks that do not require changing the
   protected design-system source.
5. Rebuild the shipped executable with `task bundle` only after the protected
   prerequisite files are present and with explicit authorization for that
   build boundary.
6. Start Development from the rebuilt `dist\venom.exe`.
7. Verify listeners and health on 8081 and 8088, confirm the browser loads the
   live-reload frontend, and confirm `/api` traffic reaches the shared backend.
8. Stop and start Development again to prove the fast path does not reinstall.
9. Review Git scope before any commit or push. Confirm `Design_System` remains
   unchanged. Push only `main` and only after verification.

## Non-Goals

- No second backend on 8082.
- No separate development database.
- No automatic repair or modification of `Design_System`.
- No unconditional `npm ci` on every Start.
- No branch creation.

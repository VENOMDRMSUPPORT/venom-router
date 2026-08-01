# P6-TEST-002 — Operate-without-terminal evidence runbook

This is the **manual, dated evidence procedure** for the part of the P6 phase
gate that cannot run in CI: watching a human operate Venom end to end from the
**system tray and the dashboard**, on a real Windows desktop, against a **real
provider account**.

## Why this is a runbook and not a CI test

The P6-TEST-002 card asks for proof that the owner performs the whole
implemented lifecycle *with no terminal*. That claim splits into two halves, and
this repo has drawn the same line twice before (P2b-TEST-003, P5-TEST-001):

- **The CI-deterministic half (blocking, runs every build)** —
  `internal/httpapi/p6gate_operate_test.go`, `TestP6Gate_OperateWithoutTerminal`.
  It drives every lifecycle step through the same HTTP surface the dashboard
  calls, in order, against **fake provider backends**, with an owner the test
  provisions itself. Run it alone with:

  ```bash
  task p6-gate
  ```

- **The recorded-evidence half (this document)** — the things no CI runner can
  observe: that the tray menu exists and works, that a double-clicked
  `venom.exe` launches with no console window, and that a real provider account
  actually routes.

Three constraints force the split, and none of them is incidental:

1. **CI is credential-free.** No real provider key or owner password is ever
   stored in this repo or on a runner. The automated scenario self-provisions
   an owner and enrolls a fake credential.
2. **Provider base URLs are compile-time constants.**
   `providers.OpenCodeZenBaseURL` is a `const`, and a composed handler exposes
   no seam to redirect it. Enrollment and discovery are still deterministic
   because `providers.RegisterOpenCodeZen` takes its two HTTP probes as
   parameters — the automated scenario fakes exactly those. The **probe
   trigger** (`POST /offerings/{id}/probe`) has no equivalent seam: ControlMux
   builds its transport map from that constant. Triggering a live probe is
   therefore a step for this runbook, not for CI.
3. **The tray is Windows-only and has no headless surface.** Its logic is
   covered by `internal/tray`'s own tests (which the Windows CI job already
   runs); its *appearance and behaviour on a desktop* is what a human confirms
   here.

## Prerequisites

1. A Windows desktop with the repo built as the single double-clickable binary:

   ```bash
   task bundle
   ```

   This produces `dist\venom.exe` with the dashboard embedded. Note that plain
   `task build` does **not** produce it.
2. A real provider account you are willing to use (for example a free
   opencode-zen key).
3. No terminal open after step 1. That is the point of the exercise.

## Steps

Perform every step through the tray and the dashboard. **If any step requires a
terminal, stop and record that as a gate failure** — do not work around it.

1. **Silent launch.** Double-click `dist\venom.exe` from Explorer. Confirm: no
   console window and no Windows Terminal window appears, and a Venom icon
   appears in the system tray.
2. **Tray menu.** Open the tray menu. Confirm the entries are present and each
   does what it says: **Open Dashboard**, **Status**, **Restart**, **Quit**.
3. **Open the dashboard** from the tray's Open Dashboard entry. Confirm the
   browser opens on the loopback control-plane URL and the dashboard renders.
4. **First-run setup.** Set the owner password in the dashboard's first-run
   screen. (Do not reuse a password you use anywhere else, and do not record it
   in this document — see the project's secret rules.)
5. **Connect a provider account.** Providers → the provider's card → *Connect
   Integration* → paste the real API key → *Validate & connect*. Confirm the
   account appears as connected and healthy.
6. **Discover.** Trigger discovery for that account and confirm the Models
   surface populates with the provider's real catalog.
7. **Probe.** Trigger a capability probe on one offering and confirm the
   certification state reaches `certified` with `capability_truth: supported`.
   *(This is the step CI cannot reach — see constraint 2 above.)*
8. **Route a request.** Use the Playground surface to send a request on
   `venom/pro`. Confirm a completion returns.
9. **Read diagnostics.** Diagnostics → confirm the request from step 8 is
   listed, then open its route explanation and confirm the attempts and the
   chosen offering are shown.
10. **Create a key.** API Keys → *New API key* → confirm the raw `vk_live_…`
    value is shown exactly once, and that after dismissing the dialog only the
    short non-secret prefix remains on screen.
11. **Connect a client.** Follow the connect-a-client quick start and confirm
    the generated client configuration carries the correct base URL.
12. **Restart from the tray**, then confirm the dashboard still shows the
    account, the catalog, and the key from the steps above.
13. **Quit from the tray** and confirm the process exits.

### Optional: the opt-in automated harness

With Venom still running and a key from step 10, the opt-in harness re-checks
the live instance over the wire:

```bash
VENOM_E2E_REAL_SDK_BASE_URL=http://127.0.0.1:8081 VENOM_E2E_REAL_SDK_KEY=vk_live_... go test ./internal/httpapi/ -run '^TestP6Gate_RealOperate_OptIn$' -v
```

With **neither** variable set it skips, and `TestP6Gate_RealOperateHarnessSkipsWithoutEnv`
proves it — that is what keeps CI credential-free.

## Evidence to paste back (dated)

Record, with the date and the Venom commit (`git rev-parse HEAD`):

- A screenshot of the tray menu open, showing the four entries.
- Confirmation that the double-click launch produced **no** console window.
- The provider and plan of the connected account (**never** the key itself).
- The number of models discovered, and the offering + operation probed with its
  resulting certification state.
- Confirmation the Playground returned a completion, and the request id its
  route explanation resolved.
- Confirmation the raw key appeared exactly once and only the prefix persisted.
- Confirmation the restart preserved state and the quit exited the process.
- Any step that required a terminal — **this is the gate finding, if there is
  one.**

## Known limits (do not record as failures)

- **Provider response quality** is out of scope; this proves reachability and
  the lifecycle, not model behaviour.
- **Backup and restore are explicitly NOT part of this gate.** The card defers
  them to P8 (`P8-UI-001`, `P8-BKP-003`, the P8 gate).
- **The tray's own logic** is already covered by `internal/tray`'s tests on the
  Windows CI job; what this runbook adds is confirmation that a real desktop
  presents it correctly.

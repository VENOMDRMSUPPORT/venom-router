# CLAUDE.md — binding rules for agents working in this repo

The numbered docs in `docs/` remain the source of truth. This file consolidates the
rules that are **hard pass/fail lines**, so no agent has to discover them by luck.

Every rule below carries its source. If a rule and its source ever disagree, the
source wins and this file is the bug — fix it in the same change.

Precedence: an explicit owner instruction in chat > this file > `docs/` > default behavior.

---

## Hard invariants

Breaking one of these is never "an acceptable trade-off". If a task appears to
require breaking one, **stop and report it** instead of doing it.

### 1. `/health` returns minimal liveness only

`/health` is unauthenticated process liveness — served on the control-plane bind
(behind the loopback + Host-allowlist gate), **outside** `/api/control/v1`, no
session, no CSRF. It returns only "process up, listener accepting" and **no owner
data**. Readiness detail (DB / keyring / migration status) belongs on a
**distinct** owner-session-gated `/api/control/v1/health`, never as a second
liveness check.

Source: [docs/01-architecture.md §6d](docs/01-architecture.md) · implementation
`internal/httpapi/health.go`, registered in `internal/httpapi/controlmux.go`.

### 2. The control plane is loopback-only *and* authenticated

Binds only to `127.0.0.1` / `::1`; the loopback check reads the real TCP socket
address, never a header; `X-Forwarded-For` is never trusted; `Host` outside the
allowlist is 403 before any session or CSRF check. Owner authentication is
**primary, not defence-in-depth** — the network gate does not replace it and it
does not replace the network gate. There is no opt-out in v1.

Source: [docs/01-architecture.md §6a](docs/01-architecture.md) ("non-negotiable"),
§8.

### 3. Every repo file is English-only

Code, docs, comments, commit messages, test fixtures — zero Arabic in any file
that lands in the repository. Chat with the owner is Arabic; the repo is not.

Source: **owner rule (no doc anchor)** — this is the one rule here that `docs/`
does not state, which is exactly why it is written down.

### 4. `G:\Venom-Router` is untouchable; `F:\projects\venom-router` is read-only

`G:\Venom-Router` is the owner's separate **live** install and owns the shared
`%LOCALAPPDATA%\VenomRouter` keyring — never read it, never write it, never
migrate it. This project's data dir is `%LOCALAPPDATA%\venom-router` (lowercase,
deliberately a different name). `F:\projects\venom-router` is an authorized
**read-only** reference only. All work stays in `Desktop\venom-router`.

Source: `internal/platform/platform.go:5-9`,
[docs/tray-dev-environment.md:82](docs/tray-dev-environment.md).

### 5. A shipped `dist/venom.exe` must be GUI-subsystem (PE Subsystem = 2)

Build the double-clickable exe **only** via `task bundle`, never a bare
`go build -o dist/`. `dist/` is `.gitignore`d, so CI can never catch a
console-subsystem binary that reached it — it happened on 2026-08-06. `task bundle`
therefore *verifies* rather than assumes: it unit-tests its own PE header reader
(`node --test`) and then reads the field back, failing the build on anything but 2.
Plain `go build ./cmd/venom` stays console-subsystem for dev/CI, which is correct.

Source: `Taskfile.yml` (`bundle`), `.github/scripts/verify-bundle.mjs`,
`.github/scripts/pe-subsystem.mjs`.

### 6. `task gate` is the only verification worth claiming

Do not report work as passing on the strength of a single package's `go test`, a
green-looking log, or a CI run you did not read. The one blocking static-invariants
gate is:

```bash
task gate
```

It runs gofmt, goimports, `go vet`, `golangci-lint`, and `go test ./...` — which is
also where the import-layering test, the no-slug-switch check, and the
P1-SEC-006 secret canary live. Two consequences learned the hard way:

- **Read the step order before trusting a green.** An early failing step (e.g.
  gofmt) hides every later step; a chain of reds can hide behind the first one.
- `-race` (`task test-race`, CGO_ENABLED=1) is separate and non-blocking.

Source: `Taskfile.yml` (`gate`, `test-race`),
[docs/08-engineering-standards.md §6](docs/08-engineering-standards.md)
(Definition of Done), §7 (principle → enforcement map).

### 7. Simplicity: one thing, one way

The owner's stated top priority. Reducing complexity and preventing duplication
outranks adding capability. A second copy of a core mechanism is a **defect**, not
a variation — three duplicated execution engines are what killed the old build.
Behavior is selected by typed capability/policy, never by `switch` on a provider
slug or model name. Prefer deleting to adding; if a change forces edits scattered
across unrelated packages, the seam is wrong — fix the seam.

Source: [docs/08-engineering-standards.md §1.2, §1.3, §10](docs/08-engineering-standards.md)
· owner directive.

---

## Also non-negotiable (short form)

- **Fail closed.** Unknown ⇒ ineligible/rejected. Unhandled cases return typed
  errors; they never guess. (08 §1.5)
- **Secrets never leak, provably.** Sanitize at the boundary; the canary test
  asserts no injected secret reaches any log, error, trace, or audit row. Secrets
  come from env, never a committed value. (08 §1.6)
- **Pure core, imperative shell.** `providers`, `accounts/domain`, `models`,
  `routing` hold no I/O, clock, randomness, or global state — all injected. SQL
  exists only in `storage` repositories. (08 §1.4, §2)
- **Docs are updated in the same change** as the behavior or extension point they
  describe. Code and spec must not diverge. (08 §6.7)
- **Never `git checkout <file>` with uncommitted work** in the tree.

## Definition of Done

The full eight-point checklist is
[docs/08-engineering-standards.md §6](docs/08-engineering-standards.md). Read it
before claiming a task is complete; the short version is *tests first, gate green,
no new duplication, no leaked secret, docs updated in the same breath*.

## Where the rest lives

| Topic | Doc |
|---|---|
| Architecture, HTTP surfaces, security model | [docs/01-architecture.md](docs/01-architecture.md) |
| Schema & domain model | [docs/02-domain-model.md](docs/02-domain-model.md) |
| Providers & OAuth | [docs/03-provider-integration-catalog.md](docs/03-provider-integration-catalog.md) |
| Model intelligence & certification | [docs/04-model-intelligence.md](docs/04-model-intelligence.md) |
| Tier/routing engine | [docs/05-tier-engine.md](docs/05-tier-engine.md) |
| Roadmap & phase gates | [docs/06-roadmap.md](docs/06-roadmap.md) |
| Design system (visual drift gates) | [docs/07-design-system.md](docs/07-design-system.md) |
| Engineering standards (process contract) | [docs/08-engineering-standards.md](docs/08-engineering-standards.md) |
| Control API contracts | [docs/09-control-api.md](docs/09-control-api.md) |

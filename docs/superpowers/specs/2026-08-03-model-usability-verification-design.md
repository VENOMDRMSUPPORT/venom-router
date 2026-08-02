# Automatic Continuous Per-Account Model-Usability Verification

**Date:** 2026-08-03
**Status:** Design — approved, pending spec review
**Scope of this spec:** opencode-zen first; a per-provider seam so the same
machinery generalizes to other providers later (only when a second provider
shows the same shape).

---

## 1. Problem

Today a connected account's model set is **declared, not verified**:

- **Discovery is manual.** Connecting an account creates the account and
  stops — no model discovery is triggered. Models only appear after the owner
  clicks "Refresh models".
- **No model is ever certified as actually working for `chat`.** The code
  asserts chat/streaming are "certified by actual use", but no code path writes
  that certification. Chat-only models therefore never reach `routable`.
- **The free-account model count is the raw discovered set**, not the
  verified-usable set. The fleet page's "N unique models" counts distinct
  `provider_model_id`, regardless of whether the model works for this account.
- **There is no continuous re-verification.** Nothing automatically re-checks
  that a model still works — to catch a model that stopped working, was
  disabled, had its trial expire, was removed by the provider, or is newly
  added.

### Empirical grounding (opencode-zen, verified 2026-08-03)

- `GET /v1/models` ignores credentials (returns 200 for any key) and returns
  60 models mixing free and paid, with only four fields each
  (`id`, `object`, `created`, `owned_by`) — **no** free/status/availability
  metadata.
- A **free, working** model (`big-pickle`) returns `200` with real content and
  `"cost":"0"`.
- A **paid** model (`claude-fable-5`) returns an error envelope
  `{"error":{"type":"CreditsError",...}}` ("Insufficient balance").
- The `-free` name suffix / a "free" label is **not** a reliable signal: some
  labelled-free models do not work, and some free models (`big-pickle`) carry
  no suffix at all.

### Ecosystem validation

Reviewing opencode (anomalyco/opencode), cline, 9router, and omniroute plus
the live models.dev dataset: **no provider exposes a runtime endpoint or field
listing only free-and-working models.** Every tool classifies "free" from
static metadata (models.dev `cost == 0`, or `-free` suffix + a hardcoded
allowlist) and can never tell "free" from "free-but-exhausted". A **runtime
probe is the only reliable judge of current usability**, and the whole
ecosystem relies on it. Our existing rule (models.dev `cost == 0` + a runtime
probe) is already the ecosystem best practice; this spec wires it into an
automatic, continuous loop and sharpens the probe's error classification.

---

## 2. Existing infrastructure to reuse (do not rebuild)

A code map established that ~80% of this feature already exists as tested,
wired units. The work is mostly **wiring**, not new domain logic.

| Capability | Location | Status |
| --- | --- | --- |
| Generic 30s `Scheduler` (immediate-run + interval, panic-recovering, graceful shutdown) | `internal/app/boot.go` | Wired; adding a tick is a one-liner |
| Per-account/per-model/per-operation `certifications` store (6-state lifecycle + separate `capability_truth` + version + audit) | `internal/models/certification.go`, `internal/storage/certifications.go` (migration `00006_catalog_discovery.sql`) | Wired |
| `CertificationDriver` state machine (Load→Transition→CAS→audit) | `internal/intelligence/certdriver.go` | Wired |
| Manual discovery `POST /accounts/{id}/discover` → `DiscoveryService` → catalog + baseline certs; opencode-zen free intersection | `internal/httpapi/discovery.go`, `internal/intelligence/discovery.go`, `internal/providers/opencode_zen.go` | Wired |
| Capability probe with genuine response-**body** inspection (Witness) + `ProbeGuard` budget | `internal/intelligence/capabilityprobe.go`, `internal/intelligence/probesafety.go`, `internal/httpapi/probeadapters.go` | Wired (opencode-zen transport) |
| Background ticks: `RecertifyTick` (30-day staleness→expire), `DrainTick` (observed→probing), `ReclaimTick` (stale slot reclaim) | `internal/httpapi/probeworkers.go` | Wired into scheduler |
| Account-level health via credential-authentic chat probe (distinguishes billing rejection from auth failure) | `internal/providers/validate.go`, `internal/httpapi/opencode_zen_seams.go` | Wired |
| Models page + Fleet "N unique models" — both read the same `/offerings` projection | `internal/httpapi/models.go`, `dashboard/src/fleet/modelStatus.ts` | Wired |

### The specific gaps (what this spec builds)

1. **Auto-discovery on connect** — not wired; discovery is manual only.
2. **Background probe *execution*** — the scheduler moves a certification row to
   `probing`, but nothing in the background runs the actual transport probe /
   `RecordAttempt` that advances `probing → certified`. `ProbeWorkers` holds no
   `ProbeTransport`, so rows stall in `probing` until a human hits the HTTP
   probe endpoint. **This is the single most important missing hop.**
3. **Chat-usability certification** — asserted in comments, absent in code.
   Chat is deliberately excluded from probing today, and no path certifies it
   from live traffic, so chat models never become routable.
4. **opencode error taxonomy** — the existing 401-body classifier only knows
   `CreditsError` and `AuthError`; it does not know `FreeUsageLimitError`
   (free-tier exhausted, transient) or `GoUsageLimitError` (paid Go limit).

---

## 3. Design (Approach A: wire the self-driving loop + add chat-usability)

### A. Auto-discovery on connect

After a successful account connect (API-key `ConnectAPIKeyAccount` and the
OAuth completion path), enqueue a discovery job for the new account
automatically, reusing the existing discovery job machinery.

- **Non-blocking:** connect still returns `201` immediately; discovery runs
  async. A discovery failure never fails the connect — the account is
  connected regardless, and the continuous loop (and the manual button) remain
  fallbacks.
- **Idempotent:** enqueuing is safe against retries/duplicate connects.

### B. Self-driving background probe execution (the missing hop)

Give the background probe workers the `ProbeTransport` they currently lack, and
have a tick execute the **existing** probe pipeline for each `observed`/stale
offering-operation:

```
ProbeGuard.Admit  →  transport.Probe  →  CertificationDriver.RecordAttempt
```

advancing `probing → certified` (or `→ suspended`) with no human action.

- **Budget-respecting:** every probe passes `ProbeGuard` (per-account 500/24h,
  per-provider in-flight cap, context cooldown). Continuous verification must
  not bypass the safety model.
- **Paced:** process a bounded batch of offering-operations per tick so a
  60-model account fills in over successive ticks rather than in one burst.

### C. Chat-usability probe (new operation certification)

Add `chat` as a probeable operation with **usability** semantics (distinct from
the capability probes for tools/structured_output/vision):

- **Fixture:** a minimal completion (`max_tokens` small, `"ping"`). The
  required witness is simply non-empty text (`WitnessTextOnly`).
- **Body classifier** (per-provider seam — §E) maps `(status, body)` to an
  outcome using the opencode taxonomy:

| Provider response | Meaning | Certification outcome |
| --- | --- | --- |
| `200` + non-empty content (ideally `cost=="0"`) | free and working | **supported → routable** |
| body contains `FreeUsageLimitError` | free model, **temporarily exhausted** | **transient** — keep the offering, do **not** mark unsupported; schedule retry honoring `retry-after-ms` / `retry-after` |
| body contains `CreditsError` or `GoUsageLimitError` | paid / not free-usable for this account | **unsupported** (excluded from the free-usable set) |
| body contains `AuthError` | invalid key | **account-level** problem (health) — do **not** mark the model |
| other 4xx / empty / malformed | rejected / unknown | inconclusive → not usable |

The transient distinction is essential: a free model that is merely exhausted
right now must **not** be learned as permanently broken — it stays listed and is
re-probed after its backoff. This is what makes the visible free set "breathe"
as usage limits reset.

### D. Free-count and Models-page sync (mostly by construction)

The count and the Models page already read certification truth from the shared
`/offerings` projection, so once chat-usability certifies, "working" becomes
meaningful with no new plumbing.

- **Free-account count:** surface the **verified-usable** free-model count, not
  the raw discovered count. Recommended display: show the working count
  prominently (e.g. "N working / M discovered") so the headline is honest while
  the total stays visible. (See open decision 2.)
- **Sync:** the Models page and the Fleet page read the same source of truth, so
  they stay in sync by construction.
- **Continuous freshness:** `RecertifyTick` plus the newly-wired background
  execution re-verify over time, catching added / removed / expired / exhausted
  models. Consider a shorter recertify TTL for free/volatile providers than the
  30-day default (see open decision 3).

### E. Per-provider discovery-classification seam

Define one interface with two responsibilities, so provider-specific logic is
isolated and the machinery generalizes:

1. **Candidate free set** — given the provider's raw model list plus external
   metadata, produce the free candidates.
2. **Usability classification** — given a probe response `(status, body)`,
   classify per the §C taxonomy.

**opencode-zen implementation (first and only in this spec):**

- Candidates: models.dev `cost.input == 0 && cost.output == 0` **and**
  `status != "deprecated"` (already implemented). Fallback when models.dev is
  unavailable: `id` ends with `-free` **or** is in a small explicit allowlist
  (`["big-pickle"]`) — mirrors 9router; `cost == 0` already covers `big-pickle`,
  so this is only a degraded-mode fallback.
- Judge: the chat-usability probe with the opencode error taxonomy.

Other providers (e.g. those exposing a dedicated free-models endpoint)
implement the same interface later. **Do not** generalize speculatively — add a
second implementation only when a real second provider needs it.

---

## 4. Error handling

- **`FreeUsageLimitError`** → transient; honor `retry-after-ms` / `retry-after`;
  never a permanent failure.
- **Discovery-on-connect failure** → non-fatal; account stays connected.
- **`ProbeGuard` refusal (budget exhausted)** → skip gracefully; retry next
  window. Log the skip; never silently drop coverage.
- **Secrets** → the key is sent only as the Authorization header; response
  bodies are read solely to classify and then discarded (existing pattern).
  Never logged.

---

## 5. Data and state

- Reuse the `certifications` table and the `CertificationDriver` state machine.
  The `chat` operation gets its own `offering_operations` row + baseline
  certification like any other operation.
- The **transient "free-but-exhausted"** condition must be representable without
  being confused with `unsupported`. **Prefer reuse over new schema:** keep the
  offering-operation in a non-terminal state (e.g. `observed`) with a transient
  marker / next-retry evidence, rather than adding a new lifecycle state or
  column, unless implementation shows reuse is untenable.

---

## 6. Testing

- **Unit — classifier (table-driven):** each taxonomy body (200+content,
  `FreeUsageLimitError`, `CreditsError`, `GoUsageLimitError`, `AuthError`,
  malformed/empty) maps to the correct outcome, including the transient branch.
- **Unit — auto-discovery on connect:** a successful connect enqueues a
  discovery job; a discovery failure leaves the account connected.
- **Unit — background execution:** a `probing` row advances to `certified` via a
  fake transport, and `ProbeGuard` refusal is respected (no bypass).
- **Integration:** connect account → auto-discovery → background probes → a free
  working model becomes `routable`; a paid model becomes `unsupported`; a
  free-exhausted model stays listed and is retried.
- **Count:** the free-account count reflects the verified-usable set.
- **Harness discipline** (project lessons): prove each mutation actually changes
  behavior; verify the verifier; do not trust `execSync` + cmd.exe quoting.

---

## 7. Open decisions (confirm at spec review)

1. **In-flight cap for cheap chat probes.** Free-model chat probes cost `$0`, so
   the binding limit is latency/serialization, not money. Recommended: a bounded
   fast-lane allowing a small number of concurrent zero-cost chat probes
   (config knob, modest default e.g. 4) so on-add fill is fast, rather than the
   current per-provider in-flight cap of 1. Alternative: keep serialized
   (simplest/safest, slower initial fill).
2. **Free-account count display.** "N working / M discovered" (recommended) vs a
   working-only headline.
3. **Recertify TTL for free/volatile providers.** Shorter than the 30-day
   default (e.g. 24h) to catch rotation faster, or keep 30 days.

---

## 8. Scope / non-goals

- **In scope:** opencode-zen end-to-end; the per-provider seam; auto-discovery
  on connect; background probe execution; chat-usability certification; honest
  free-count.
- **Out of scope (future):** passive certification from live routing traffic
  (Approach B) — additive later; new provider integrations; generalizing the
  discovery seam to a second provider before one exists.

# Venom Catalog — Code Review Report

| | |
|---|---|
| **Date** | 2026-08-24 |
| **Scope** | Uncommitted working-tree diff under `catalog/` (20 modified files, 3 new files) |
| **Baseline commit** | `fd494cb` — *Animate catalog view switches* |
| **Effort level** | max (`/code-review ultra`) |
| **Runtime** | Node v24.19.0, Windows 11 Pro 10.0.26200 |
| **Working path** | `C:\Users\venom\Desktop\venom-router\catalog` |
| **Confirmed findings** | 15 reported · 7 additional verified but cut by the report cap · 5 refuted |

---

## 1. Verification status

`npm run typecheck` passes clean on both tsconfig projects. That is worth stating up
front, because it is precisely *why* several of these defects reached review: the
CSS-module type is an index signature, so a misspelled style key type-checks and only
fails at runtime, and an unused-but-still-threaded prop chain is invisible to the
compiler.

The remaining three gate commands (`test:backend`, `test:spa`, `build`) were not the
subject of this review; the findings below identify the specific test cases that are
missing rather than claiming the suites are red.

---

## 2. Findings, ranked

### F1 — Reconcile tick reopens operator-resolved roster alerts
**`server/alerts.ts:327`** · correctness · CONFIRMED

The new 30-second reconcile tick treats a roster change as a level-triggered
condition, but a roster change is an *immutable past event* that remains a candidate
for the full 7-day `ROSTER_ALERT_WINDOW_MS`. So resolving an alert cannot stick.

An operator resolves a `model_added` alert. 30 seconds later the tick runs, the event
is still inside the window, so `currentStatus === 'resolved'` maps to
`nextStatus = 'open'` with `reopened = true`: `occurrence_count += 1` and
`enqueueNotification(..., 'reopened')` fires. That repeats every 30 seconds for seven
days — roughly **20,160 reopens and 20,160 notification rows for a single alert**.

It gets worse with webhooks off. `enqueueNotification` (`notifications.ts:67`) INSERTs
unconditionally, ignoring `config.enabled`, while `deliverDueNotifications` returns 0
early when disabled. Rows therefore accumulate as `pending` forever and
`notificationDeliverySummary`'s `pending` count climbs without bound.

**Effect:** the Resolve button is unusable, and the notifications table grows without
limit.

**Fix:** distinguish "the condition still holds" from "the event is still inside the
window". A resolved roster alert must stay resolved for the life of its window.

**Test gap:** `alerts.test.ts` covers acknowledge-survives-reconcile (line 212) and
age-out (line 189). The resolved-within-window case is untested.

---

### F2 — `rosterCandidates` silently inherits a 500-change limit
**`server/alerts.ts:204`** · correctness · CONFIRMED

`loadChanges(db, { since })` is called with no limit, so it falls back to
`DEFAULT_CHANGES_LIMIT = 500`. `loadChanges` orders `at DESC` and breaks at
`changes.length >= safeLimit`, which means the **oldest** changes inside the 7-day
window are the ones dropped.

The resolve sweep at `alerts.ts:345` is level-triggered against that truncated
candidate set, so it marks the vanished changes' still-open alerts `resolved` and
fires a `resolved` webhook. The next quieter reconcile brings them back inside the cap
and reopens them — an alert that flaps resolved/reopened with a webhook each way.

Two consequences:

- `standalone.md`'s promise that *"a roster alert stays a candidate for seven days"*
  is false above 500 changes per window.
- The cap is applied **before** the `ROSTER_ALERT_SEVERITY` filter, so 500
  `price_changed` rows can starve every availability alert out of the candidate set.

**Fix:** pass an explicit limit sized to the window, or filter by severity inside the
query rather than after it.

---

### F3 — Change-cursor poll blanks ProviderPage and ComparePage
**`src/hooks/useCatalog.tsx:59`** · correctness · CONFIRMED

The new change-cursor poll reuses the existing `[nonce]` catalog effect, which calls
`setLoading(true)` unconditionally. Two pages guard on a bare `if (loading)`:

- `ProviderPage.tsx:118` — `if (loading) return <div>Loading…</div>`
- `ComparePage.tsx:34`

So any detected change discards the entire rendered page until the refetch lands,
losing scroll position, expanded rows, and focus. `DashboardPage` is protected by
`if (loading && !data)` at line 209, which is why this was never noticed.

It compounds badly. The pre-existing resolution-retry effect clamps with
`Math.max(1_000, untilNext)` and re-arms on `[data]`, so while any model is
`processing` with a past `nextAttemptAt`, `nonce` bumps **every ~1 second** — blanking
those two pages once per second. Auto-evaluation shipping opt-out makes `processing`
models routine after every sync, so this is the common path, not the edge.

**Fix:** only set `loading` when there is no `data` yet — i.e. make the refetch a
background revalidation, matching what DashboardPage already assumes.

---

### F4 — `isolation: isolate` traps the Evaluate modal under the sidebar
**`src/pages/ProviderPage/ProviderPage.module.css:1798`** · correctness · CONFIRMED

`.viewStage { isolation: isolate }` was added to scope the view-transition animation,
but `isolation: isolate` creates a full **stacking context**, not just a compositing
layer.

`EvaluateModal` is rendered at `ProviderPage.tsx:830` (table view) and `:938` (grid
view), both inside the `.viewStage` div opened at line 315 — there is no portal. The
modal's `z-index: 200` (`EvaluateModal.module.css:10`) is now scoped *inside*
`.viewStage`, whose own `z-index` is `auto` in the root context. It therefore no longer
competes with `.sidebar`'s `z-index: 100` (`Sidebar.module.css:28`) or the header's
`80`, both of which sit in the root context.

**Effect:** clicking Evaluate on any provider row renders the dialog *under* the
sidebar and header, which also remain clickable through the dimmed backdrop.

**Fix:** drop `isolation: isolate` — it is not needed to scope the animation. If a
containing context is genuinely wanted later, portal the modal to `document.body`
first.

---

### F5 — Change cursor advances before the refetch it triggers succeeds
**`src/hooks/useCatalog.tsx:121`** · correctness · CONFIRMED

`seenCursor.current = cursor` runs unconditionally, one line *before* the comparison
that fires `setNonce`. So a failed refetch pins the roster stale permanently — exactly
the staleness the polling was added to remove.

Sequence: the cursor reads `T1` on mount. A sync retires a model. The next probe
returns `T2`, stores it, and bumps `nonce`. The `[nonce]` catalog fetch then fails —
service restart, or a 503 while the sync holds the single writer. `setError` is set but
`data` keeps the pre-sync roster. Every later probe returns `T2`, now equal to
`seenCursor.current`, so `setNonce` never fires again and the retired model stays on
screen indefinitely with no retry path.

**Fix:** advance `seenCursor` only after the refetch resolves, or track the last
*successfully rendered* cursor separately from the last probed one.

**Test gap:** `useCatalog.test.tsx:129` covers a failing probe, never a failing catalog
fetch after a *successful* probe.

---

### F6 — `onRosterAdded` throwing discards a committed sync's outcome
**`server/sync-runner.ts:183`** · correctness · CONFIRMED

`onRosterAdded` is invoked inside the `SyncOutcome` object literal, so anything it
throws escapes *after* the sync has already committed.

Path: `autoEvaluate` → `evaluations.plan(...)` → `planEvaluation(db, ...)` throws for
one malformed newly-discovered model (unresolvable identity, bad overlay row). The
providers have already been written and `beginResolutionWindow` + `onSnapshot` have
run, but the `outcome` literal never completes, so `this.last = outcome` never
executes and the exception propagates out of `run()`.

**Effect:** `POST /v1/sync` returns 500 and `/v1/health` reports no last sync — for a
sync that actually succeeded.

Every other cross-cutting step in this file is deliberately failure-isolated (the
file's own comment: *"One job that throws must not cancel the twenty behind it"*).
This one is not.

**Fix:** compute the report into a local inside a try/catch before building the
literal.

---

### F7 — Dashboard reads an `alert.notifications` field the API no longer returns
**`src/pages/DashboardPage/DashboardPage.tsx:838`** · correctness · CONFIRMED

`app.ts:193` changed `alerts: filtered.map((alert) => ({ ...alert, notifications: ... }))`
to plain `alerts: filtered`. The dashboard still reads `alert.notifications?.[0]`, so
`latest` is now always `undefined` and the ternary chain falls through to
`delivery?.enabled ? 'Webhook queued' : 'Webhook disabled'`.

**Effect:** an alert whose webhook exhausted its 5 attempts and is `failed` renders
**"Webhook queued"** — a live-looking claim that is false. `catalog/CLAUDE.md` forbids
this directly: *"A fallback may be stale only when it is explicitly identified as a
snapshot. It must never pretend to be a live answer."* The `delivery.failed` count is
still returned alongside, so the UI visibly contradicts itself.

**Fix:** either restore the per-alert delivery state on the API side or render the
label as unknown. Do not infer a live delivery status from a config flag.

---

### F8 — Shared evaluation identities are double-charged against the budget
**`server/auto-evaluation.ts:115`** · correctness · CONFIRMED

Cost is per **identity**: `planEvaluation` derives coverage from
`repository.identityDimensions(identityId)` (`plan.ts:125`) and
`estimatedRequests = dimensions.length * REQUESTS_PER_DIMENSION` (`plan.ts:160`). But
all offers are planned up front, and `enqueue`'s duplicate check keys on
`providerId + modelId` (`evaluation-runner.ts:130`), **not** `identityId`.

So when a sync discovers two provider offerings of the same canonical model, both
report 6 unmeasured dimensions at plan time → 401 requests each, both are accepted,
and `committedRequests` reaches 802 of 1200. A third genuinely-unmeasured offer at 401
is then refused `over_budget`. When the second job actually starts, `runJob` re-plans
(`evaluation-runner.ts:192`), finds the identity already scored, and spends ~23
requests — so **378 phantom requests displaced real coverage for a whole sync cycle**.

The module's own comment already states *"quality is measured per identity"*, so the
sharing is known; the budget arithmetic just doesn't account for it.

**Fix:** deduplicate candidates by `plan.identityId` before summing estimated cost.

---

### F9 — `enqueue` can queue a job and start no worker
**`server/evaluation-runner.ts:139`** · correctness · CONFIRMED

`enqueue` starts a worker only `if (!this.working)`, but `this.working` is cleared in a
`.finally` two microtask hops after `drain()` settles. An enqueue landing in that
window queues the job and starts nothing.

Reproduced empirically on Node v24.19.0 against the real class: with *k* microtask hops
between the last sample resolving and the `enqueue` call, both `k=1` and `k=2` return
`accepted=true, position=1` while `state='idle'` and `queue=1`. The job never runs until
an unrelated later enqueue arrives.

The reviewed diff is what makes this reachable: `autoEvaluate` now calls `enqueue` for
every candidate in one synchronous loop (`auto-evaluation.ts:147`) from a post-`await`
continuation inside `SyncRunner.run()`. One stranded enqueue strands the whole
discovered batch and silently reopens the very discovery gap this feature was written
to close — until the next sync, ~6 hours later.

**Fix:** re-check `this.queue.length` in the `finally` and restart the worker if it is
non-empty.

---

### F10 — DOM/localStorage side effects inside a React state updater
**`src/hooks/useTheme.ts:82`** · correctness · CONFIRMED

`applyThemeSync` — which mutates the DOM, injects a `<style>` node, and writes
`localStorage` — is called from *inside* the `setTheme` updater. React state updaters
must be pure, and React invokes them twice under StrictMode in development, so one
click performs the DOM write, the `<style>` injection, and the storage write twice.

Separately, the effect that used to derive the DOM from `theme` had its dependency
array changed from `[theme]` to `[]` (labelled "Initial sync"). That deletes the
state → DOM derivation entirely: `data-theme` is now correct only because every
mutation path happens to call `applyThemeSync` by hand. Any future path calling
`setTheme` without it — or a React state restore — leaves `theme` and the DOM attribute
permanently disagreeing, with no test covering it.

**Fix:** move `applyThemeSync(next)` out of the updater into an effect keyed on
`[theme]`. That is both pure and self-enforcing.

---

### F11 — Reconcile UPDATE never refreshes `provider_id` / `model_id`
**`server/alerts.ts:335`** · correctness · CONFIRMED

The reconcile UPDATE refreshes severity, title, and detail, but not the model link. So
a grouped roster alert keeps whatever link its *first* occurrence had.

`rosterCandidates` exists specifically to null out `modelId` when the referenced
`models` row is absent, because the composite FK would otherwise abort the entire
reconcile. But that correction only ever reaches the database on INSERT:

- An alert first inserted while the model row existed, whose row is later absent, keeps
  the stale `model_id` on every subsequent UPDATE.
- Symmetrically, an alert that starts as one model and later groups more members keeps
  pointing at the single original model — sending the reader to the wrong row, which is
  the exact failure the comment at `alerts.ts:186` claims to avoid.

**Fix:** add `provider_id = ?, model_id = ?` to the UPDATE.

---

### F12 — Skipped offers are dropped permanently, not deferred
**`server/auto-evaluation.ts:143`** · correctness · CONFIRMED

`discovered` comes from `provider.added`, and `sync/engine.ts:318` computes
`const added = roster.filter((id) => !storedById.has(id))` — models not yet stored.
Once a model is written it is never in `added` again, so the auto-evaluation trigger
fires **exactly once per model in its lifetime**.

Any offer skipped as `over_budget`, `missing_credentials`, or `consent_required` is
therefore dropped forever. Both `standalone.md`'s *"retried by the next run"* and the
log line *"deferred to the next run"* misdescribe the behavior.

Compounding it: the budget check runs with `committedRequests = 0` and has no carve-out
for a lone job, so a 401-request new identity under a 200-request budget is refused
forever rather than being the one thing the budget buys.

**Fix:** drive the trigger from unevaluated-offer *state* rather than the one-shot
`added` delta, and allow a single job to exceed the budget when nothing has been bought
yet. Correct the doc and the log line in the same change.

---

### F13 — A blank budget env var silently disables measurement while reporting it on
**`server/auto-evaluation.ts:41`** · correctness · CONFIRMED

`env.X ?? DEFAULT` does not catch an empty string, and `Number('')` is `0`, which is
finite. So `CATALOG_AUTO_EVALUATION_MAX_REQUESTS=` in a `.env` — or a blank CI
variable — yields `raw = 0`, `Number.isFinite(0) === true`, and
`maxRequestsPerRun = 0`.

Every offer is then reported `over_budget` while `enabled` stays `true`, and startup
prints *"auto-evaluation: on, up to 0 requests per sync"*. The reporting says on; the
behavior is off. The module's own comment calls a budget of 0 *"the honest way to turn
the spending off"*, so nothing flags this as misconfiguration.

**Fix:** treat a blank string as absent, matching how `notifications.ts:36` already
does `?.trim() || null`.

**Test gap:** no test covers the empty-string case.

---

### F14 — Subscription cost cell prints a bare reference rate as if it were the price
**`src/components/ScoreCell/ScoreCell.tsx:294`** · correctness · CONFIRMED

Two defects in the same cell.

**(a) The guard was removed, not relocated.** For `p.kind === 'included'` with
`ref > 0`, the cell now prints a bare `$3` in `styles.value` — the *real-charge* style —
with the `ref` marker gone. That is precisely what the deleted comment forbade:

> a bare `$3` under a subscription reads as what you pay, which is the one claim the
> provider's documentation denies — so it moves rather than disappearing.

`statedOnce` was renamed to `_statedOnce` and discarded, while `ProviderPage.tsx` still
computes `uniformCostKind !== null` and threads `costStatedOnce` through `ModelTable`
(line 689) and `ModelGrid` (line 835) to **four dead call sites** (806, 807, 910, 914).
The old assertion `expect(screen.getByText(/ref \$3/))` was rewritten to accept the new
rendering, so nothing pins the invariant any more.

**(b) Two CSS-module keys do not exist.** At lines 279–280, `styles.refLabel` and
`styles.refUnit` are absent from `ScoreCell.module.css` (only `.refPrice` at line 255
exists). Both spans render `class="undefined"`, so the "Market ref" label and the "/M"
unit inherit ambient styling at full weight — again making the reference figure read as
the price. `typecheck` cannot catch this because the CSS-module type is an index
signature.

**Fix:** restore the reference-rate marker for subscription plans, add the two missing
CSS classes, restore the test assertion, and delete the dead `costStatedOnce` prop
chain.

---

### F15 — `+N` capability overflow badge ignores the unknown-capability icons
**`src/pages/ProviderPage/ProviderPage.tsx:1184`** · correctness · CONFIRMED

The new overflow badge truncates only the *known*-capability icons; the `showUnknown`
icons are still appended after it.

For a table row (`<CapabilityBadges model={m} showUnknown />`, line 809) on a model with
6 reported capabilities and `capabilities.structured === null`, the output is 4 icons,
then `+2`, then an unknown-capability icon. The overflow marker sits mid-row and claims
2 hidden when 3 items follow it, while `aria-label="…: 6 reported"` accompanies 7
rendered icons.

Width overflows too: `.caps` is `width: 166px; box-sizing: border-box; padding: 5px 7px`
= 152px of content, but `4×24 + 25 + 24 + 5×5 = 170px` with `.capIcon { flex-shrink: 0 }`.
The content bursts the bordered pill the redesign just sized.

**Fix:** compute the overflow across the full icon set, unknown icons included, and
render the badge last.

---

## 3. Verified but below the report cap

All seven were confirmed and are worth fixing; they were cut by the 15-finding limit,
not by doubt.

| # | Location | Defect |
|---|---|---|
| C1 | view-transition CSS | The 260 ms `data-transitioning` clear runs against a 450 ms staggered animation, so cards 3 and later snap mid-flight. |
| C2 | `server/alerts.ts` | The alert id collapses `note: null` and `note: ''` into one id while the *group key* distinguishes them, so one publish-policy decision silently overwrites another. |
| C3 | `server/alerts.ts` | The first reconcile after deploy backfills the window and fires an `opened` webhook for every roster event in the past 7 days. |
| C4 | `listAlerts` | Returns the now-permanently-growing `operational_alerts` table in full on every 30-second poll. |
| C5 | grid card `<dl>` | `<dd>` is emitted before `<dt>`, reversing term/description pairing for screen readers. |
| C6 | `notifications.test.ts:92` | Returns a promise out of its `try`, so `db.close()` is skipped when an async assertion fails. |
| C7 | `useCatalog.test.tsx:107` | Restores its `visibilityState` spy only on the happy path — `afterEach` calls `vi.clearAllMocks()`, which does not uninstall a spy — so one real failure cascades into the two tests after it. |

---

## 4. Refuted — recorded so they are not re-raised

| Claim | Verdict |
|---|---|
| `requestAnimationFrame` teardown leaks permanently in a hidden tab | **Refuted** |
| `drain`'s `stopping` flag poisons the runner forever | **Refuted** — it self-heals on the next enqueue. *But* that cycle silently discards the rest of the queue, which is a smaller real bug. |
| Retry click causes a double fetch | **Refuted** — React 19 batches it. |
| The alert tick can interleave into a sync transaction | **Refuted** — `transaction()` takes a synchronous callback. |
| `?? 0` in the score gauge violates "unrated must not become zero" | **Refuted** — `if (score.value === null)` early-returns, so the gauge never renders for unrated models and the `?? 0` is dead code. The rule is intact. |

---

## 5. Suggested fix order

**First — user-visible and cheap**

1. **F4** — delete `isolation: isolate`. One line; unblocks the Evaluate modal.
2. **F3** — gate `setLoading` on `!data`. Small; stops two pages blanking every second.
3. **F14b** — add the two missing CSS classes. Mechanical.

**Second — data integrity**

4. **F1** + **F11** + **F2** — the `alerts.ts` reconcile cluster. All three share one
   root cause: a level-triggered ledger built over immutable past events. Fix the
   semantics once rather than patching three symptoms.
5. **F7** — stop the dashboard claiming a delivery state it no longer receives.

**Third — correctness under load**

6. **F9** — the enqueue/worker race; it strands whole discovered batches.
7. **F6** — isolate `onRosterAdded` so a committed sync cannot be reported as a 500.
8. **F8** + **F12** + **F13** — the auto-evaluation budget cluster.

**Fourth — presentation and semantics**

9. **F14a** — restore the subscription reference-rate guard and its test assertion;
   delete the dead `costStatedOnce` chain.
10. **F15** — overflow badge across the full icon set.
11. **F10** — move `applyThemeSync` into a `[theme]` effect.
12. **C1–C7** as capacity allows; **C6** and **C7** first, since a leaky test fixture
    makes every later failure harder to read.

---

## 6. Missing test coverage, collected

Each of these would have caught a finding above:

- A roster alert resolved *inside* its 7-day window, reconciled twice (**F1**).
- More than `DEFAULT_CHANGES_LIMIT` classified changes inside the alert window (**F2**).
- A catalog fetch that fails *after* a successful cursor probe (**F5**).
- `onRosterAdded` throwing, asserting the sync outcome still lands (**F6**).
- Two newly discovered offers resolving to a single evaluation identity (**F8**).
- `enqueue` called 1 and 2 microtask hops after the previous drain settles (**F9**).
- `CATALOG_AUTO_EVALUATION_MAX_REQUESTS=''` (**F13**).
- The `ref $3` assertion in `ScoreCell`, restored rather than relaxed (**F14a**).

---

## 7. Standing note

`npm run typecheck` passing on both projects is the reason F14's missing CSS-module keys
and its dead prop chain reached review at all. Two structural gaps behind that:

- **CSS-module types are index signatures.** `styles.anyTypo` type-checks and renders
  `class="undefined"`. Any new `.module.css` deserves a check that every referenced key
  actually exists.
- **A removed guard leaves its scaffolding behind.** F14's `costStatedOnce` chain still
  runs, still threads through two components, and reaches four call sites that ignore
  it. Deleting a rule means deleting what fed it — otherwise the next reader assumes
  the rule is still enforced.

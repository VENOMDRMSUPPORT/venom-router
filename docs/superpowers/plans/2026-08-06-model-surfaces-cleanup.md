# Model Surfaces Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the two model surfaces display-only and legible — no manual test controls, no dead chips, and capability rows the owner can actually read.

**Architecture:** Deletion first, then adoption. Strip the manual triggers and their orphaned machinery from both surfaces, then replace the hand-rolled icon-only chip with the design system's existing labelled capability chip, then fix the one number-formatting bug that lives in the design system itself.

**Tech Stack:** React 18 + TypeScript, Vitest + Testing Library, the `@venom/design-system` workspace package (`file:../Design_System`, consumed through its built `dist/`).

**Spec:** `docs/superpowers/specs/2026-08-05-model-qualification-pipeline-design.md` §5 (Simplification and deletion).

## Owner decisions this plan implements

Stated directly after inspecting the live instance: the Live Models page is **display only**. The owner does not know what the probe or benchmark buttons are for and will never use them. Everything must happen automatically on provider enablement. Cost is obtained from the provider, account and quota surfaces, not per model.

## Live evidence

Captured 2026-08-06 from the owner's running instance. Both surfaces render, per model group: `Discover` and `Benchmark` buttons; a `Probe` button on every capability row; a `1 offering` caption that is always `1` because the owner has one account per provider; a `Needs review` badge; and an amber census banner whose eight blocker rows say "Not evaluated" for seven of them. Capability rows render **icons only with no text**, so `vision` and `reasoning` are indistinguishable at a glance, and `context_window` falls through to an unlabelled grey box because it is absent from the local icon map. Context reads `1M` on one row and `1.048576M` on the next.

## Global Constraints

- **English-only files.** Zero Arabic in any repo file — code, comments, docs, commit messages.
- **Strict TDD.** Write the failing test, run it, see it fail for the right reason, then implement. For deletion tasks the "test" is usually an existing test that must be deleted or inverted — see each task.
- **Never leave a test red, and never delete a test to make the suite pass.** A test whose subject is deleted is deleted with it; a test whose expectation inverts is inverted with a comment saying why.
- **Do not touch** `internal/tray/`, `Taskfile.yml`, `.github/scripts/`, or any Go file. This plan is dashboard + design system only.
- **`dashboard/scripts/check-ds-adherence.mjs` must keep passing.** It fails if the dashboard imports a `@venom/design-system` path outside the package's `exports` map, or vendors a file whose name matches a real design-system component.
- **Accessibility is enforced.** Several suites run `axe`; they must stay green.
- **Commit per task.** Verification for every task: `npm --prefix dashboard run build`, `npm --prefix dashboard test -- --run`, `npm --prefix dashboard run check:ds-adherence`, and `npm --prefix dashboard run lint` if that script exists.

---

## File Structure

| File | Responsibility | Change |
|---|---|---|
| `dashboard/src/models/ModelsSurface.tsx` | Live Models page | Modify: strip triggers, caption, badge, banner mount, and the orphaned job machinery |
| `dashboard/src/models/ModelsSurface.test.tsx` | its tests | Modify: delete trigger tests, invert the no-label assertion |
| `dashboard/src/fleet/ModelTestReport.tsx` | provider-page model modal | Modify: strip toolbar actions, cost chip, test copy |
| `dashboard/src/fleet/ModelTestReport.test.tsx` | its tests | Modify |
| `dashboard/src/fleet/CapabilityChips.tsx` | hand-rolled icon-only chip | **Delete** |
| `dashboard/src/fleet/CapabilityChips.test.tsx` | its tests | **Delete** |
| `dashboard/src/fleet/modelStatus.ts` | fleet-side derivations | Modify: drop `probeTargets` |
| `dashboard/src/fleet/jobs.ts` | job helpers | Modify: drop `runWithConcurrency` |
| `dashboard/src/fleet/fleet.css` | fleet styles | Modify: drop the capability-icon-box block |
| `dashboard/src/api/controlClient.ts` | API client | Modify: drop `startBenchmark` |
| `Design_System/components/domain-model/ModelIntelligence.tsx` | DS domain components | Modify: fix the ≥1M formatter |

---

## Task 1: Strip the Live Models page to display-only

**Files:**
- Modify: `dashboard/src/models/ModelsSurface.tsx`
- Modify: `dashboard/src/models/ModelsSurface.test.tsx`
- Modify: `dashboard/src/api/controlClient.ts`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `ModelsSurfaceProps` keeps `csrfToken` and `onSessionExpired` — `AppShell.tsx:523` passes both and is not in scope. `csrfToken` becomes unused inside the component; keep the prop and mark it with a comment rather than changing the shell's call site.

- [ ] **Step 1: Delete the trigger UI**

Remove from `ModelsSurface.tsx`:
- the `Discover`/`Benchmark` button group (`:708-727`)
- the `Probe` button inside `OfferingRow` (`:389-403`) and the `probeID` local (`:383`)
- the `1 offering` caption (`:699-701`)
- the `Needs review` badge (`:702-706`)
- the `<ReviewQueueBanner>` mount: the `banner` const (`:584-589`) and both render sites (`:596`, `:613`)

Do **not** delete `ReviewQueueBanner.tsx` itself or its test file — `dashboard/src/overview/OverviewSurface.tsx:314-320` still renders it. Removing it there is not in scope.

- [ ] **Step 2: Delete the machinery those triggers orphan**

From `ModelsSurface.tsx`, remove:
- `handleDiscover` (`:519-534`), `handleProbe` (`:536-548`), `handleBenchmark` (`:550-577`)
- `runTrigger` (`:482-517`) and `interface JobOutcome` (`:229-233`)
- state `outcome` (`:451`) and `busy` (`:452`)
- the job-outcome `<Card data-testid="job-outcome">` (`:615-632`)
- `backlogOnly` state (`:450`), the `visibleGroups` backlog filter (`:579-582`), the "Showing the review backlog only" strip (`:634-643`), and the backlog-scoped empty-state branch (`:645-654`) — all reachable only through the banner's `onReviewBacklog`
- `groupNeedsReview` (`:110-112`) and `offeringNeedsReview` (`:106-108`) with their rationale comment (`:92-105`)
- `OfferingRow`'s `busy` and `onProbe` props (`:276-277`, destructure `:279`) and the arguments at its call site (`:790-798`)
- now-unused imports: `startDiscovery`, `startProbe`, `startBenchmark`, `getJob`, `IconButton`, `ReviewQueueBanner`

Keep `setReloadToken` — the load effect and `ErrorState onRetry` (`:601`) still use it. Keep `firstOffering` (`:658`) — the group-header provider logo (`:676-688`) uses it. Keep `visibleGroups` as a plain alias of the loaded groups if that reads better than renaming every use.

- [ ] **Step 3: Delete `startBenchmark` from the API client**

`controlClient.ts:881-887`. After Step 2 it has no caller anywhere in `dashboard/src`. `startDiscovery` and `startProbe` stay — `ProviderRow.tsx`, `AccountRow.tsx` and `ModelTestReport.tsx` still use `startDiscovery`, and `startProbe` is removed in Task 2.

- [ ] **Step 4: Delete the tests whose subject no longer exists**

From `ModelsSurface.test.tsx`, delete these whole test cases:
`:657` "sends the CSRF token when triggering discovery" · `:685` "probes the offering-operation id the API reported, with CSRF" · `:732` "keeps the probe control disabled when the API reports no offering-operation id" · `:760` "reports an accepted trigger as a job in flight, never as instant success" · `:779` "states the honest completion outcome instead of the stale 'no canonical quality source' claim" · `:820` "explains a benchmark 409 as enrichment being disabled, not a permission problem" · `:975` "renders the banner and filters the catalog to the backlog when asked" · `:1047` "shows the review banner's count grouped by reason" · `:1011` "does NOT flag a fully certified+supported model as needing review, even when routable is false"

These are not being weakened — their subjects are gone. Say so in the commit message, listing them.

- [ ] **Step 5: Add the test that pins the new contract**

```tsx
it("renders no manual test or trigger control anywhere — the page is display only", async () => {
  renderSurfaceWithCatalog();
  await screen.findByText("cline-pass/kimi-k3");

  for (const label of [/discover/i, /benchmark/i, /^probe$/i, /needs review/i]) {
    expect(screen.queryByRole("button", { name: label })).toBeNull();
  }
  expect(screen.queryByTestId("job-outcome")).toBeNull();
  expect(screen.queryByText(/\d+ offerings?$/)).toBeNull();
});
```

Use the file's existing render helper and fixture rather than the placeholder name above — read the neighbouring tests. Expand the expanded-offering rows first if `Probe` only renders when expanded.

- [ ] **Step 6: Verify**

Run: `npm --prefix dashboard test -- --run src/models`
Expected: PASS, including the two `axe` cases (`:944`, `:966`).
Then: `npm --prefix dashboard run build` and `npm --prefix dashboard run check:ds-adherence`.

- [ ] **Step 7: Mutation-proof**

Restore just the `Benchmark` button JSX. The Step 5 test MUST fail. Remove it again.

- [ ] **Step 8: Commit**

```bash
git add dashboard/src/models/ dashboard/src/api/controlClient.ts
git commit -m "feat(dashboard): make Live Models display-only

Removes the Discover, Benchmark and Probe triggers, the always-'1'
offering caption, the Needs review badge and the census banner mount,
plus the job-outcome card, runTrigger, the backlog filter and the two
needs-review predicates that only those controls reached.

The owner does not use manual model testing; capability and context
facts arrive automatically on discovery. ReviewQueueBanner itself stays
- OverviewSurface still renders it.

Nine tests were deleted rather than weakened, because their subjects no
longer exist.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 2: Strip the Model Test Report modal to a report

**Files:**
- Modify: `dashboard/src/fleet/ModelTestReport.tsx`, `dashboard/src/fleet/ModelTestReport.test.tsx`
- Modify: `dashboard/src/fleet/modelStatus.ts`, `dashboard/src/fleet/jobs.ts`
- Modify: `dashboard/src/api/controlClient.ts`

**Interfaces:**
- Consumes: nothing from Task 1.
- Produces: `CapabilityChips` is left with only `capabilities` and `cap` in use; Task 3 deletes it entirely. `ModelTestReportProps` keeps its shape — `FleetOverview.tsx:517-530` is the only mount and stays unchanged.

- [ ] **Step 1: Delete the toolbar actions and the per-chip test affordance**

From `ModelTestReport.tsx`:
- the `Refresh Models` and `Test All` buttons and the `Testing n/m…` progress span, from the `vnd-report-toolbar` block (`:296-322`). **Keep the search `Input` and the status `Select`** in that same block — they are display controls the owner did not object to.
- `handleRefreshModels` (`:161-184`), `handleTestAll` (`:186-230`), `handleTestCapability` (`:232-252`)
- `TEST_ALL_CONCURRENCY` (`:80`), `progress` state (`:112`, sets at `:189`/`:199`, reset `:156`), `probingOperationId` state (`:114-119`, set `:237`, reset `:157`)
- the `onTest`, `disabled` and `probingOperationId` props at the `CapabilityChips` call site (`:382-388`), leaving `capabilities` and `cap`
- the cost chip (`:369-381`) entirely
- imports that go unused: `startDiscovery`, `startProbe`, `pollJobToTerminal`, `runWithConcurrency`, `probeTargets`, `toast`, and `Button` if nothing else in the file uses it

- [ ] **Step 2: Rewrite the copy that promises testing**

The dialog title, the header caption (`:268-272`) and the component doc block (`:85-105`) all describe a test console. Rewrite them to describe a report. The title becomes `Model Report: {providerName}`. The caption states what the surface now is — the models this account exposes, what each one can do, and where those facts came from. Delete the sentence beginning "Every other capability icon below". Also fix the empty state at `:334`, which names the deleted `Refresh Models` button; point it at the provider row's own fetch action instead.

- [ ] **Step 3: Delete the now-callerless helpers**

- `probeTargets` from `modelStatus.ts:126-134`, and its `describe` block in `modelStatus.test.ts:206-270`. Keep `PROBEABLE_OPERATIONS` (`:109-114`) only if something still imports it after Task 3; if not, delete it too and say so.
- `runWithConcurrency` from `jobs.ts:40`. Keep `pollJobToTerminal` — `AccountRow.tsx` and `ProviderRow.tsx` still use it.
- `startProbe` from `controlClient.ts:1245-1259` and the `StartProbeResult` type (`:1238-1241`) if nothing else references them.

- [ ] **Step 4: Delete the tests whose subject no longer exists**

From `ModelTestReport.test.tsx`: `:200` "Refresh Models runs discovery…" · `:213` "Test All probes EVERY probeable capability…" · `:230` "renders no clickable capability chip for a model with no probeable operation (chat-only)" · `:239` "renders one clickable capability chip PER probeable capability…" · `:248` "probes exactly the capability whose chip was clicked…" · `:260` "probes only the ONE capability clicked…"

- [ ] **Step 5: Add the test that pins the new contract**

```tsx
it("renders no test or refresh control, and no cost chip — the modal is a report", async () => {
  renderReport();
  await screen.findByText("cline-pass/kimi-k3");

  for (const label of [/refresh models/i, /test all/i]) {
    expect(screen.queryByRole("button", { name: label })).toBeNull();
  }
  expect(screen.queryByText(/cost unknown/i)).toBeNull();
  expect(screen.queryByText(/\bfree\b/i)).toBeNull();
  expect(screen.queryByText(/click one to run a test/i)).toBeNull();
  // The display controls stay.
  expect(screen.getByPlaceholderText(/search models/i)).toBeInTheDocument();
});
```

Adapt to the file's existing render helper and fixture ids.

- [ ] **Step 6: Verify, mutation-proof, commit**

Run: `npm --prefix dashboard test -- --run src/fleet` — including the `axe` case at `:192`.
Then `npm --prefix dashboard run build` and `check:ds-adherence`.

Mutation: restore the `Test All` button. The Step 5 test MUST fail. Remove it again.

```bash
git add dashboard/src/fleet/ dashboard/src/api/controlClient.ts
git commit -m "feat(dashboard): turn the Model Test Report into a Model Report

Removes Refresh Models, Test All, the clickable per-capability test
chips and the cost chip, and rewrites the copy that described the modal
as a test console. The search and status filters stay -- they are
display controls.

The cost chip could only ever render 'Cost unknown': all three sources
behind it are structurally dead, and the owner reads cost from the
provider, account and quota surfaces instead.

Also deletes probeTargets, runWithConcurrency and startProbe, which lose
their last callers here.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 3: Adopt the design system's labelled capability chip

The dashboard hand-rolls `CapabilityChips` with a local icon map and raw `vn-icon` classes. It renders **no text label**, so `vision` and `reasoning` are indistinguishable, and `context_window` is absent from the map so it falls through to an unlabelled grey `box`.

The design system already exports exactly what is needed — `CapabilityIcon` and `ModelCapabilitySet` (`Design_System/components/domain-model/ModelIntelligence.tsx:64-104`), icon **plus** text label, a `data-truth` treatment, and a `CAP_ORDER` that includes `context_window`. Neither is imported anywhere in `dashboard/src`. The design system's own CSS header states the rule the local component breaks: state is never communicated by colour alone; structures pair colour with an icon slot **and** a text label.

**Files:**
- Delete: `dashboard/src/fleet/CapabilityChips.tsx`, `dashboard/src/fleet/CapabilityChips.test.tsx`
- Modify: `dashboard/src/models/ModelsSurface.tsx` (call site `:250`), `dashboard/src/fleet/ModelTestReport.tsx` (call site `:382-388`)
- Modify: `dashboard/src/fleet/fleet.css`
- Modify: `dashboard/src/models/ModelsSurface.test.tsx`

**Interfaces:**
- Consumes: Task 2 removed `onTest`/`disabled`/`probingOperationId`, so both call sites now pass only `capabilities` and `cap`.
- Produces: both surfaces render `ModelCapabilitySet` / `CapabilityIcon` from `@venom/design-system/domain`.

- [ ] **Step 1: Invert the assertion that pins the absence of labels**

`ModelsSurface.test.tsx:363` currently asserts `expect(within(cell).queryByText("vision")).toBeNull();` inside the test at `:342` ("renders a capability as the shared icon-chip component, not its bare operation name as text").

That test encoded a deliberate earlier decision. It is being reversed on the owner's instruction after they could not tell one icon from another on the live instance. Rewrite it — do not silently delete it:

```tsx
it("renders each capability as a labelled chip, so the operation is readable without hovering", async () => {
  // Reversed 2026-08-06: this previously asserted the operation name was NOT
  // rendered as text. The owner, looking at the live fleet, could not tell
  // vision from reasoning from an icon alone. The design system's own rule is
  // that colour never carries state without an accompanying label.
  renderSurfaceWithCatalog();
  const cell = await screen.findByTestId(/* the existing capability-cell testid */);
  expect(within(cell).getByText("vision")).toBeInTheDocument();
});
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `npm --prefix dashboard test -- --run src/models/ModelsSurface.test.tsx -t "labelled chip"`
Expected: FAIL — no text node matches.

- [ ] **Step 3: Swap both call sites to the design system component**

`ModelsSurface.tsx:250` renders one capability per row, so use a single chip:

```tsx
<CapabilityIcon capability={capability.operation} truth={capability.truth} />
```

`ModelTestReport.tsx:382` renders the whole set, capped:

```tsx
<ModelCapabilitySet
  capabilities={offering.capabilities.slice(0, CAPABILITY_CHIP_CAP).map((c) => ({
    capability: c.operation,
    truth: c.truth,
  }))}
/>
```

Read `ModelIntelligence.tsx:97-104` for `ModelCapabilitySet`'s real prop shape and match it exactly; the mapping above is illustrative. Preserve the `+N` overflow indicator when `offering.capabilities.length` exceeds the cap — keep `vnd-capability-overflow-box` for that alone.

Import from `@venom/design-system/domain`, the subpath both files already use.

- [ ] **Step 4: Delete the local component and its CSS**

Delete `CapabilityChips.tsx` and `CapabilityChips.test.tsx`. From `fleet.css`, delete the capability-icon-box rules (`:942-975` and `:1169-1230`), keeping only `vnd-capability-overflow-box` if Step 3 still uses it. The `--vnd-cap-*` palette (`:925-940`) becomes unused — delete it too, and check nothing else references those variables first.

- [ ] **Step 5: Verify**

Run: `npm --prefix dashboard test -- --run`
Expected: PASS. Several `ModelsSurface` tests assert on capability rendering (`:151`, `:178`, `:195`, `:229`, `:266`) and `ModelTestReport.test.tsx:150` asserts "capability chips capped at 6 with +N overflow" — update their queries to the new markup where the assertion is about presentation, but keep every assertion about **which** capability and **what** truth intact. If an assertion cannot be preserved, stop and report BLOCKED rather than dropping it.

Then `npm --prefix dashboard run build` and `check:ds-adherence` — the latter specifically guards against vendoring design-system components, and this task moves in the opposite direction, so it must stay green.

- [ ] **Step 6: Mutation-proof**

Change the swapped call site to pass `showLabel={false}`. The Step 1 test MUST fail. Restore.

- [ ] **Step 7: Commit**

```bash
git add dashboard/src/ 
git commit -m "feat(dashboard): use the design system's labelled capability chip

CapabilityChips was hand-rolled, icon-only, and its local icon map had
no entry for context_window, so that operation rendered as an unlabelled
grey box. Looking at the live fleet, the owner could not tell vision
from reasoning.

The design system already exports CapabilityIcon and ModelCapabilitySet
- icon plus text label, a truth treatment, and a capability order that
includes context_window - and nothing in the dashboard imported them.
This deletes the local copy rather than adding labels to it.

One test is reversed, not deleted: it previously pinned the absence of a
text label. The reversal is commented at the test.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 4: Fix the context formatter in the design system

`ContextWindowDisplay` renders `1.048576M` for 1,048,576 tokens and `200K` for 200,000. The ≥1M branch has no rounding while the ≥1K branch does.

`Design_System/components/domain-model/ModelIntelligence.tsx:290`:

```tsx
const fmt = tokens >= 1000000 ? (tokens / 1000000) + "M" : tokens >= 1000 ? Math.round(tokens / 1000) + "K" : String(tokens);
```

**Files:**
- Modify: `Design_System/components/domain-model/ModelIntelligence.tsx`
- Test: the design system's own test file for this component — find it; if none exists, create one beside the component following the package's existing test conventions.

**Interfaces:**
- Consumes: nothing.
- Produces: unchanged component API. Only the rendered string changes.

- [ ] **Step 1: Write the failing test**

```tsx
it("formats a megatoken context to one decimal at most, never a raw quotient", () => {
  render(<ContextWindowDisplay tokens={1048576} verified />);
  expect(screen.getByText(/^1M$/)).toBeInTheDocument();
});

it("keeps a genuinely fractional megatoken context readable", () => {
  render(<ContextWindowDisplay tokens={1500000} verified />);
  expect(screen.getByText(/^1\.5M$/)).toBeInTheDocument();
});

it("still renders the exact count in the tooltip", () => {
  render(<ContextWindowDisplay tokens={1048576} verified />);
  expect(screen.getByTitle(/1,048,576 tokens/)).toBeInTheDocument();
});
```

The third test matters: rounding the display must not lose the exact number, which the `title` already carries.

- [ ] **Step 2: Run and confirm the first fails**

Expected: FAIL — receives `1.048576M`.

- [ ] **Step 3: Round the megatoken branch**

```tsx
  const fmt =
    tokens >= 1000000
      ? trimTrailingZero((tokens / 1000000).toFixed(1)) + "M"
      : tokens >= 1000
        ? Math.round(tokens / 1000) + "K"
        : String(tokens);
```

with, above the component:

```tsx
// trimTrailingZero turns "1.0" into "1" so a round megatoken count reads "1M",
// while "1.5" survives intact. The exact token count is never lost - the
// badge's title attribute carries tokens.toLocaleString().
function trimTrailingZero(value: string): string {
  return value.endsWith(".0") ? value.slice(0, -2) : value;
}
```

- [ ] **Step 4: Rebuild the design system bundle**

The dashboard consumes `Design_System/dist/*.mjs`, not the source. Run the design system's build script (read `Design_System/package.json` for its name — likely `npm --prefix Design_System run build`). Confirm the built output contains the rounded logic; the pre-change bundle contained `1e6 ? n / 1e6 + "M"`.

If `dist/` is committed to the repo, commit the rebuilt artifact with the source change — the dashboard build will otherwise keep the old string.

- [ ] **Step 5: Verify end to end**

Run the design system's tests, then `npm --prefix dashboard test -- --run` and `npm --prefix dashboard run build`. Any dashboard test asserting `1.048576M` must be updated to `1M`.

- [ ] **Step 6: Mutation-proof and commit**

Revert the `.toFixed(1)` to the raw quotient; the first test MUST fail. Restore.

```bash
git add Design_System/
git commit -m "fix(design-system): round the megatoken context to one decimal

ContextWindowDisplay rendered 1,048,576 tokens as '1.048576M' while
200,000 rendered as '200K' - the megatoken branch was the only one
without rounding, so the owner's fleet showed '1M' beside '1.048576M'
for two models with genuinely similar limits.

The exact count is unchanged in the badge's title attribute.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Task 5: Unify the two duplicated context-provenance marks and the rating copy

Two byte-identical copies of `ContextProvenanceMark` exist — `ModelsSurface.tsx:144-168` and `ModelTestReport.tsx:27-46` — the second carrying a comment that calls the duplication deliberate. The two surfaces also disagree on unrated copy: the page says `Not rated — unknown` with a dated tooltip, the modal says `Not rated` with a flat `Local benchmark` tooltip.

**Files:**
- Create: `dashboard/src/fleet/ContextProvenanceMark.tsx`
- Modify: `dashboard/src/models/ModelsSurface.tsx`, `dashboard/src/fleet/ModelTestReport.tsx`
- Test: `dashboard/src/models/ModelsSurface.test.tsx` (the existing provenance suite at `:367-468` must keep passing unchanged)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `export function ContextProvenanceMark(props: { tokens: number | null; provenance?: string })` — behaviour identical to both current copies.

- [ ] **Step 1: Extract, without changing behaviour**

Create `dashboard/src/fleet/ContextProvenanceMark.tsx` containing the exact current implementation, exported. Do not put it under a filename matching a design-system component — `check:ds-adherence` blocks that. Import it in both surfaces and delete both local copies along with the "duplicated locally on purpose" comment.

- [ ] **Step 2: Verify the existing provenance suite still passes untouched**

Run: `npm --prefix dashboard test -- --run src/models/ModelsSurface.test.tsx -t "provenance"`
Expected: PASS with **no edits** to those four tests (`:368`, `:394`, `:415`, `:441`). If any needs editing, the extraction changed behaviour — revert and redo.

- [ ] **Step 3: Unify the unrated copy**

Make both surfaces render the same string for an unrated model. Use the page's fuller wording, `Not rated`, on both, and give the modal the same dated `benchmarkProvenanceTitle` the page uses — move that helper (`ModelsSurface.tsx:197-225`, both `isoDay` and `benchmarkProvenanceTitle`) into the new shared file alongside the mark, or a sibling module if that reads better.

The modal's `EffectiveOffering` has no `latest_benchmark` — that field lives on `ModelGroup`, which the modal does not receive. So the modal calls the shared helper with `null`, which already yields the plain `"Local benchmark"`. Do not invent a date the modal cannot know.

Add:

```tsx
it("uses the same unrated wording as the Live Models page", async () => {
  renderReport();
  expect(await screen.findAllByText("Not rated")).not.toHaveLength(0);
  expect(screen.queryByText(/not rated — unknown/i)).toBeNull();
});
```

and update the page's tests to the unified string.

- [ ] **Step 4: Full verification**

Run: `npm --prefix dashboard test -- --run`, `npm --prefix dashboard run build`, `npm --prefix dashboard run check:ds-adherence`. All `axe` suites must stay green.

- [ ] **Step 5: Commit**

```bash
git add dashboard/src/
git commit -m "refactor(dashboard): one context-provenance mark, one unrated wording

The mark existed as two byte-identical copies whose comment called the
duplication deliberate; the two surfaces meanwhile disagreed on how to
say a model is unrated. Both now share one component and one string.

The modal passes null for the benchmark provenance because
EffectiveOffering carries no latest_benchmark - that field is on
ModelGroup, which the modal never receives. It renders the undated
wording rather than inventing a date.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Self-Review

**Coverage of the live findings.** Discover/Benchmark → Task 1. Probe → Task 1. Refresh Models/Test All → Task 2. Clickable chips → Task 2. Cost chip → Task 2. `1 offering` → Task 1. Census banner → Task 1 (mount only; the component survives for Overview). `Needs review` → Task 1. Icons without labels → Task 3. Missing `context_window` icon → Task 3, via the design system's `CAP_ORDER`. `1M` vs `1.048576M` → Task 4. Duplicated provenance mark → Task 5. Inconsistent unrated copy → Task 5.

**Deliberately not in scope.** `Not rated` itself stays on the cards: the score is produced by the spec's Phase 3, which is not built. Touching these components again for it is one edit, not two, because Phase 3 changes the field and the label together. `ReviewQueueBanner` stays alive for `OverviewSurface`. No Go file is touched.

**Ordering rationale.** Deletion precedes adoption: Task 3's chip swap is clean only after Task 2 has removed `onTest`, which is the sole reason the local component renders a `<button>` branch at all.

**Risk.** Task 3 is the largest, because several existing tests assert on capability markup. The instruction there is explicit: presentation queries may be updated, assertions about which capability and what truth may not, and a blocked assertion stops the task rather than being dropped.

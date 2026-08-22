# Catalog — the change ledger and the conflict verdict

Two contracts in `catalog/` that a reader has to know before touching the sync,
because both were reachable-looking and wrong, and both are load-bearing for
whether a number on screen can be trusted.

Written 2026-08-21, alongside the change that established them.

---

## 1. The change ledger compares the feed against the feed

`/v1/changes` promises, in its own subtitle: *a sync that finds nothing different
adds nothing here*.

The diff in `catalog/sync/engine.ts` compares each incoming `ModelSpec` against
`models.feed_tracked_json` — a JSON snapshot of what the FEED published for the
tracked fields on the previous sync, written by the engine and by nothing else.

**It must never compare against the `models` columns.** Those hold the RESOLVED
answer and the enrichment pass is authoritative over them:

- a subscription provider's per-token numbers are moved to `ref_cost_*` and the
  effective columns set to NULL (`cost_kind = 'included'`);
- a reviewed fact overrides the feed's output limit.

Comparing the feed against a column somebody else owns re-reported the same
difference on every run. Measured on the live database before the fix:
`cost_out_per_m null -> 1.6` recorded **13 times** for one ClinePass row with a
single distinct from/to pair, and **298 of 459** ledger events were this class of
false positive — which also meant a genuine ClinePass price change would have
been invisible in the noise.

`feed_tracked_json IS NULL` means no baseline was ever recorded. That reports
**nothing** and seeds the column: an unknown previous value cannot support a
claim that something moved. This is what makes the transition silent on an
existing database.

Adding a field to `TRACKED` therefore needs no migration — the snapshot is
rebuilt from `TRACKED` on every sync — but the first run after the change will
report nothing for the new field, by the same rule.

## 2. A human verdict on a source disagreement lives in the overlay

`model_conflicts.status` may be `open` or `resolved`, and `resolved_to` carries
the settled value. **The only thing that writes either is a reviewed fact in
`catalog/overlays/reviewed-facts.json`**, applied by `enrich()`.

Before this change nothing wrote them at all: all 128 live conflicts were `open`
and `resolved` was a state the schema allowed and no code path could reach.

The verdict is **derived, not latched** — re-read from the overlay on every run,
so withdrawing a review reopens the dispute. Do not add a second channel (a hand
edit to the column, a separate resolutions file). Every other decision a machine
may not make — identity, quality bounds, reviewed facts — lives in a cited file
under review; a value typed straight into the database has no citation and no
reviewer, and two channels for one verdict is the duplication rule in
[CLAUDE.md §7](../CLAUDE.md).

### Why this is not cosmetic

A disputed capability decides which dimensions a model is graded on. Live
example: `xiaomi/mimo-v2.5-pro` scored **85.9 %** at ClinePass and **74.6 %** at
OpenCode Go, both reported `status: complete` at `100 %` coverage — because
`attachment` was resolved `false` for one and `true` for the other, so `vision`
was applicable to one exam and not the other. `overallCoverage.percent` is
measured against the APPLICABLE dimensions, so a narrower test set certifies
itself as 100 %.

Two surfaces now state this rather than hiding it:

- `ScoreCell` shows `graded on N of M` whenever a dimension was excluded;
- the **Same model, which provider?** page refuses to name a best offer for a
  group whose rows were graded on different dimension sets, and says which
  dimension differed.

Settling the underlying dispute still needs a reviewed fact with a citation —
that is an owner decision, not a code change.

## 3. Where the numbers on the compare page come from

`catalog/src/api/cross-provider.ts` is pure derivation over the `/v1/models`
response: it groups offers by the `canonicalId` the sync already settled, and
adds no fact of its own. Rows with a null `canonicalId` are dropped rather than
pooled — grouping on that null would merge every unidentified row into one
fictional model, which is the guess the identity rules refuse to make.

The quality half of an overall score belongs to the model, not the seller, so
within a group it is identical by construction (verified: spread exactly 0.0 for
14 of the 15 live multi-provider models — the 15th is the `mimo-v2.5-pro` case
above). That is why a group's spread can be read as an operational difference,
and why a group that fails the same-dimensions check must not be read that way.

## 4. An evaluation identity is a permission, not only a name

`catalog/overlays/evaluation-identities.json` declares, per exact provider offer,
which identity local measurements may be attributed to — and whether evaluating
that offer needs the owner's consent (`not_required` | `required` | `granted`).

Two rules follow, and both are load-bearing:

**The precedence is stated once.** `resolveOfferIdentityId()` in
`catalog/sync/evaluation/identity.ts` is the only place that says *VQ source id,
then the recorded vendor identity, then the reviewed evaluation identity*. The
planner and the recalculation both call it. This order decides which offers share
a single body of evidence, so a second copy drifting would silently split or
merge evidence between offers — the duplication rule in
[CLAUDE.md §7](../CLAUDE.md) applied to the one ordering that changes what a
score means.

**Consent governs publication, not only acquisition.** `planEvaluation()` blocks
with `consent_required`, so no samples are bought. But a review can be tightened
*after* samples exist, and a score still standing on them publishes the same
claim the refusal was meant to prevent. So `recalculatePublishedOffers()` passes
`withheldReason: 'consent_required'`, and a withheld offer reads **no** evidence
— identity-level or offer-level — and stores `value: null` with that reason
leading. Nulling the identity alone was not enough: the offer-scoped rows would
still aggregate into a published number.

The reason leads the `reasons` array on purpose. `insufficient_evidence` with no
reason reads as "nobody has evaluated this yet", which is a different statement
from "we are not permitted to".

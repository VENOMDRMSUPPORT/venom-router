# Catalog data sources and update procedure

How every number in the model catalog gets there, and how to refresh it.

The short version: **Ollama Cloud is generated, everything else is hand-typed.**
Knowing which is which matters, because only one of those two can be trusted
without re-checking.

---

## 1. Why this exists

The catalog originally had every model's specs typed in by hand from
`ollama.com`. That drifted in three distinct ways, all found on 2026-08-11:

| Failure | Example |
|---|---|
| Values copied between sibling rows | `glm-5.2` showed `203K` context — carried over from `glm-5.1`. Real value: `976K`. |
| Numbers with no upstream source | `mistral-large-3:675b` showed `32K` max output. `ollama.com` publishes no output limit at all. Real value: `262K`. |
| Retired models left in place | 26 of the 46 listed models were no longer served by Ollama Cloud. |

Hand-entry is the root cause, so the fix is to stop hand-entering anything that
can be sourced.

---

## 2. Where the data comes from

`ollama.com` is the obvious source but **not a sufficient one** — it publishes
context length, parameter size and capability flags, but **no max output
tokens**. That single gap is what produced the invented numbers above.

So the catalog uses the same feed OpenCode uses:

```
https://models.dev/api.json     ->  provider key "ollama-cloud"
```

`models.dev` is itself generated from Ollama's own API
(`GET /api/tags` + `POST /api/show`) by
[`generate-ollama-cloud.ts`](https://github.com/anomalyco/models.dev), then
enriched with vendor-published output limits inherited from each model's
`base_model` entry. OpenCode consumes the same endpoint in
[`packages/core/src/models-dev.ts`](https://github.com/anomalyco/opencode).

### Every displayed field is sourced

| Field | Origin | Hand-editable? |
|---|---|---|
| `context` | `limit.context` | No — regenerated |
| `maxOutput` | `limit.output` | No — regenerated |
| `inputs` | `modalities.input` | No — regenerated |
| `capabilities` | `attachment`, `reasoning`, `tool_call`, `structured_output` | No — regenerated |

Nothing else. The overlay holds no model facts at all — only the withdrawn-model
list and documented source conflicts, neither of which a machine can derive.

### Structure mirrors the source: one flat list

models.dev exposes `ollama-cloud` as a single `models` map with **no tier, no
service level and no cost**. The catalog renders it as one table for the same
reason — any grouping we invented would be ours, not the provider's.

The catalog previously split these models into "Free Tier" and "Pro/Max Tier".
**That split was unsourced and has been removed.**
<https://ollama.com/pricing> gates plans by *usage volume and concurrency*, not
by which models you may call — the Free plan lists "Access cloud models" with no
qualification, and the differences between plans are allowance (50x, then 5x
again) and concurrent models (1 / 3 / 10). No per-model entitlement exists to
represent.

The `L1`–`L4` service level went with it. Ollama does publish a usage level per
model (`gpt-oss:20b` is level 1, `deepseek-v4-pro` is level 4), but it appears
only as a `low`/`medium`/`high` label on each model's HTML page and is absent
from models.dev. The values previously shown were inherited from the old
hand-typed catalog and never verified, so they were removed rather than
rendered with unearned confidence.

> Provenance caveat, stated once so nobody has to rediscover it: the output
> limits for Ollama Cloud are **vendor figures inherited by models.dev**, not
> values Ollama itself reports. They are the best available source, not a
> first-party one.

---

## 3. Updating

```bash
cd catalog
npm run sync:ollama
```

This rewrites `src/data/ollama-cloud.generated.ts` and prints a summary:

```
wrote 20 models to src/data/ollama-cloud.generated.ts
  retired: 26
  documented conflicts: 1
```

Then rebuild so the served bundle actually changes:

```bash
npm run build
```

> The dev server (`npm run dev`, port 5173) hot-reloads. `npm run preview`
> serves the static `dist/` build, so it shows stale data until you rebuild.

### Drift check for CI

```bash
npm run sync:ollama:check    # exit 1 if the generated file is out of date
```

This ignores the `Last verified` date, so it only fails on real data drift.

---

## 4. The one case that needs a human

**A new model appears in the feed.** Nothing to do — it is rendered from feed
fields alone, so it simply shows up on the next sync.

**A model disappears from the feed.** The sync succeeds but warns, comparing
against the `sourceId`s recorded in the previous generation:

```
!! Withdrawn from the feed since the last sync:
   - kimi-k2.6:cloud
Add them to "retired" in scripts/ollama-cloud.overlay.json.
```

Add the display name to the `retired` array. Retired models are listed by name
only in the page footer — deliberately without specs, because once a model is
withdrawn there is nothing left to verify against.

If the feed itself is unreachable or the `ollama-cloud` key vanishes, the script
**fails closed**: it exits non-zero and leaves the previous file untouched
rather than emitting a half-empty catalog.

---

## 5. Number formatting

`ollama.com` is neither consistently decimal nor consistently binary — it picks
whichever unit makes the number come out round. The formatter matches that,
because a user who reads `256K` on ollama.com and then `262K` here will
reasonably conclude the catalog is broken.

| Input | Rule | Output |
|---|---|---|
| exact multiple of 1000 | decimal | `976000` -> `976K`, `512000` -> `512K` |
| exact multiple of 1024 | binary | `131072` -> `128K`, `262144` -> `256K`, `202752` -> `198K` |
| anything else | decimal, rounded | `196608` -> `197K` |

Verified against ollama.com on 2026-08-11: `gpt-oss` 128K, `glm-5.1` 198K,
`glm-5.2` 976K, `nemotron-3-super`/`-ultra` 256K, `gemma4:31b` 256K,
`qwen3.5:397b` 256K, `minimax-m3` 512K, `kimi-k3` 1M. All match.

The `contextSize` field is a **cosmetic weight bucket only** (`1m`, `500k`,
`256k`, `200k`, `164k`, `128k`) used for table styling. It is derived from the
raw context value and carries no meaning of its own.

---

## 5b. Floating tags (aliases)

Ollama publishes some models under both a pinned tag and a floating alias that
follows the current release — the same artifact under two names. models.dev
carries both as separate entries and has no concept of aliasing, so the
relationship is recorded in the overlay's `aliases` map and rendered as a small
`alias of …` line under the model name.

Currently one: `deepseek-v4-flash:cloud` resolves to
`deepseek-v4-flash:0731-cloud`. Both list digest `031ce2a95446` on
<https://ollama.com/library/deepseek-v4-flash/tags> — identical digest is the
proof, and re-checking that page is how you re-verify or discover new ones.

The alias is shown rather than hidden: it is a real, callable model name, and
collapsing it would misrepresent what Ollama serves. But a user comparing two
rows with identical specs deserves to know they are the same file.

An alias entry is ignored if its target leaves the feed, so a stale mapping
cannot silently mislabel a row.

---

## 5a. Known source conflicts

The two sources are not always reconcilable. Confirmed disagreements live under
`conflicts` in the overlay and are rendered in the page footer rather than
silently resolved — a quietly picked winner is indistinguishable from a bug.

**`minimax-m2.7`** — ollama.com shows `200K`; models.dev reports `196608`
(= 192Ki), which renders as `192K`. No reading of "K" reconciles them, so this
is a real data conflict, not a rounding artifact. Notably, ollama.com's implied
raw value (204800) is bit-for-bit the value models.dev lists for
`minimax-m2.5` — consistent with a stale carry-over on Ollama's side.

We keep the smaller figure. Over-stating a context window is the failure mode
that breaks live requests; under-stating one only wastes headroom.

---

## 6. Other providers

Every provider other than Ollama Cloud is still hand-maintained in
`src/data/catalog.ts`, last checked against the legacy HTML pages on 2026-08-10.
They have no `lastVerified` date, and the page footer says so explicitly instead
of implying a machine check that never happened.

Porting another provider to this pattern means finding a machine-readable feed
for it first. `models.dev` covers 183 providers, so for most of them the same
script generalises — the per-provider work is the curated overlay, not the fetch.

---

## 7. Files

| Path | Role |
|---|---|
| `catalog/scripts/sync-ollama-cloud.mjs` | Fetch, transform, generate |
| `catalog/scripts/ollama-cloud.overlay.json` | Retired-model list + documented source conflicts |
| `catalog/src/data/ollama-cloud.generated.ts` | Generated — do not edit |
| `catalog/src/data/catalog.ts` | Hand-maintained providers; imports the generated Ollama slice |
| `catalog/src/components/DataProvenance/` | Page footer showing source, date, refresh command |

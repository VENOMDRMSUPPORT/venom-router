# CLAUDE.md - binding rules for catalog/

## Scope and source of truth

This directory is the Venom Catalog product. Catalog is a standalone application and
is the sole source of truth for model inventory, model facts, provenance, freshness,
and catalog scoring consumed by Venom Router. Router may read the Catalog API but
must not open the Catalog SQLite database, duplicate Catalog derivation logic, or
be required to run for Catalog development, testing, syncing, or serving.

The default local endpoints are:

| Product surface | Endpoint |
|---|---|
| Catalog UI | `http://127.0.0.1:5173/` |
| Catalog API | `http://127.0.0.1:8791/v1` |
| Venom Router control plane | `http://127.0.0.1:8081` |

The UI and API ports are intentionally separate from the Router control-plane port.
A deployment may expose the UI under a reverse-proxy prefix such as `/catalog`, but
that is a publishing choice and does not make Catalog part of Router.

This directory is the Venom Catalog product. Do not apply Go control-plane,
`task gate`, PE-subsystem, or unrelated Design_System rules to catalog work.
The repository root `CLAUDE.md` still applies where it does not conflict with
this catalog contract.

## Language and verification

Every file committed under `catalog/` is English-only: code, comments, docs,
tests, fixtures, and commit-facing text. Chat with the owner may be Arabic.

Run this gate from `catalog/` and report every command separately:

```bash
npm run typecheck && npm run test:backend && npm run test:spa && npm run build
```

A green SPA run does not replace backend tests. A passing TypeScript diagnostic
with an alternate loader does not replace a working `test:backend` script. When
reporting a failed command, include `node --version` and the absolute working
path. If the failure cannot be reproduced in the stated environment, drop the
finding rather than acting on an older report.

Do not redirect gate output into `catalog/`. The root `.gitignore` hides `*.log`,
so these files never reach a diff or a review and simply accumulate — seventy of
them, 1.5 MB, had collected by 2026-08-24. Read the command output directly, or
send it to the session scratchpad directory outside the project.

`test:backend` runs `.ts` files directly under `node --test`, which relies on
the runtime stripping types with no loader. That is why `package.json` pins
`engines.node`: on an older runtime the script fails for a reason that has
nothing to do with the code under test. Report the runtime before reporting the
failure.

## Runtime and single writer

The catalog service uses Native SQLite through `node:sqlite` and
`DatabaseSync`. The service binds to `127.0.0.1` intentionally; do not add a
bind-address switch or expose it on a network interface.

The service is the single database writer. Every terminal batch that opens the
catalog database must pass the service guard, or the operation must be driven
through the service API. Batches open the database through `openBatchDb` in
`scripts/batch-db.ts`, which wires the guard once so a new script cannot omit
it. Keep in mind that the guard is a heuristic for the default port, not proof
against every non-default service instance.

## Provenance and lifecycle

Never physically delete model rows or model events. Model lifecycle is expressed
through the existing states (`active`, `missing`, `retired`, and `excluded`).
Every displayed fact or derived number must retain its source, source reference,
source URL where applicable, evidence state, resolver/methodology version, and
resolution/computation time.

A fallback may be stale only when it is explicitly identified as a snapshot. It
must never pretend to be a live answer. Unknown is an honest result and must
remain unknown.

### Lifecycle invariants

The catalog tracks **provider-declared existence**, not runtime model health. A
successful roster fetch is the provider's existence declaration: an offer present
in the roster is `active`, and an offer absent from that successful roster is
`retired` on the same run. A failed or quarantined fetch is not a roster
statement and changes no model lifecycle state.

A conflict requires **two entitled sources** for the same offering to publish
materially different values, with no standing rule that settles the difference. A
single declaration, a missing declaration, or a source that has no standing is not
a conflict.

A plan or mode listing (`:thinking`, `:free`, and the rest of `PLAN_VARIANT`)
describes a different offering of the same weights, so it never votes on the base
offering's facts. It is excluded once, before any standing rule is applied, and it
answers only for its own mode row when the serving seller is the one publishing it.
When every declaration for a field is a mode variant, the field is unknown - not
conflicted: nobody answered for the base offering.

A model is **measured once**. Existing measurements, scores, and evidence are
kept on routine syncs; a new model is measured only after it is inserted, and an
explicit human or maintenance action is required to re-run measurement. Enriching
stored facts after a resolver correction is re-derivation, not measurement or
scoring.

| Sync result | Model state transition | Measurement and score effect |
|---|---|---|
| Successful roster, id present | insert as `active`, or keep/re-activate `active` | New rows may be measured; existing rows keep stored data |
| Successful roster, prior id absent | `retired` immediately; preserve row and events | No probe, evaluation, or re-score |
| Failed or malformed roster | no lifecycle transition | No model row, fact, or score changes |
| Quarantined removal | no lifecycle transition | No model row, fact, or score changes |

`missing` is not on the default path. First-miss retirement moves an absent offer
straight to `retired`, so `missing` - and the `readded` event that leaves it - is
reachable only for rows written before this rule, or when `retireAfterMisses` is
deliberately configured above 1. Both remain supported and tested; neither is
produced by a default sync.

## Evidence and scoring

`unrated` means unknown quality; it is not low-rated and it must not be lowered
to zero or placed at the end of a quality ranking. VQ, VO, model score, and
overall score remain server-owned semantics. The client normalizes absence for
rendering but never recomputes or fabricates a score.

The model API keeps the complete conflict history in `conflicts`, including rows
whose reviewed verdict is `resolved`. The server derives `openConflicts` as the
only current-work view; counts, field-state badges, and missing-fact explanations
must use that field. Resolved history remains visible in evidence panels for audit,
but must not count as an active conflict or make a field look unresolved.

## Provider architecture

Provider adapters declare data sources, parsers, billing/access policy, and
publish exclusions. Shared fetch, retry, validation, delta gates, transactions,
provenance, enrichment, and scoring stay in shared pipeline code. Do not add a
switch on provider slug or model name when a typed adapter capability or policy
can express the behavior.

A roster listing is not proof of free access. Publish policy must fail closed,
keep excluded history, and record the reason. A provider-specific detail fact
must not be copied to another provider offering.

## HTTP boundary

Keep request bodies bounded, validate query limits at the boundary, return typed
generic errors, and do not expose credentials, raw upstream payloads, query
secrets, or filesystem details. A generic response body is not a licence to
discard the error: log it server-side, where the service holds no secrets, so a
500 still leaves a trace.

Any cross-origin policy must be explicit and tested; do not use wildcard CORS
for write-capable local endpoints. The SPA reaches the service through the Vite
proxy in development, so the service needs no CORS header at all.

## Change discipline

Read the actual files and current git target before classifying a defect. Update
catalog documentation and tests in the same change as behavior. Prefer one
mechanism, one data path, and deletion of duplication over new abstractions.
Do not add a dependency when the pinned Node runtime or existing project code
can provide the needed behavior.

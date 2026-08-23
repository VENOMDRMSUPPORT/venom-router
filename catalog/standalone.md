# Venom Catalog Standalone Contract

Venom Catalog is an independent product and the sole source of truth for model inventory, model facts, provenance, freshness, and catalog scoring consumed by Venom Router.

## Local surfaces

| Surface | Default endpoint | Owner |
|---|---|---|
| Catalog UI | `http://127.0.0.1:5173/` | Catalog frontend |
| Catalog API | `http://127.0.0.1:8791/v1` | Catalog service |
| Venom Router control plane | `http://127.0.0.1:8081` | Venom Router |

The Catalog UI and API run as separate processes. Catalog does not require Venom Router to be running for development, syncing, evaluation, serving, or testing.

## Ownership rules

Venom Router consumes Catalog through its API. It must not open the Catalog SQLite database, create a second model roster, duplicate Catalog scoring or provenance logic, or write Catalog-owned state.

Catalog owns the live provider roster, model facts, source evidence, freshness state, change history, and server-derived scores. Unknown values remain unknown. A snapshot is explicitly identified as a snapshot and must not be presented as live data.

## Port and binding rules

The Catalog API binds to `127.0.0.1` and defaults to port `8791`. The Catalog UI defaults to port `5173`. Venom Router uses a separate control-plane port, currently `8081`. These defaults are validated by `scripts/standalone-contract.test.ts`.

A deployment may publish the standalone UI under a reverse-proxy prefix such as `/catalog/dashboard`. That prefix is a deployment concern. It does not merge the Catalog process, database, or API ownership into Venom Router.

## Development commands

Run from this directory:

```bash
npm run dev
npm run serve
npm run sync
npm test
```

The Vite development server proxies `/v1` requests to the Catalog API. Override the UI port with `PORT` and the API target with `CATALOG_API` when an isolated verification instance is required.

## Integration expectations

Router integrations should use the Catalog API base URL and record the Catalog response version or generation timestamp used by a routing decision. A future contract version should be introduced when the wire shape or ownership semantics change; consumers must fail closed on unsupported versions rather than inventing defaults.

## Operational alert lifecycle

Catalog owns the operational alert ledger. The dashboard reads `GET /v1/alerts`, which reconciles the current health response into durable alert records before returning them. Each record has a stable `id`, a server-owned `severity`, optional `providerId` and `modelId` targets, and one of three statuses: `open`, `acknowledged`, or `resolved`.

An operator may transition a known alert with `PATCH /v1/alerts/:id` and a JSON body such as `{ "status": "acknowledged" }`. The service rejects unknown statuses with HTTP 400 and unknown alert IDs with HTTP 404. A problem that disappears from the candidate set is automatically marked `resolved`; if the same stable alert identity returns later, Catalog reopens it as a new active occurrence while preserving its timestamps.

`occurrenceCount` counts how many separate times a condition arose — it advances on a reopen, not on a read. `lastSeenAt` is the field that means "still true as of", and it advances on every reconcile.

The service reconciles its own ledger every 30 seconds, so alerts are raised and notifications queued whether or not a browser is open. `GET /v1/alerts` also reconciles, so a dashboard poll and the service tick cannot report different ledgers.

### Model lifecycle alerts

The ledger covers two independent families. Service health contributes `service_degraded`, `stale_provider`, `sync_failure`, and `sync_in_flight`. Recorded roster changes contribute `model_added`, `model_readded`, `model_retired`, `model_became_missing`, `model_excluded`, and `model_quality_lost`, classified by the same code that serves `GET /v1/changes` so the bell and the change feed cannot describe one event differently.

Roster alerts are **grouped** by change class, provider, observation time and reason, because that is the shape the underlying decision had: one publish-policy sweep that withholds eleven models is one alert naming the count and the reason, not eleven. `modelId` is set only when the alert concerns exactly one model, and is dropped when the referenced model row is absent so a dangling event cannot abort the reconcile.

A roster alert stays a candidate for seven days after the event, matching the dashboard's default change window, then resolves on its own. Acknowledging one removes it from the open list immediately without waiting for the window to close. Metadata changes — price, context, capability, score movement — are deliberately **not** alerts; they are reported on the change feed, because an alert list that reports everything reports nothing.

## Automatic evaluation

After every sync, each **active** offer is re-planned and queued if anything measurable is still missing. The sweep is not restricted to what the run just discovered: that restriction left an already-published offer with an unmeasured dimension unmeasured forever, because nothing asked about it again. The plan is the filter — an offer with nothing left to measure costs nothing and reports `already_covered`, so a sweep over a complete catalog is pure database reads.

Only providers whose refresh returned `ok` contribute offers, and only rows with `status = 'active'`. A quarantined roster is not trusted enough to write a catalog row from, so it is not trusted enough to spend a provider request on; a `missing` model is one the provider is not currently serving, so a request for it would be spent rediscovering that.

| Variable | Default | Meaning |
|---|---|---|
| `CATALOG_AUTO_EVALUATION` | on | Set to `false` to stop queueing. Any other value leaves it on, so a typo cannot silently stop measurement. |
| `CATALOG_AUTO_EVALUATION_MAX_REQUESTS` | *(none)* | Request ceiling per sync. **Absent means no ceiling** — the goal is a complete catalog, and a ceiling that stops short of it only defers the same spend. A value that does not parse is treated as absent for the same reason. `0` spends nothing while leaving the reporting intact. |
| `CATALOG_AUTO_EVALUATION_RETRY_HOURS` | `24` | How long an identity is left alone after a measurement attempt. `0` disables the guard. |

### Why there is a retry cooldown even with no ceiling

A plan stays incomplete when a dimension was attempted and produced no verdict. `x-ai/grok-4.5`/vision was attempted on 2026-08-23 at 09:45 and returned `insufficient_evidence: incomplete_valid_scenarios`; `xiaomi/mimo-v2.5-pro`/vision did the same at 10:21. With an uncapped sweep on a six-hourly schedule and nothing else in the way, those dimensions would be re-bought four times a day, indefinitely, for a verdict that asking again immediately does not produce.

The cooldown is keyed on the **identity**, because that is what the evidence belongs to: two sellers of one model share both the measurement and the reason it is not yet worth re-buying. It reads `evaluation_runs`, so it is anchored to what the service actually did rather than to a separate counter — and an unfinished run counts by its start time, so a service killed mid-evaluation does not hand the next sync a free re-buy.

### What "complete" means

Full **coverage**, not a particular score: every *applicable* dimension has a verdict. A dimension the offer does not support is excluded from coverage rather than counted as unsatisfied. In the live catalog 22 of 38 identities lack a `vision` or `structuredOutput` score for exactly that reason — the models have no such capability, and testing one would produce a number with no meaning. Those identities are complete.

Offers are queued cheapest-first. With no ceiling that does not change what gets bought, but it decides what reaches the provider first, so a run interrupted halfway has still closed as many dimensions as it could. The ordering is stable, so two runs over the same roster make the same choices.

Every offer not queued is named with a typed reason: a `blocked` plan reports the planner's own reason (`missing_credentials`, `consent_required`, `identity_unresolved`, `model_not_found`), a fully covered offer reports `already_covered`, one inside its window reports `retry_cooldown`, and anything above a configured ceiling reports `over_budget` and is retried next run. `POST /v1/sync` returns the whole report as `autoEvaluation`.

## Active-alert notifications

Outbound notifications are disabled by default. To enable them, set `CATALOG_ALERT_NOTIFICATIONS=true`, `CATALOG_ALERT_WEBHOOK_URL`, and optionally `CATALOG_ALERT_WEBHOOK_SECRET`. Catalog emits signed JSON events for `opened`, `reopened`, `acknowledged`, and `resolved` transitions. The `x-catalog-signature` header is an HMAC-SHA256 digest of the exact request body when a secret is configured.

Delivery is performed by the standalone Catalog process from a durable SQLite queue. Each attempt records its HTTP status or sanitized error, retries with exponential backoff, and becomes `failed` after the configured maximum number of attempts. A delivery failure never changes the underlying alert state or catalog facts. `GET /v1/alerts` includes the notification delivery records for each alert so the Dashboard can distinguish pending, delivered, retrying, and failed notifications.

## Header notification center

The catalog header reads only the authoritative `GET /v1/alerts?status=open` alert ledger. On a provider route, the visible badge and list are scoped to that provider; elsewhere, they show the catalog-wide open-alert set. The bell never derives alerts from client-side health data or change history.

Acknowledging a notification calls `PATCH /v1/alerts/:id` with `{ "status": "acknowledged" }`. The popover removes only alerts that the service acknowledges successfully, and retains a failed item with a visible error instead of claiming it was handled. The same state transition is used for individual and bulk acknowledgement.

While the catalog page is open, the notification center refreshes its open-alert list every 30 seconds only when the document is visible, and refreshes immediately when focus returns. This is a client-side polling enhancement, not a background worker: closing the page clears the interval and aborts in-flight reads. The catalog currently exposes a durable read-and-transition alert API rather than a browser event stream, so polling keeps the implementation aligned with the existing service contract without creating a second real-time channel.

## Provider table reading conventions

Provider tables present the server-owned overall score as the primary comparison value. A normal page-local position renders as `#N`; `T-N` denotes a tied position, meaning the service found overlapping uncertainty intervals and does not claim a strict order. Input and output columns are normalized to USD per one million tokens. `Market ref` identifies a comparison rate from elsewhere and is never the provider charge.

Capability cells display up to four reported capabilities, then a `+N` indicator with an accessible explanation of additional items. The `Evidence` control opens the provenance trail, while an amber count identifies only outstanding work rather than settled findings. These display rules change no catalog facts, scores, or lifecycle state.

The capability legend contains eight distinct concepts arranged in an equal responsive grid: four columns on desktop, two columns below 980px, and one column below 560px. `Vision` means image and visual comprehension; `Image Gen` is a separate pink-accented legend item for native image creation and editing. The legend describes model properties but does not itself assert that every provider reports an Image Gen field.

## Provider Grid-card reading order

The Grid view is a decision-card presentation rather than a miniature table. Each card reads in a fixed order: callable model identity and rank, overall score, four core comparison facts, reported capabilities, then evidence and evaluation actions. The four facts use one shared visual band with internal dividers instead of four nested cards, so cards retain the same hierarchy when prices, context limits, or lifecycle markers vary.

Cards share a minimum desktop height and pin their action footer to the bottom. At narrow desktop and tablet sizes the grid still preserves equal card regions; below 700px it becomes a single-column list. Below 440px, core facts move to a two-by-two grid and footer actions expand into stable touch targets. These layout rules do not alter model ranking, pricing, capability facts, or evidence state.

## Provider view transitions

Pointer-triggered switches between Table and Grid use a 200–260ms opacity-and-transform stage. Entering Grid adds a short 35ms card cadence so the card collection reads as one composed change rather than a sudden wall of content. The implementation uses only `opacity` and `transform`, clears any pending timer when a user switches again, and does not retain stale content during the change.

Keyboard-triggered view changes are immediate, and the stylesheet disables transition rules unless the browser reports `prefers-reduced-motion: no-preference`. The layout controls and model data remain available throughout; the animation is confirmation, never a delay or prerequisite for interaction.

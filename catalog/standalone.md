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

### Scoped conflict re-derivation

After a resolver correction, `npm run rederive:conflicts` re-derives only active or missing offerings that already have a recorded conflict. It refreshes model facts and conflict rows from the shared source feeds while deliberately skipping provider roster sync, provider detail calls, probes, evaluation, and scoring. The command opens the database through the single-writer batch guard and can target another database with `-- --db=<path>`.

## Integration expectations

Router integrations should use the Catalog API base URL and record the Catalog response version or generation timestamp used by a routing decision. The current advertised contract is **`catalog-api-v2`**. Version 2 replaces the old `/v1/alerts` lifecycle wire shape with `/v1/notifications` read history; `GET /v1/alerts` returns HTTP 410 with the explicit replacement endpoint. Consumers must fail closed on unsupported versions rather than inventing defaults.

## Catalog notification history

The bell and dashboard read `GET /v1/notifications`. A notification is an immutable, idempotent projection of one durable source record: `model_added`, `model_retired`, or `fetch_problem`. Success, error, and warning icons communicate the category without presenting routine catalog events as an operator incident queue.

The reconciler scans durable `model_events` and failed or quarantined `sync_runs` in deterministic batches, using a unique source key for every inserted notification. It does not use the public `GET /v1/changes` limit, so a catalog with more than 500 changes does not lose a notification. Re-running the reconciler never creates a duplicate, and a notification remains in history after the user reads it.

Read state is a user-facing preference only. `PATCH /v1/notifications/read` marks supplied notification IDs — or all unread records when no IDs are supplied — as read. There are no acknowledge, resolve, reopen, severity-filter, or delivery-status controls in the catalog UI.

## Automatic evaluation

After a sync, automatic evaluation receives only **newly added active offers** from providers whose refresh returned `ok`. Existing models are never automatically re-evaluated by a routine poll or later unchanged sync; their explicit `Evaluate` action remains the manual route for a re-run.

Planning is keyed by canonical identity. A new provider offer that already maps to an identity with complete evidence is reported as `already_covered`; two new offers of the same identity cannot both queue full work. Retired rows are never submitted to automatic evaluation, while their model row, events, scores, and evidence remain preserved for history.

| Variable | Default | Meaning |
|---|---|---|
| `CATALOG_AUTO_EVALUATION` | on | Set to `false` to stop queueing. Any other value leaves it on, so a typo cannot silently stop measurement. |
| `CATALOG_AUTO_EVALUATION_MAX_REQUESTS` | *(none)* | Request ceiling per sync. **Absent means no ceiling** — the goal is a complete catalog, and a ceiling that stops short of it only defers the same spend. A value that does not parse is treated as absent for the same reason. `0` spends nothing while leaving the reporting intact. |
| `CATALOG_AUTO_EVALUATION_RETRY_HOURS` | `24` | How long an identity is left alone after a measurement attempt. `0` disables the guard. |

The cooldown still protects a just-added identity from duplicate work, and `POST /v1/sync` returns the automatic evaluation report with every skip reason. Outbound alert webhooks remain an optional backend-only feature when explicitly configured. Disabled webhook configuration creates no pending delivery rows, and the UI never makes delivery-state claims.

## Header notification center

The header fetches the authoritative notification history every 30 seconds only while the document is visible and refreshes on focus. A provider route scopes the history to that provider. The badge shows unread count; `Mark all as read` changes read state but never removes history or mutates a catalog fact.

## Provider table reading conventions

Provider tables present the server-owned overall score as the primary comparison value. A normal page-local position renders as `#N`; `T-N` denotes a tied position, meaning the service found overlapping uncertainty intervals and does not claim a strict order. Input and output columns are normalized to USD per one million tokens. `Market ref` identifies a comparison rate from elsewhere and is never the provider charge.

Capability cells display up to four reported capabilities, then a `+N` indicator with an accessible explanation of additional items. The `Evidence` control opens the provenance trail, while an amber count identifies only outstanding work rather than settled findings. These display rules change no catalog facts, scores, or lifecycle state.

The capability legend contains eight distinct concepts arranged in an equal responsive grid: four columns on desktop, two columns below 980px, and one column below 560px. `Vision` means image and visual comprehension; `Image Gen` is a separate pink-accented legend item for native image creation and editing. The legend describes model properties but does not itself assert that every provider reports an Image Gen field.

## Provider Grid-card reading order

The Grid view is a decision-card presentation rather than a miniature table. Each card reads in a fixed order: callable model identity and rank, overall score, four core comparison facts, reported capabilities, then evidence and evaluation actions. The four facts use one shared visual band with internal dividers instead of four nested cards, so cards retain the same hierarchy when prices, context limits, or lifecycle markers vary.

Cards share a minimum desktop height and pin their action footer to the bottom. At narrow desktop and tablet sizes the grid still preserves equal card regions; below 700px it becomes a single-column list. Below 440px, core facts move to a two-by-two grid and footer actions expand into stable touch targets. These layout rules do not alter model ranking, pricing, capability facts, or evidence state.

## Opening view for a model list

The Dashboard and the Provider page decide which view to open in through one
shared rule, `useModelView`. Precedence is the reader's explicit switch, then the
`defaultView` preference saved in Settings, then the viewport width at first
render: below 768px a reader who expressed no preference opens in Grid.

The viewport supplies a default, never an override. Neither page re-applies the
width rule after mount, so narrowing a window or rotating a device does not move
a reader off the view they chose, and the view switcher never reports a view the
page is not showing. Table remains reachable at every width — the table wrapper
is a horizontal scroll region, and the stylesheet tunes it for touch below 768px.

## Provider view transitions

Pointer-triggered switches between Table and Grid use a 200–260ms opacity-and-transform stage. Entering Grid adds a short 35ms card cadence so the card collection reads as one composed change rather than a sudden wall of content. The implementation uses only `opacity` and `transform`, clears any pending timer when a user switches again, and does not retain stale content during the change.

Keyboard-triggered view changes are immediate, and the stylesheet disables transition rules unless the browser reports `prefers-reduced-motion: no-preference`. The layout controls and model data remain available throughout; the animation is confirmation, never a delay or prerequisite for interaction.

## Database Browser

The local Catalog UI exposes the read-only Database Browser at `/database`. It is an inspection surface for the Catalog database and is not a general SQL console. The service opens a separate SQLite connection in read-only mode for file-backed databases; in-memory test instances use the same connection with an authorizer installed only for the synchronous query and cleared in `finally`.

| Endpoint | Method | Contract |
|---|---|---|
| `/v1/db/tables` | `GET` | Returns non-internal Catalog tables as `{ tables: [{ name, sql }] }`. |
| `/v1/db/schema?table=<name>` | `GET` | Returns the exact known table schema, including columns, indexes, and foreign keys. Unknown table names return `404`; missing or malformed requests return `400`. |
| `/v1/db/query` | `POST` | Accepts `{ sql, limit }` and returns `{ columns, rows, rowCount, truncated, limit }`. |

Database Browser queries accept exactly one `SELECT` statement or a read-only CTE. SQLite's authorizer rejects inserts, updates, deletes, schema changes, transactions, pragmas, extension loading, attach/detach operations, and other non-read actions. The service also rejects multiple executable statements, bounds SQL text to 64 KiB, and requires `limit` to be an integer from `1` through `1000` (omitted `limit` defaults to `100`). Queries read only `limit + 1` rows, so `truncated` is true only when at least one additional row exists; `rowCount` is the number of rows returned in the response.

Rows are represented as value arrays rather than objects so duplicate result-column names are preserved. `NULL` remains JSON `null`; large integers are encoded as `{ "type": "bigint", "value": "..." }`; and BLOBs are encoded as `{ "type": "blob", "value": "<base64>", "bytes": N }`. Internal SQLite errors are logged by the service but are returned to the browser only as a typed generic error. The browser keeps the latest 50 query records in local storage, supports cancellation of stale requests, and exports the typed result payload as JSON.

The query connection uses SQLite's VDBE operation budget and a one-second lock timeout to prevent pathological statements or lock waits from holding the service indefinitely. The API remains local-only and does not change the `catalog-api-v2` contract.

## Theme and motion preferences

The Catalog Dashboard supports two browser themes: `dark` and `light`. `dark` is the default when no valid preference is available. The selected theme is stored under the `catalog-theme` local-storage key; the Settings record also carries the same theme for compatibility, but the runtime state and document application are owned by the single `ThemeProvider` mounted at the application root.

Header and Settings use that same provider, so changing either control updates the other immediately without changing the current route, re-fetching Catalog data, or resetting notifications. The provider applies both `data-theme` and the document `color-scheme` together. Invalid or unavailable storage values fall back safely to `dark`, and storage events synchronize changes from another browser tab without writing the event back.

Settings stores the explicit `reduceMotion` preference in `venom-catalog-settings`. The runtime also observes `prefers-reduced-motion: reduce`; either source enables `data-reduce-motion="true"`. Reduced motion disables non-essential transitions and animations and changes Settings section scrolling to an immediate jump. Theme switching uses a generation-guarded transition blocker with owned animation-frame cleanup, so rapid toggles and unmounts cannot leave stale blockers, style tags, timers, or listeners behind.

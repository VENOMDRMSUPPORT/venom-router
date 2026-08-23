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

An operator may transition a known alert with `PATCH /v1/alerts/:id` and a JSON body such as `{ "status": "acknowledged" }`. The service rejects unknown statuses with HTTP 400 and unknown alert IDs with HTTP 404. A problem that disappears from the health response is automatically marked `resolved`; if the same stable alert identity returns later, Catalog reopens it as a new active occurrence while preserving its occurrence count and timestamps.

The Dashboard is intentionally read-only with respect to catalog facts. Acknowledge, resolve, and reopen actions change only the operational alert ledger; they never change provider data, model facts, scores, freshness, or the Catalog release metadata.

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

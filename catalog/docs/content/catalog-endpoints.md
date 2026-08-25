# Catalog API Endpoints

The endpoints below are the current stable Catalog-facing routes for the V1 documentation. All paths are relative to the `/v1` API base URL.

## Inventory and metadata

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/providers` | Read provider offerings and catalog metadata |
| `GET` | `/models` | Read the current model inventory; supports provider and evidence filters |
| `GET` | `/models/{providerId}/{modelId}/provenance` | Read the stored provenance for a model value |
| `GET` | `/models/{providerId}/{modelId}/evaluation` | Read sanitized evaluation diagnostics and the current evaluation plan |
| `GET` | `/changes` | Read deterministic catalog changes with a bounded limit and cursor |
| `GET` | `/notifications` | Read immutable catalog notification history |
| `PATCH` | `/notifications/read` | Update user-facing notification read state |
| `GET` | `/health` | Read service and catalog health; this is a local operational endpoint |

## Models

A model response is server-owned. The client should render the returned scores, evidence, lifecycle, and reasons without recomputing them. Use the `providerId` and `modelId` returned by `/models` to address a model-scoped endpoint.

```text
GET /v1/models?provider=acme&evidence=measured
```

Retired offerings are excluded by default unless the request explicitly asks to include them.

## Provenance

The provenance endpoint explains the source value and its transformation. An unrated model may correctly return `404` for a value that has no provenance to offer; this is not evidence that the model row was deleted.

## Notifications

Notifications are immutable history projections for catalog events such as `model_added`, `model_retired`, and `fetch_problem`. They are not an outbound delivery queue and do not have an acknowledge, resolve, or webhook lifecycle.

## Local diagnostic surface

The `/db/tables`, `/db/schema`, and `/db/query` routes are intentionally excluded from this stable public API table. They belong to the local Database Browser and should be described, if needed, as a **local diagnostic surface, not a stable integration contract**.

See [Errors and Pagination](/api/errors-and-pagination) for bounded query behavior and [API Overview](/api/overview) for the contract header.

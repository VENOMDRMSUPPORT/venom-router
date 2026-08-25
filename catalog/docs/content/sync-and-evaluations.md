# Sync and Evaluations

Sync and evaluation routes are local control operations in the current Catalog service. They are not presented as a hosted public API.

## Start a sync

```http
POST /v1/sync
```

The service runs one sync at a time. If a sync is already running, the route returns HTTP `409` rather than starting a parallel run. A failed or quarantined provider fetch does not change model lifecycle state.

## Inspect evaluation state

```http
GET /v1/evaluations
```

This returns the current evaluation state. The endpoint is useful for local inspection and operator tooling.

## Queue an evaluation

```http
POST /v1/evaluations
Content-Type: application/json

{
  "providerId": "acme",
  "modelId": "model-id"
}
```

Accepted work returns HTTP `202`. A missing model returns `404`; work that cannot be evaluated returns a typed `422` reason. Existing measurements are not silently re-run by routine polling.

## Stop queued evaluation work

```http
DELETE /v1/evaluations
```

## Re-read retained responses

```http
POST /v1/evaluations/regrade
Content-Type: application/json

{
  "providerId": "acme",
  "modelId": "model-id",
  "dryRun": true
}
```

Regrade reads retained responses and does not make provider requests. A deliberate evaluation run and a no-request regrade are different operations.

For versioning and response behavior, read the [API Overview](/api/overview). For model semantics, read [Lifecycle and Evidence](/concepts/lifecycle-and-evidence).

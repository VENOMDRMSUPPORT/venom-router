# Errors and Pagination

Catalog responses use explicit status codes and generic error bodies. The service does not expose internal exception text, filesystem details, raw upstream payloads, or credentials.

## Common statuses

| Status | Meaning |
|---:|---|
| `200` | The request completed successfully |
| `202` | Evaluation work was accepted for processing |
| `400` | The request body or query value is invalid |
| `404` | The route or requested resource is not available |
| `409` | The operation conflicts with current service state, such as a concurrent sync |
| `410` | A legacy route was removed and provides a migration target |
| `422` | The request is valid but the requested operation is not eligible |
| `500` | The service could not complete the request |
| `503` | The service is running but the catalog is stale or degraded |

## Bounded queries

List endpoints accept bounded query values. The server owns the applied limit and returns enough metadata for a consumer to understand whether it received a full result or a bounded page. Clients must not assume that an omitted or invalid value means an unbounded query.

The `/changes` endpoint supports a `since` cursor and a bounded `limit`. The notification endpoint reports the applied `limit` and uses immutable history semantics.

## Legacy alerts route

`GET /v1/alerts` returns `410` with a `replacement` pointing to `/v1/notifications`. Consumers should migrate rather than retrying the old route.

## Fail closed

Unknown contract versions, unsupported operations, and unknown eligibility states must not be silently converted into defaults. See [API Overview](/api/overview) for the `{{CATALOG_API_CONTRACT_VERSION}}` response contract.

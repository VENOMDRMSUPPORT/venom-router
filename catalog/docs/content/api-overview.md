# API Overview

The Catalog API is currently a local HTTP boundary served under `/v1`. Its default base URL is:

```text
http://127.0.0.1:8791/v1
```

## Contract version

Every HTTP response emitted by the Catalog service carries the contract header:

```text
{{CATALOG_API_CONTRACT_HEADER}}: {{CATALOG_API_CONTRACT_VERSION}}
```

The version and header name are defined by the shared Catalog configuration. Consumers should fail closed when they receive a contract version they do not support.

## Response style

Successful responses return JSON payloads owned by the server. Error responses use typed status codes and generic messages; internal exception details, filesystem paths, raw upstream payloads, and credentials are not exposed to the caller.

The API is not currently an authenticated public service. See [Access Model and Security Boundaries](/concepts/access-model-security-boundaries) before exposing it beyond a controlled local boundary.

## Migration from alerts

The old `GET /v1/alerts` route is retained only as an explicit migration response:

```json
{
  "error": "The alerts contract was replaced by notification history.",
  "replacement": "/v1/notifications",
  "contractVersion": "{{CATALOG_API_CONTRACT_VERSION}}"
}
```

It returns HTTP `410`. Use `GET /v1/notifications` for immutable notification history.

## Next

See [Catalog API Endpoints](/api/catalog-endpoints) for read routes, and [Sync and Evaluations](/api/sync-and-evaluations) for local control operations.

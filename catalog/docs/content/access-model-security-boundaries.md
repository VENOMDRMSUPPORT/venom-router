# Access Model and Security Boundaries

The current Catalog API is a local control surface. The service binds to `127.0.0.1` and does not expose a public authentication protocol in its current contract.

This means the API should be reached through the local machine or an explicitly controlled deployment boundary. A docs page must not describe the current service as a hosted, authenticated, multi-tenant API when that behavior is not present in the code.

## Boundary rules

- The bind address is intentionally fixed to loopback.
- Unknown routes return a typed `404` rather than a directory listing or stack trace.
- HTTP errors return generic response bodies and do not expose internal exception text.
- The response header `x-venom-catalog-contract` identifies the API contract version.
- Consumers should fail closed when the declared contract version is unsupported.

## Local does not mean unrestricted

The Database Browser is a separate read-only diagnostic surface. It accepts one bounded `SELECT` statement or read-only CTE and is not a general SQL console. It is intentionally excluded from the stable public API reference.

The [Quick Start](/guides/quick-start) shows the local setup. The [API Overview](/api/overview) explains versioning and the boundary between current local behavior and future hosted deployment.

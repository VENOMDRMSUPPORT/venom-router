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

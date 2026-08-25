# Venom Catalog

Venom Catalog is an independent product and the source of truth for model inventory, model facts, provenance, freshness, and catalog scoring consumed by Venom Router.

The public product documentation is generated from the canonical Markdown content under [`docs/content/`](docs/content/). Start at [`docs/content/overview.md`](docs/content/overview.md) for the product explanation, or [`docs/content/quick-start.md`](docs/content/quick-start.md) for local setup.

## Local surfaces

| Surface | Default endpoint | Owner |
|---|---|---|
| Catalog UI | `http://127.0.0.1:5173/` | Catalog frontend |
| Catalog API | `http://127.0.0.1:8791/v1` | Catalog service |
| Venom Router control plane | `http://127.0.0.1:8081` | Venom Router |

The Catalog UI and API run as separate processes. Catalog does not require Venom Router to be running for development, syncing, evaluation, serving, or testing.

## Ownership boundary

Venom Router consumes Catalog through its API. It must not open the Catalog SQLite database, create a second model roster, duplicate Catalog scoring or provenance logic, or write Catalog-owned state.

## Development commands

Run from this directory:

```bash
npm run dev
npm run serve
npm run sync
npm test
```

The Vite development server proxies `/v1` requests to the Catalog API. Override the UI port with `PORT` and the API target with `CATALOG_API` when an isolated verification instance is required.

## Contract reference

The current API contract is defined in [`config/api-contract.ts`](config/api-contract.ts) and exposed by the HTTP service. Consumers should fail closed on unsupported contract versions. The public API route map and migration notes live in the canonical docs content, not in a second copy of this index.

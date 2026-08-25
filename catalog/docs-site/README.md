# Venom Catalog Docs

This is the standalone public documentation surface for Venom Catalog. It is intentionally separate from the dashboard SPA and produces `catalog/dist-docs/` instead of `catalog/dist/`.

## Local development

Run the following from `catalog/`:

```bash
npm run dev
```

Then open `http://localhost:5173/docs/`. The dashboard app routes this path to the independent docs shell and does not mount the Catalog API provider for documentation pages, so the docs work on the local Catalog origin without a domain or subdomain.

For a standalone docs development server, use:

```bash
npm run dev:docs
```

For the static build and prerender checks, use:

```bash
npm run typecheck:docs
npm run build:docs
```

The generated site is static. It does not require the Catalog API, SQLite, a login session, a domain, or a subdomain.

## Publication modes

The same source supports three deployment shapes:

| Mode | Build setting | Hosting shape |
|---|---|---|
| Local/root | `DOCS_BASE_PATH=/` | `http://localhost/...` or a dedicated host |
| Reverse-proxy path | `DOCS_BASE_PATH=/docs` | `https://example.com/docs/` |
| Documentation subdomain | `DOCS_BASE_PATH=/` and optional `DOCS_PUBLIC_ORIGIN=https://docs.example.com` | `https://docs.example.com/` |

`DOCS_PUBLIC_ORIGIN` is optional. When it is absent, prerendered pages use relative URLs and no absolute sitemap is generated. When it is present, page canonical tags, Open Graph URLs, and `sitemap.xml` use that origin. `VITE_CATALOG_URL` and `VITE_REPO_URL` are also optional; the top navigation does not show external links unless their destinations are explicitly configured.

The hosting layer is responsible for mapping the generated `dist-docs/` directory to a root, path, or host. DNS and reverse-proxy configuration are deliberately outside the repository so the application remains portable.

## Content source

Canonical Markdown lives in `../docs/content/`. Page metadata, ordering, and API declarations live in `content.manifest.json`. The same sources generate navigation, search data, prerendered HTML, and the API contract check.

## Contract discipline

API version and header placeholders are resolved from `../config/api-contract.ts`. The backend docs contract test checks endpoint recognition through the actual `route()` boundary, while deeper behavior remains covered by the Catalog backend tests.

Database Browser routes are not stable public API documentation. If they are documented in a future local guide, they must be labeled as a local diagnostic surface rather than a stable integration contract.

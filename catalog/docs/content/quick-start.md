# Quick Start

Venom Catalog currently runs as a local application with a separate UI process and API process. No domain or subdomain is required for development.

## Prerequisites

Use the repository's pinned Node runtime and install dependencies from `catalog/`.

```bash
cd catalog
npm install
```

## Start the API

Start the Catalog service on its loopback-only default endpoint:

```bash
npm run serve
```

The API is available at `http://127.0.0.1:8791/v1` by default. It is intentionally not bound to a public network interface.

## Start the UI

In a second terminal:

```bash
cd catalog
npm run dev
```

Open `http://127.0.0.1:5173/`. The Vite development server proxies `/v1` requests to the local Catalog API.

## Verify the installation

Run the complete Catalog verification sequence from `catalog/`:

```bash
npm run typecheck
npm run test:backend
npm run test:spa
npm run build
```

Each command is a separate quality gate. A passing UI test does not replace backend tests.

## What comes next

Read [How the Catalog Works](/guides/how-the-catalog-works) to understand ownership and lifecycle rules. Then explore [Providers](/concepts/providers) and [Models and Offers](/concepts/models-and-offers).

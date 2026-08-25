# Venom Catalog

Venom Catalog is the independent source of truth for AI model inventory, provider facts, provenance, freshness, and catalog scoring consumed by Venom Router.

The Catalog owns the provider roster, model facts, source evidence, freshness state, change history, and server-derived scores. Venom Router consumes the Catalog through its API; it does not open the Catalog database or duplicate Catalog derivation logic.

## What this documentation covers

This first documentation release explains the local Catalog product, its data model, its evidence rules, and the current API boundary. It is written for developers and operators who need to understand what the Catalog can honestly guarantee today.

## Current operating model

The Catalog UI and API run as separate local processes. The API binds to `127.0.0.1` by design, and the current local defaults are:

| Surface | Default |
|---|---|
| Catalog UI | `http://127.0.0.1:5173/` |
| Catalog API | `http://127.0.0.1:8791/v1` |
| Venom Router control plane | `http://127.0.0.1:8081` |

This documentation site is a static public surface. It does not require the Catalog API, a database, a login session, or a domain name during development.

## Continue

Start with the [Quick Start](/guides/quick-start), then read [How the Catalog Works](/guides/how-the-catalog-works). For integrations, begin with the [API Overview](/api/overview).

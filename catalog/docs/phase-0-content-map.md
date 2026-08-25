# Phase 0 Content-to-Code Map

**Status:** Reviewed baseline for the public Venom Catalog documentation V1.

**Purpose:** Establish one evidence-backed map from public documentation claims and API declarations to the current Catalog source code and executable tests before creating the docs site.

## Decision summary

Venom Catalog is treated as a product moving toward a public developer audience, so a public `docs.<primary-domain>` surface is justified. The first release remains deliberately small: Overview, Getting Started, Concepts, API Reference, and Glossary. The public site must describe the current local service honestly; it must not imply that the loopback-only API is already a hosted multi-tenant service.

The authoritative documentation content will live once in the future `catalog/docs/content/` tree. This file is the Phase 0 inventory and review record, not a second long-form source of product truth. `standalone.md` must be reduced in the same change that introduces the canonical content tree so lifecycle, evidence, notifications, and API explanations are not maintained twice.

## Evidence key

| Key | Repository source | Role in the map |
|---|---|---|
| S | [`standalone.md`](../standalone.md) | Existing standalone product contract and operational index |
| C | [`catalog/CLAUDE.md`](CLAUDE.md) | Binding catalog rules and lifecycle/evidence invariants |
| A | [`server/app.ts`](../server/app.ts) | Actual route boundary and response behavior |
| H | [`server/index.ts`](../server/index.ts) | HTTP listener, loopback binding, response header, and body boundary |
| V | [`config/api-contract.ts`](../config/api-contract.ts) | Single source for API contract version and header name |
| P | [`config/ports.ts`](../config/ports.ts) | Single source for local bind host and default ports |
| T | [`server/app.test.ts`](../server/app.test.ts) | Executable route and serialization behavior tests |
| HT | [`server/index.test.ts`](../server/index.test.ts) | Executable proof that the HTTP layer emits the contract header |
| MT | [`server/alerts-route.test.ts`](../server/alerts-route.test.ts) | Executable proof for the legacy alerts migration response |
| ST | [`scripts/standalone-contract.test.ts`](../scripts/standalone-contract.test.ts) | Executable proof for local surfaces and non-colliding ports |

## Public claims mapped to code

The following claims are approved for V1 only when the future Markdown page links to the cited source set. A claim marked **verified** is supported by current code or tests. A claim marked **needs wording** is true only with a precise limitation in the page copy.

| Claim to document | V1 page | Status | Source evidence | Editorial boundary |
|---|---|---:|---|---|
| Venom Catalog is an independent product and the source of truth for provider inventory, facts, provenance, freshness, and catalog scoring consumed by Venom Router. | Overview | verified | S §§1–3; C §§3–9 | Do not say Router owns or directly reads Catalog storage. |
| Catalog UI and Catalog API are separate local processes, and Catalog does not require Venom Router to run for development, syncing, evaluation, serving, or testing. | Quick Start | verified | S §§5–13; ST tests 10–21 | Present this as the current local operating model. |
| The API binds to `127.0.0.1`; default Catalog UI/API/Router ports are `5173`/`8791`/`8081`. | Access model & security boundaries; Quick Start | verified | S §§37–41; P; H §§2–7, 26–32; ST tests 10–21 | This is local loopback access, not public network authentication. |
| The Catalog service is the single database writer, and terminal batches must pass the service guard or use the service API. | How the Catalog Works | verified | C §§58–69; S §§43–54 | Keep implementation detail concise in public docs; link to operations material later. |
| A successful provider roster is the existence declaration: present offers are active and absent offers become retired on that successful run; failed or quarantined fetches do not change lifecycle state. | Lifecycle and Evidence | verified | S §§15–21; C §§71–119; T lifecycle sections | Do not describe a failed fetch as proof that a model disappeared. |
| Existing measurements and scores are retained on routine syncs; a new model is measured once, while re-measurement requires explicit maintenance or human action. | Lifecycle and Evidence | verified | S §§19–21, 74–86; C §§103–107 | Avoid promising automatic re-evaluation of every routine sync. |
| `missing` is not the default first-miss state; default first-miss retirement goes directly to `retired`, while older/configured paths remain supported. | Lifecycle and Evidence | verified | C §§116–120 | Include only if the page can explain the historical/configured exception clearly. |
| Unknown values remain unknown; `unrated` is not a low score and is not converted to zero or placed at the end of a quality ranking. | Models and Offers; Lifecycle and Evidence | verified | C §§122–127; S §§19–21; T score sections | This is a core product principle and should be explicit. |
| A provider-specific fact must not be copied to another provider offering; a conflict requires entitled sources that materially disagree without a standing rule resolving the difference. | Models and Offers; Lifecycle and Evidence | verified | C §§91–101, 135–145; T conflict sections | Distinguish conflict history from the current `openConflicts` work view. |
| Resolved conflicts remain inspectable in history but do not count as active conflicts or unresolved current work. | Lifecycle and Evidence | verified | C §§129–133; T conflict sections | Do not filter resolved history out of evidence panels. |
| Snapshot data is explicitly a snapshot and must never be presented as live data. | How the Catalog Works; Glossary | verified | S §§17–21; C §§71–81 | Every example or fallback explanation must preserve this distinction. |
| API responses carry `x-venom-catalog-contract: catalog-api-v2`. | API Overview | verified | V lines 8–14; H §§137–184; HT lines 6–24 | The docs must import/display the version from the shared constant, not hard-code a second version literal. |
| `catalog-api-v2` replaces the old alerts lifecycle wire shape with notification history; consumers should fail closed on unsupported versions. | API Overview; Errors and Pagination | verified | V lines 2–12; S §§60–62; MT lines 113–126 | This is a migration/versioning rule, not a claim that alerts still works. |
| Model inventory and provider listing are read through `GET /v1/models` and `GET /v1/providers`. | Catalog API Endpoints | verified | A §§156–170; T AC1/AC2 sections | Document query parameters and response fields only after extracting them from actual response fixtures. |
| Provenance and evaluation diagnostics are available through model-scoped read routes. | Catalog API Endpoints | verified | A §§172–190; T AC4/AC6 and evaluation sections | Document that diagnostic responses are sanitized and do not include raw provider responses or credentials. |
| Changes are read through `GET /v1/changes`, with bounded limits and a cursor/since mechanism. | Catalog API Endpoints; Errors and Pagination | verified | A §§193–197; T AC10 section | The docs-contract test should check presence and basic response status, not duplicate the detailed limit suite. |
| Notifications are immutable history read through `GET /v1/notifications`; read state is a preference changed through `PATCH /v1/notifications/read`. | Catalog API Endpoints | verified | S §§64–72; A §§199–233; T notification sections | Do not describe an outbound webhook, acknowledge/resolve queue, or delivery lifecycle; those are not current product behavior. |
| `POST /v1/sync` starts a sync and refuses a concurrent run with a typed `409` result. | Sync and Evaluations | verified | A §§255–263; T AC8 section | Describe it as a local control operation and keep it out of a generic public-hosted API promise. |
| `/v1/evaluations` exposes queue state/control and `/v1/evaluations/regrade` re-reads retained responses without provider requests. | Sync and Evaluations | verified | A §§266–333; T evaluation sections | Document method/status existence and safety boundary; leave deep queue semantics to later operations docs. |
| Database Browser routes are bounded, read-only local diagnostics rather than a general SQL console. | Not in stable public API V1 | verified but excluded | S §§125–139; `server/database-browser.ts`; T Database Browser section | If mentioned later, label clearly: “local diagnostic surface, not a stable integration contract.” Do not place `/v1/db/*` in the public stable API table now. |

## V1 endpoint declaration set

The first public API reference will contain only the stable, product-facing read and control surfaces listed below. The future `apiEndpoints` manifest must use these declarations as its source for navigation and contract tests.

| Method | Path | V1 treatment | Contract-test scope |
|---|---|---|---|
| GET | `/v1/health` | Include with local/access limitation | Exists, non-404, basic response envelope |
| GET | `/v1/providers` | Include | Exists, non-404, basic response envelope |
| GET | `/v1/models` | Include | Exists, non-404, basic response envelope |
| GET | `/v1/models/{providerId}/{modelId}/provenance` | Include | Exists with safe fixture, non-404, basic response envelope |
| GET | `/v1/models/{providerId}/{modelId}/evaluation` | Include with diagnostic/sanitization note | Exists with safe fixture, non-404, basic response envelope |
| GET | `/v1/changes` | Include | Exists, non-404, basic response envelope |
| GET | `/v1/notifications` | Include | Exists, non-404, basic response envelope |
| PATCH | `/v1/notifications/read` | Include | Exists, safe empty/body fixture, non-404, basic response envelope |
| POST | `/v1/sync` | Include with local control-operation warning | Exists, fixture runner, non-404/expected control status |
| GET | `/v1/evaluations` | Include with local control-operation warning | Exists, non-404, basic response envelope |
| POST | `/v1/evaluations` | Include with local control-operation warning | Exists, safe invalid/model fixture, non-404, basic response envelope |
| DELETE | `/v1/evaluations` | Include with local control-operation warning | Exists, non-404, basic response envelope |
| POST | `/v1/evaluations/regrade` | Include with explicit no-provider-request note | Exists, safe dry-run fixture, non-404, basic response envelope |
| GET | `/v1/alerts` | Include only in migration/errors page | Assert `410`, replacement `/v1/notifications`, and shared contract version |

### Excluded from the stable public API table

The following routes remain real code surfaces but are deliberately not presented as stable public integration contracts in V1:

| Surface | Reason for exclusion | Allowed future wording |
|---|---|---|
| `GET /v1/db/tables` | Local inspection of approved tables, not a product integration endpoint | “Local diagnostic surface; not a stable contract.” |
| `GET /v1/db/schema` | Same local inspection boundary | Same warning, with exact behavior only in an operations/local guide later |
| `POST /v1/db/query` | Bounded read-only SQL inspection; public documentation could encourage unsupported dependency on schema names | Same warning; never call it a general SQL console |

## Contract-test boundary

`docs-contract.test.ts` must verify **existence and contract shape only**. It must not become a second `app.test.ts` suite.

The test will read endpoint declarations from the canonical docs manifest, substitute safe fixture values for parameterized paths, call the real `route()` boundary, and assert that the declared method/path is not an unknown route. For routes requiring a valid seeded model, the test will use one shared fixture rather than re-testing model scoring, conflict resolution, notification limits, SQL authorization, or evaluation queue semantics already covered by the existing backend suite.

The contract test will also assert the two versioning facts that are part of the public contract:

1. The shared constant remains `catalog-api-v2`, and the shared header name remains `x-venom-catalog-contract`; no literal version may be duplicated in docs content.
2. `GET /v1/alerts` returns `410` and points consumers to `/v1/notifications` with the same shared contract version.

The actual network-level header is already proven in `server/index.test.ts`, which is the correct place for HTTP envelope behavior. The docs contract test should reference that proof rather than reimplementing a second temporary HTTP server.

## Isomorphic prerender constraint

The Markdown parser and normalized document model must run identically in Node during prerender/search-index generation and in the browser during the first client render. The Phase 1 design must therefore choose an isomorphic parser or ensure the browser consumes the exact serialized normalized document generated at build time.

A sample-page regression test is required: render one representative page through the prerender path and through the first client render, normalize only unavoidable runtime attributes, and assert equivalent content structure. This test is specifically for hydration mismatch prevention; it must not become a visual snapshot suite.

## `standalone.md` reference audit before reduction

Before reducing `standalone.md`, search the repository for references to its headings, route names, contract version, and operational claims. The audit must include tests, roadmap notes, project instructions, and any generated or maintained memory files that are part of the working project context. A reference is either updated to the canonical docs path, retained because it is an internal rule, or removed if obsolete.

The reduction is accepted only when no document continues to copy the same lifecycle, evidence, notification, or API explanation as an independent normative source.

## Phase 0 acceptance checklist

| Check | Acceptance condition |
|---|---|
| Audience | Public docs direction is explicit, while V1 remains limited to approximately 12 pages |
| Source of truth | Canonical content tree and migration of `standalone.md` are part of the same implementation change |
| Public API boundary | Database Browser is excluded from the stable public API table and marked local diagnostic if referenced later |
| Endpoint map | Every V1 declaration maps to a real route in `server/app.ts` |
| Versioning | Docs use `CATALOG_API_CONTRACT_VERSION`; no literal version copy is accepted |
| Migration | `/v1/alerts` is documented as a `410` migration response to `/v1/notifications` |
| Contract test | Future docs-contract test checks existence/shape only and does not duplicate deep backend assertions |
| Prerender | Parser/document model is isomorphic, with a sample render-equivalence test planned |
| Reference audit | All `standalone.md` consumers are found and classified before reduction |
| Repository language | New committed files remain English-only, per repository rules |

## References

[1]: ../standalone.md "Venom Catalog standalone product contract"
[2]: CLAUDE.md "Catalog product rules"
[3]: ../server/app.ts "Catalog HTTP route boundary"
[4]: ../server/index.ts "Catalog HTTP server and response envelope"
[5]: ../config/api-contract.ts "Catalog API contract identifiers"
[6]: ../config/ports.ts "Catalog local ports and bind host"
[7]: ../server/app.test.ts "Catalog route-boundary tests"
[8]: ../server/index.test.ts "Catalog HTTP envelope test"
[9]: ../server/alerts-route.test.ts "Legacy alerts migration test"
[10]: ../scripts/standalone-contract.test.ts "Standalone surface contract tests"

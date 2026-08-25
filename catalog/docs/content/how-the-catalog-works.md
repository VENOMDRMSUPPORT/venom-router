# How the Catalog Works

Venom Catalog is a standalone product. Venom Router reads the Catalog API, but it must not open the Catalog SQLite database, create a second model roster, or duplicate Catalog scoring and provenance logic.

## One writer

The Catalog service is the single database writer. Terminal batches that need to inspect or transform the database use the service guard or are driven through the service API. This prevents two independent writers from corrupting SQLite state.

## The data flow

1. A provider adapter fetches a roster or metadata source.
2. The shared pipeline validates the response and records source provenance.
3. A successful roster updates provider-declared model existence.
4. Enrichment resolves model facts and identity evidence.
5. Server-owned scoring projects quality and operational information for the UI and API.
6. Changes and evidence remain inspectable instead of being replaced by an opaque current value.

## Source and snapshot semantics

Every displayed fact or derived number should retain its source, source reference, source URL where applicable, evidence state, resolver or methodology version, and resolution or computation time.

> A snapshot is explicitly identified as a snapshot. It must never be presented as a live answer.

## Router integration

Router integrations should use the Catalog API base URL and record the Catalog response version or generation timestamp used for a routing decision. Consumers should fail closed when they receive an unsupported API contract version.

See the [API Overview](/api/overview) for the current contract identifier and [Lifecycle and Evidence](/concepts/lifecycle-and-evidence) for the rules that determine what the Catalog is allowed to publish.

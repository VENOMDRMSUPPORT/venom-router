# Models and Offers

The Catalog distinguishes a canonical model identity from a provider offering. This prevents the same model from being copied into unrelated rows while preserving provider-specific facts.

## Canonical identity

A canonical identity represents the model family or model version as a shared concept. Aliases preserve the different names used by providers, vendors, benchmarks, or reviewed mappings.

## Provider offering

A provider offering is the callable model as declared by a provider. It carries the provider model ID, lifecycle, publication state, pricing, capabilities, and evidence that belong to that provider. The same canonical model can have multiple provider offerings.

| Concept | Meaning |
|---|---|
| Canonical model | Shared identity used to relate equivalent offerings |
| Alias | A reviewed or sourced name for that identity |
| Provider offering | A provider-specific callable row |
| Fact | A value with source and evidence metadata |
| Publication state | Whether the offering is published, excluded, missing, retired, or under review |

## Scores and unknown values

Scores are server-owned semantics. The client renders values and reasons returned by the API; it does not recompute or fabricate scores. `unrated` means that quality is unknown. It is not a zero and it is not placed at the end of a quality ranking.

Use `GET /v1/models` for the current model list. Use the provider and model identifiers returned by that endpoint when requesting [provenance](/api/catalog-endpoints).

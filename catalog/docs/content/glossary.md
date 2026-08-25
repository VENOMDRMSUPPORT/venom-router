# Glossary

| Term | Definition |
|---|---|
| Catalog | The independent Venom product that owns provider inventory, model facts, provenance, freshness, and catalog scoring. |
| Provider | A source or serving seller represented by a provider adapter and its declared data sources. |
| Canonical model | A shared identity used to relate equivalent provider offerings. |
| Provider offering | A provider-specific callable model row with its own ID, facts, pricing, capabilities, lifecycle, and evidence. |
| Alias | A provider, vendor, benchmark, or reviewed name mapped to a canonical model identity. |
| Fact | A model or offering value retained with source and evidence metadata. |
| Provenance | The source, transformation, version, and time information needed to explain a displayed value. |
| Evidence state | The state describing how a fact was established, such as a measured, calibrated, or declared result. |
| Lifecycle | The provider-declared existence state of an offering, including `active`, `missing`, `retired`, and `excluded`. |
| Snapshot | A captured catalog state that is explicitly not a live answer. |
| Conflict | A material disagreement between entitled sources for the same offering when no standing rule resolves it. |
| Open conflict | A conflict that remains current work. Resolved conflict history remains inspectable but is not counted as open work. |
| Unrated | An honest unknown quality result. It is not a zero and is not a last-place ranking. |
| Catalog API contract | The versioned response boundary identified by `{{CATALOG_API_CONTRACT_VERSION}}` and the `{{CATALOG_API_CONTRACT_HEADER}}` header. |
| Local diagnostic surface | A local inspection feature, such as the Database Browser, that is not a stable integration contract. |
| Single writer | The rule that the Catalog service owns database writes and terminal batches must use the service guard or service API. |

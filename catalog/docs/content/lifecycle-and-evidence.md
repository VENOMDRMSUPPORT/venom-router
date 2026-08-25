# Lifecycle and Evidence

The Catalog tracks provider-declared existence and preserves the evidence behind displayed values. Lifecycle changes are not inferred from a failed request.

## Lifecycle transitions

| Event | Result |
|---|---|
| Successful roster contains an offering | The offering is inserted or remains `active` |
| Successful roster omits a previously present offering | The offering becomes `retired` on that run |
| Failed or malformed roster | No model lifecycle state changes |
| Quarantined removal | No model lifecycle state changes |
| Routine sync sees an existing measured model | Existing measurements and scores are retained |
| New model is inserted | It may be measured once after insertion |

The default first-miss path goes directly to `retired`. The `missing` state remains supported for older rows or an explicitly configured multi-miss policy.

## Evidence and conflicts

A fact is useful only when its source, source reference, evidence state, resolver or methodology version, and resolution time are preserved. A conflict requires two entitled sources for the same offering to publish materially different values without a standing rule that resolves the difference.

The API keeps complete conflict history in `conflicts`. The derived `openConflicts` view represents current work. Resolved history remains inspectable but does not count as an active conflict.

## Unknown is honest

Unknown quality is represented as `unrated`, with a null score where appropriate. The Catalog does not lower an unknown value to zero, hide it, or invent a ranking position.

Read the [Models and Offers](/concepts/models-and-offers) page for the identity boundary and the [Catalog API Endpoints](/api/catalog-endpoints) page for provenance responses.

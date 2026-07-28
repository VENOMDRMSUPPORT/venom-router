// Package routing is the tier engine's pure domain package (05): typed
// tier policies (lite/pro/max), request normalization and
// hard-requirement derivation, provider-neutral thinking-budget
// normalization, and the deterministic workload-profile bucket key.
//
// Layering (08 §2, staticgate-enforced): this package imports
// internal/models and the standard library only — no storage, no
// database/sql, no net/http, no env access. Everything here is keyed by
// Tier and typed capability — no model-name or provider-slug input
// exists anywhere in this package.
package routing

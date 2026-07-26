// Package intelligence is discovery orchestration, probing, external
// metadata sync, and the certification pipeline (01 §3). It may import
// internal/models and internal/providers but never internal/storage,
// internal/httpapi, internal/httpui, database/sql, or net/http. It is pure
// where practical: the evidence-precedence engine and the catalog_only
// classification in this package do no I/O, keep no state, and take the
// current time as an injected parameter rather than calling time.Now()
// themselves, so every test is deterministic.
package intelligence

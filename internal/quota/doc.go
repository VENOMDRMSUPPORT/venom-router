// Package quota holds the pure quota domain model (02 §3, 05 §4): the
// multi-window quota model, window_key normalization, per-window
// eligibility states, the mandatory local-safety budget, canonical
// pre-execution consumption estimates, the five-state reservation
// lifecycle, the janitor's discriminated recovery branches, the
// reconciliation worker's retry policy, and the cooldown scope/source
// vocabulary. It imports no storage, database/sql, net/http, or
// internal/providers — persistence, the janitor/reconciliation/cooldown
// storage-layer implementations, and provider-evidence mapping all live
// in internal/storage.
package quota

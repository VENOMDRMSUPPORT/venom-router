// Package quota holds the pure quota domain model (02 §3, 05 §4): the
// multi-window quota model, window_key normalization, per-window
// eligibility states, the mandatory local-safety budget, and canonical
// pre-execution consumption estimates. It imports no storage,
// database/sql, net/http, or internal/providers — persistence lives in
// internal/storage, and provider-evidence mapping is a later unit's
// concern.
package quota

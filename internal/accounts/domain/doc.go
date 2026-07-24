// Package domain holds the pure account entity and its state machines
// (02 §3 "Account lifecycle"): the two independent persisted axes
// (ConnectionState, HealthState), their legal-transition rules, the
// derived display_status projection, and the routing-eligibility
// projection.
//
// This package is pure: no I/O, no persistence, no clock or randomness
// read directly from the environment. Every function that needs the
// current time takes it as a parameter (an injected clock) rather than
// calling time.Now() itself, so every test is deterministic. Persistence
// (internal/storage) and the application service
// (internal/accounts/application) are later units built on top of this
// one, not part of it.
package domain

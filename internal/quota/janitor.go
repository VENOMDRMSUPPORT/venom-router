package quota

import "time"

// JanitorResult tallies how many reservations one janitor sweep moved
// through each of its three discriminated branches (02 §3): reservations
// released outright, reservations pended for reconciliation, and
// reservations that hit the terminal unknown_consumption boundary.
type JanitorResult struct {
	Released           int
	Pended             int
	UnknownConsumption int
}

// SetMaxOpenConns(1) hazard: the janitor acquires its own dedicated
// connection and holds it for the whole sweep (exactly like
// QuotaReservationRepo.Reserve and QuotaLifecycleRepo.applyTransition do),
// so a caller that invokes the janitor from inside a transaction it
// already holds deadlocks waiting for a second connection the pool will
// never hand out. This is documented here and on storage.listWindowsOnConn
// rather than expressed as an exported sentinel: no code path can detect
// the misuse at runtime, so nothing could ever return such an error, and
// an exported error that is never returned is contract surface with no
// behaviour behind it.

// DefaultRetryDeadline is how long a reconciliation_pending reservation
// may wait before the janitor gives up and transitions it to
// unknown_consumption (02 §3 / 05 §4's retry boundary) — the terminal
// backstop for a reservation whose provider outcome never arrives.
const DefaultRetryDeadline = 30 * time.Minute

// DefaultJanitorBatchSize bounds how many reservations the janitor moves
// per branch in one sweep, keeping its transaction short-held.
const DefaultJanitorBatchSize = 100

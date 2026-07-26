package quota

import (
	"errors"
	"time"
)

// JanitorResult tallies how many reservations one janitor sweep moved
// through each of its three discriminated branches (02 §3): reservations
// released outright, reservations pended for reconciliation, and
// reservations that hit the terminal unknown_consumption boundary.
type JanitorResult struct {
	Released           int
	Pended             int
	UnknownConsumption int
}

// ErrJanitorWouldDeadlock documents the SetMaxOpenConns(1) hazard: the
// janitor acquires its own dedicated connection and holds it for the
// whole sweep (exactly like QuotaReservationRepo.Reserve and
// QuotaLifecycleRepo.applyTransition do), so a caller that invokes the
// janitor from inside a transaction it already holds would deadlock
// waiting for a second connection the pool will never hand out. The
// janitor itself takes no connection parameter and so cannot detect or
// recover from this misuse — this sentinel exists purely so a future
// caller (e.g. the composition-root startup hook, P3b-JOBS-001) has a
// named hazard to guard against and document by, mirroring how
// listWindowsOnConn documents the same hazard without runtime detection.
var ErrJanitorWouldDeadlock = errors.New("quota: janitor would deadlock: must never be called from within an already-open transaction")

// DefaultRetryDeadline is how long a reconciliation_pending reservation
// may wait before the janitor gives up and transitions it to
// unknown_consumption (02 §3 / 05 §4's retry boundary) — the terminal
// backstop for a reservation whose provider outcome never arrives.
const DefaultRetryDeadline = 30 * time.Minute

// DefaultJanitorBatchSize bounds how many reservations the janitor moves
// per branch in one sweep, keeping its transaction short-held.
const DefaultJanitorBatchSize = 100

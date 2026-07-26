package quota

// JanitorResult tallies how many reservations one janitor sweep moved
// through each of its four discriminated branches (02 §3): reservations
// released outright, reservations pended for reconciliation, pending
// reservations reclaimed from an expired-or-absent lease (branch C1,
// re-enqueued, never terminalized), and pending reservations that hit
// the terminal unknown_consumption boundary (branch C2).
type JanitorResult struct {
	Released           int
	Pended             int
	Reclaimed          int
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
//
// DefaultRetryDeadline (the old wall-clock terminal boundary) is
// deliberately GONE: it contradicted the reconciliation worker's own
// MaxRetries x BaseBackoff boundary for the exact same transition. There
// is now exactly one terminal-boundary predicate, RetryExhausted
// (reconciliation.go), which both the janitor and the worker call.

// DefaultJanitorBatchSize bounds how many reservations the janitor moves
// per branch in one sweep, keeping its transaction short-held.
const DefaultJanitorBatchSize = 100

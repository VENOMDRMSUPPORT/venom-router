package quota

import (
	"errors"
	"time"
)

// ReconciliationPolicy bounds the reconciliation worker's retry behavior
// over reconciliation_pending reservations (02 §3 / 05 §4): how many
// retries it allows before giving up, the backoff between them, and how
// many pending reservations it processes per sweep.
type ReconciliationPolicy struct {
	MaxRetries  int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
	BatchSize   int
}

// DefaultReconciliationPolicy returns the conservative defaults (05 §4).
func DefaultReconciliationPolicy() ReconciliationPolicy {
	return ReconciliationPolicy{
		MaxRetries:  5,
		BaseBackoff: 30 * time.Second,
		MaxBackoff:  30 * time.Minute,
		BatchSize:   20,
	}
}

// ReconciliationOutcome is what ReconcileOne decided for one pending
// reservation: the terminal or non-terminal ReservationState it moved
// to, and (when settled) the per-window costs it settled at.
type ReconciliationOutcome struct {
	ReservationID string
	Outcome       ReservationState
	Actuals       map[string]float64
}

// ErrReconciliationWouldDeadlock documents the same SetMaxOpenConns(1)
// hazard as ErrJanitorWouldDeadlock: the reconciliation worker's storage
// methods each acquire their own connection (via QuotaLifecycleRepo's
// Settle/Release/Transition), so calling them from inside a transaction
// the caller already holds would deadlock. Reserved for a future caller
// to guard against and document by.
var ErrReconciliationWouldDeadlock = errors.New("quota: reconciliation would deadlock: must never be called from within an already-open transaction")

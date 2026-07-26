package quota

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"
)

// DefaultProcessingDeadline is expires_at's default offset (02 §3:
// "default now + 30 s"). expires_at is a PROCESSING DEADLINE, never a
// terminal state — there is no stored `expired` reservation state.
const DefaultProcessingDeadline = 30 * time.Second

// ErrInvalidReservationIdentity is returned by ReservationID for an empty
// requestID or attemptID.
var ErrInvalidReservationIdentity = errors.New("quota: invalid reservation identity")

// ReservationID derives the deterministic reservation identity (02 §3:
// "reservation_id = f(request_id, attempt_id)"): the lowercase hex
// SHA-256 of an injective, length-prefixed encoding of (requestID,
// attemptID) — the same technique models.CanonicalKey uses for provider
// model identity, reimplemented locally rather than imported, since a
// reservation id is not a model identity and internal/quota must stay
// independent of internal/models. Each field is preceded by its own
// 4-byte big-endian length so distinct pairs can never collide (e.g.
// ("a","bc") and ("ab","c") hash differently, unlike a plain
// concatenation which would confuse them). Empty inputs are rejected
// rather than hashed.
func ReservationID(requestID, attemptID string) (string, error) {
	if requestID == "" || attemptID == "" {
		return "", fmt.Errorf("%w: request id and attempt id must both be non-empty", ErrInvalidReservationIdentity)
	}

	fields := [...]string{requestID, attemptID}

	size := 0
	for _, f := range fields {
		size += 4 + len(f)
	}

	buf := make([]byte, 0, size)
	var lenPrefix [4]byte
	for _, f := range fields {
		binary.BigEndian.PutUint32(lenPrefix[:], uint32(len(f)))
		buf = append(buf, lenPrefix[:]...)
		buf = append(buf, f...)
	}

	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), nil
}

// WindowDebit pairs one applicable window with the cost to reserve on it
// and the optimistic-concurrency token (the window's current Version)
// the storage layer must feed into its conditional UPDATE.
type WindowDebit struct {
	WindowID        string
	Unit            Unit
	Cost            float64
	EstimateSource  EstimateSource
	ExpectedVersion int64
}

// ErrNoApplicableWindow is returned by ApplicableDebits when an
// attempt's allocations match no bounding window at all. Nothing
// applicable means nothing bounds the attempt — 02 §3's whole premise is
// that unknown provider quota must never mean unlimited, so an empty
// result is an error, never a silent success (mirrors
// MostRestrictive(nil) == StateUnknown: absence of information is never
// treated as a green light).
var ErrNoApplicableWindow = errors.New("quota: no applicable window to reserve against")

// ApplicableDebits pairs each allocation with every window sharing its
// Unit, producing the exact set of per-window debits Reserve must apply
// atomically.
//
//   - A window is applicable to an allocation when window.Unit ==
//     allocation.Unit. One allocation may therefore debit SEVERAL windows
//     (e.g. a requests allocation hits both an rpm provider-evidence
//     window and the local_safety estimated-consumption window) — that is
//     the entire point of the multi-window model (02 §3).
//   - A window whose Capacity() reports ok == false is EXCLUDED — not
//     debited and not blocking. The conditional UPDATE's
//     COALESCE(remaining, limit_value) - reserved >= cost predicate is
//     NULL for such a window, so including it would make every attempt
//     fail and an account with an unknown provider window would become
//     permanently unroutable — directly contradicting 05 §4 ("unknown …
//     not a hard gate for eligibility, but the attempt still reserves
//     against the account's local-safety windows"). The local-safety
//     windows always carry limit_value, so they remain the fail-closed
//     bound even when every provider window is excluded.
//   - A non-positive allocation cost contributes no debit (nothing to
//     reserve), but does not by itself make the result empty-and-therefore-
//     an-error if other debits exist.
//   - Several allocations matching the SAME window are AGGREGATED into one
//     debit whose Cost is their sum. 02 §3 states a reservation holds "one
//     allocation per applicable window", and M5 enforces exactly that with
//     PRIMARY KEY(reservation_id, window_id). Emitting one debit per
//     allocation instead would break the reservation two ways: the second
//     conditional UPDATE on that window would carry an ExpectedVersion the
//     first UPDATE has already incremented, so it would affect 0 rows and
//     reject the whole attempt, and the second allocation INSERT would
//     violate the primary key. Callers other than Estimate (whose units are
//     unique by construction) can legitimately supply repeated units, so
//     aggregation is what makes this function safe for them.
//   - An aggregated debit takes the LEAST certain contributing
//     EstimateSource (from_request > provider_conversion > policy_default):
//     a summed cost is only as trustworthy as its weakest input, and an
//     unrecognized source is treated as least certain — fail closed.
//   - An empty result is ErrNoApplicableWindow, never a silent success.
//   - The output order is deterministic: aggregated, then sorted by
//     WindowID — the accumulator map is never ranged over to build output.
func ApplicableDebits(windows []Window, allocations []Allocation) ([]WindowDebit, error) {
	byWindow := make(map[string]*WindowDebit, len(windows))
	order := make([]string, 0, len(windows))

	for _, w := range windows {
		if _, ok := w.Capacity(); !ok {
			continue
		}
		for _, a := range allocations {
			if a.Unit != w.Unit || a.Cost <= 0 {
				continue
			}
			if existing, seen := byWindow[w.ID]; seen {
				existing.Cost += a.Cost
				if estimateCertaintyRank(a.Source) > estimateCertaintyRank(existing.EstimateSource) {
					existing.EstimateSource = a.Source
				}
				continue
			}
			byWindow[w.ID] = &WindowDebit{
				WindowID:        w.ID,
				Unit:            w.Unit,
				Cost:            a.Cost,
				EstimateSource:  a.Source,
				ExpectedVersion: w.Version,
			}
			order = append(order, w.ID)
		}
	}
	if len(order) == 0 {
		return nil, ErrNoApplicableWindow
	}

	sort.Strings(order)
	debits := make([]WindowDebit, 0, len(order))
	for _, id := range order {
		debits = append(debits, *byWindow[id])
	}
	return debits, nil
}

// estimateCertaintyRank orders provenance from most to least certain, so
// aggregating picks the weakest contributor. An unrecognized source ranks
// as least certain rather than silently passing as exact.
func estimateCertaintyRank(s EstimateSource) int {
	switch s {
	case EstimateSourceFromRequest:
		return 0
	case EstimateSourceProviderConversion:
		return 1
	case EstimateSourcePolicyDefault:
		return 2
	default:
		return 3
	}
}

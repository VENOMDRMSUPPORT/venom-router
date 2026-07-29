package routing

import (
	"errors"
	"fmt"
)

// This file adds the ONE routing error that P4-ROUTE-015 genuinely needs and
// that no earlier unit already defines. The other stable routing errors already
// exist and ROUTE-015 only MAPS them (never redefines):
//   - ErrContextExceedsTier   (hardgates.go)  → venom_context_exceeds_tier (400)
//   - ErrInvalidExtension     (normalize.go)  → venom_invalid_extension (400)
//   - ErrNoEligibleOffering / *NoEligibleOfferingError (fallback.go) →
//     venom_no_eligible_offering (503), or venom_free_capacity_exhausted (429)
//     when the exhaustion carries a retry_after (a route exists but is
//     temporarily rate-limited/cooling, vs. a structural "no route at all").
//     The 429-vs-503 split is drawn at the envelope from RetryAfter so the
//     frozen Step-8 loop and its tests are untouched.

// ErrCapabilityUnsupported is the sentinel behind venom_capability_unsupported
// (05 §5): a required capability is not certified anywhere in the tier's fleet.
// It is a Go value; the wire code/status live in internal/httpapi.
var ErrCapabilityUnsupported = errors.New("routing: required capability unsupported")

// CapabilityUnsupportedError names the offending capability and records whether
// the miss is STRUCTURAL to the tier (the client asked for something this tier
// can never serve — a client error, mapped 400) or a FLEET GAP (a capability
// valid for the tier that no offering currently certifies — mapped 501). The
// capability name is retained for logging; the rendered wire message stays
// generic so no field is ever echoed onto the wire.
type CapabilityUnsupportedError struct {
	Capability     string
	TierStructural bool
}

func (e *CapabilityUnsupportedError) Error() string {
	return fmt.Sprintf("%s: %q (tier_structural=%t)", ErrCapabilityUnsupported.Error(), e.Capability, e.TierStructural)
}

// Unwrap lets errors.Is(err, ErrCapabilityUnsupported) match.
func (e *CapabilityUnsupportedError) Unwrap() error { return ErrCapabilityUnsupported }

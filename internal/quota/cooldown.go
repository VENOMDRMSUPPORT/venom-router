package quota

import (
	"errors"
	"fmt"
	"time"
)

// CooldownScope is the M5 cooldowns.scope vocabulary (02 §3 / 05 §4):
// exactly one of account/offering/provider, each paired with exactly one
// non-nil identity column — the same invariant M5's CHECK constraint
// enforces at the schema level.
type CooldownScope string

const (
	CooldownScopeAccount  CooldownScope = "account"
	CooldownScopeOffering CooldownScope = "offering"
	CooldownScopeProvider CooldownScope = "provider"
)

// ErrUnknownCooldownScope is returned by ParseCooldownScope for any token
// outside the three canonical scopes.
var ErrUnknownCooldownScope = errors.New("quota: unknown cooldown scope")

// ParseCooldownScope fails closed: an unrecognized token is rejected.
func ParseCooldownScope(s string) (CooldownScope, error) {
	switch CooldownScope(s) {
	case CooldownScopeAccount, CooldownScopeOffering, CooldownScopeProvider:
		return CooldownScope(s), nil
	default:
		return "", ErrUnknownCooldownScope
	}
}

// CooldownSource is the M5 cooldowns.source vocabulary: whether the
// cooldown's `until` came from a provider-supplied Retry-After value or
// this router's own conservative default backoff.
type CooldownSource string

const (
	CooldownSourceRetryAfter     CooldownSource = "retry_after"
	CooldownSourceDefaultBackoff CooldownSource = "default_backoff"
)

// ErrUnknownCooldownSource is returned by ParseCooldownSource for any
// token outside the two canonical sources.
var ErrUnknownCooldownSource = errors.New("quota: unknown cooldown source")

// ParseCooldownSource fails closed: an unrecognized token is rejected.
func ParseCooldownSource(s string) (CooldownSource, error) {
	switch CooldownSource(s) {
	case CooldownSourceRetryAfter, CooldownSourceDefaultBackoff:
		return CooldownSource(s), nil
	default:
		return "", ErrUnknownCooldownSource
	}
}

// Cooldown is one M5 cooldowns row. Exactly one of AccountID,
// OfferingOperationID, ProviderID is non-nil, matching Scope — the same
// invariant the M5 CHECK constraint enforces.
type Cooldown struct {
	ID                  string
	Scope               CooldownScope
	AccountID           *string
	OfferingOperationID *string
	ProviderID          *string
	ReasonCode          string
	Until               time.Time
	Source              CooldownSource
}

// IsOnCooldown reports whether c is still in effect as of now — strictly
// before Until, never inclusive of the boundary instant itself.
func IsOnCooldown(c Cooldown, now time.Time) bool {
	return now.Before(c.Until)
}

// CooldownTrigger is a scope-correct cooldown request in quota's OWN
// vocabulary (05 §3 / 05 §4's 429 bullet). The mapping from
// execution.TypedFailure to this type lives in P4-ROUTE-014, NOT here:
// internal/execution transitively imports net/http (via the vendored
// bifrost core), so importing it from internal/quota would break
// TestLayering_DomainPackagesImportNoInfrastructure.
type CooldownTrigger struct {
	Scope      CooldownScope
	ScopeRef   string
	Until      time.Time
	Source     CooldownSource
	ReasonCode string
}

// ErrInvalidCooldownTrigger is returned by Validate for any structurally
// invalid trigger.
var ErrInvalidCooldownTrigger = errors.New("quota: invalid cooldown trigger")

// Validate fails closed: a known Scope, a non-empty ScopeRef and
// ReasonCode, a known Source, and a Until strictly after now. now is
// taken as an explicit parameter — never time.Now() — matching every
// other quota domain function that needs a clock (e.g. Window.State)
// and this project's "no wall-clock in domain logic" rule.
func (t CooldownTrigger) Validate(now time.Time) error {
	if _, err := ParseCooldownScope(string(t.Scope)); err != nil {
		return err
	}
	if t.ScopeRef == "" {
		return fmt.Errorf("%w: scope ref required", ErrInvalidCooldownTrigger)
	}
	if t.ReasonCode == "" {
		return fmt.Errorf("%w: reason code required", ErrInvalidCooldownTrigger)
	}
	if _, err := ParseCooldownSource(string(t.Source)); err != nil {
		return err
	}
	if !t.Until.After(now) {
		return fmt.Errorf("%w: until must be strictly after now", ErrInvalidCooldownTrigger)
	}
	return nil
}

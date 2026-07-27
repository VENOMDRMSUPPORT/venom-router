package intelligence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

// ProbeClass distinguishes a tiny fixed-fixture probe (standard) from a
// probe whose declared cost is large by construction, such as the
// context-window probe's ~3,000,000-token oversized request (expensive).
// Expensive probes are refused unless the owner has opted in (04 §2).
type ProbeClass string

const (
	ProbeStandard  ProbeClass = "standard"
	ProbeExpensive ProbeClass = "expensive"
)

// ErrUnknownProbeClass is returned by ParseProbeClass for any value
// outside the exact two-value vocabulary.
var ErrUnknownProbeClass = errors.New("intelligence: unrecognized probe class")

// ParseProbeClass fails closed on any value outside the exact two-value
// vocabulary.
func ParseProbeClass(s string) (ProbeClass, error) {
	switch ProbeClass(s) {
	case ProbeStandard, ProbeExpensive:
		return ProbeClass(s), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownProbeClass, s)
	}
}

// ProbeCostCap is a hard ceiling on one quota.Unit dimension.
type ProbeCostCap struct {
	Unit quota.Unit
	Max  float64
}

// ProbeSafetyPolicy is the owner-configurable set of hard limits every
// probe admission is checked against (04 §2 / 09 §3.8): per-probe and
// per-account cost caps, the expensive-probe opt-in toggle, the
// per-provider in-flight cap, and the context-probe cooldown.
type ProbeSafetyPolicy struct {
	PerProbe               []ProbeCostCap
	PerAccount             []ProbeCostCap
	PerAccountWindow       time.Duration
	ExpensiveProbesEnabled bool
	MaxInFlightPerProvider int
	ContextProbeCooldown   time.Duration
}

// DefaultProbeSafetyPolicy returns conservative, owner-overridable
// defaults. PerProbe and PerAccount cover every unit quota.Estimate can
// ever emit (requests, concurrency, input_tokens, output_tokens, credits,
// balance) — a unit Estimate could produce but this policy leaves
// uncapped would otherwise silently pass ProbeGuard.Admit's cap check.
func DefaultProbeSafetyPolicy() ProbeSafetyPolicy {
	return ProbeSafetyPolicy{
		PerProbe: []ProbeCostCap{
			// One request per probe attempt (04 §2: "one request per model
			// per attempt").
			{Unit: quota.UnitRequests, Max: 1},
			// One in-flight slot per probe attempt.
			{Unit: quota.UnitConcurrency, Max: 1},
			// Sized to admit the context-window probe's one oversized
			// request (ContextProbeInputTokens = 3,000,000); every other
			// probe's fixture is far smaller.
			{Unit: quota.UnitInputTokens, Max: 3_000_000},
			// Capability/context probes declare a tiny max_tokens (<=8-64);
			// 1024 leaves headroom without approaching a real chat cost.
			{Unit: quota.UnitOutputTokens, Max: 1024},
			// A probe is never expected to carry a meaningful credit cost;
			// this is a conservative ceiling, not an expected value.
			{Unit: quota.UnitCredits, Max: 1000},
			// Mirrors the credits ceiling for balance-denominated accounts.
			{Unit: quota.UnitBalance, Max: 1000},
		},
		PerAccount: []ProbeCostCap{
			// Bounds how many probe attempts one account can accumulate
			// within PerAccountWindow.
			{Unit: quota.UnitRequests, Max: 500},
			// Mirrors the request ceiling for the concurrency dimension.
			{Unit: quota.UnitConcurrency, Max: 500},
			// Enough for several context-window probes plus many small
			// capability probes within the window.
			{Unit: quota.UnitInputTokens, Max: 20_000_000},
			{Unit: quota.UnitOutputTokens, Max: 200_000},
			// Conservative aggregate ceiling; see the per-probe credits cap.
			{Unit: quota.UnitCredits, Max: 5000},
			{Unit: quota.UnitBalance, Max: 5000},
		},
		PerAccountWindow:       24 * time.Hour,
		ExpensiveProbesEnabled: false,
		MaxInFlightPerProvider: 1,
		ContextProbeCooldown:   7 * 24 * time.Hour,
	}
}

// ProbeReserver reserves quota for one probe attempt's estimated
// allocations (P3b's reservation engine). The storage adapter is supplied
// by a later unit (P3c-CAPI-001/JOBS-001); this package only declares the
// port.
type ProbeReserver interface {
	ReserveProbe(ctx context.Context, accountID, requestID, attemptID string, allocations []quota.Allocation) (reservationID string, err error)
}

// ProbeSpendReader reads accountID's probe spend recorded since since, so
// ProbeGuard can enforce the rolling per-account cap.
type ProbeSpendReader interface {
	ProbeSpendSince(ctx context.Context, accountID string, since time.Time) ([]quota.Allocation, error)
}

// ProbeInFlightReader reads how many probes are currently in flight for
// providerID, enforcing the per-provider concurrency cap (04 §2: "max 1
// in-flight probe").
type ProbeInFlightReader interface {
	InFlightProbes(ctx context.Context, providerID string) (int, error)
}

// ProbeCooldownReader reads the context-probe cooldown deadline (if any)
// for one offering-operation (04 §2: "7-day cooldown").
type ProbeCooldownReader interface {
	ProbeCooldownUntil(ctx context.Context, offeringOperationID string) (*time.Time, error)
}

// ErrProbeRefused is the single shared error boundary every probe
// admission refusal wraps (this repo already paid for having two — see
// QUOTA-006/007/008's remediation). Callers should use errors.Is against
// this sentinel, or RefusalOf for the specific typed reason.
var ErrProbeRefused = errors.New("intelligence: probe refused")

// ProbeRefusal is the typed, user-safe reason ProbeGuard.Admit refused a
// probe.
type ProbeRefusal string

const (
	RefusalOptInRequired     ProbeRefusal = "probe_opt_in_required"
	RefusalCapped            ProbeRefusal = "probe_capped"
	RefusalAccountCapped     ProbeRefusal = "probe_account_capped"
	RefusalConcurrency       ProbeRefusal = "probe_concurrency"
	RefusalCoolingDown       ProbeRefusal = "probe_cooling_down"
	RefusalQuotaRejected     ProbeRefusal = "probe_quota_rejected"
	RefusalSafetyUnavailable ProbeRefusal = "probe_safety_unavailable"
)

// ProbeRefusedError is the concrete error ProbeGuard.Admit returns on
// every refusal path. Detail is a short internal description only — it
// must never contain provider text or credential material.
type ProbeRefusedError struct {
	Refusal ProbeRefusal
	Detail  string
}

func (e *ProbeRefusedError) Error() string {
	return fmt.Sprintf("intelligence: probe refused (%s): %s", e.Refusal, e.Detail)
}

// Unwrap lets errors.Is(err, ErrProbeRefused) succeed for every refusal.
func (e *ProbeRefusedError) Unwrap() error {
	return ErrProbeRefused
}

// RefusalOf extracts the typed ProbeRefusal from err, if err is (or
// wraps) a *ProbeRefusedError.
func RefusalOf(err error) (ProbeRefusal, bool) {
	var refused *ProbeRefusedError
	if errors.As(err, &refused) {
		return refused.Refusal, true
	}
	return "", false
}

func refuse(reason ProbeRefusal, detail string) error {
	return &ProbeRefusedError{Refusal: reason, Detail: detail}
}

// ErrInvalidProbeSafetyPolicy is returned by NewProbeGuard for a
// structurally invalid ProbeSafetyPolicy.
var ErrInvalidProbeSafetyPolicy = errors.New("intelligence: invalid probe safety policy")

// ErrNilProbeGuardPort is returned by NewProbeGuard when any required
// port is nil.
var ErrNilProbeGuardPort = errors.New("intelligence: probe guard requires all four ports")

// ErrInvalidProbeAdmissionRequest is returned by Admit for a structurally
// malformed ProbeAdmissionRequest, before any gate or port is consulted.
var ErrInvalidProbeAdmissionRequest = errors.New("intelligence: invalid probe admission request")

// ProbeGuard is the single admission gate every probe must pass through
// before touching a transport (P3c-CERT-002/003 both call Admit first).
// It never calls ProbeReserver.ReserveProbe on any refusal path.
type ProbeGuard struct {
	policy   ProbeSafetyPolicy
	reserver ProbeReserver
	spend    ProbeSpendReader
	inflight ProbeInFlightReader
	cooldown ProbeCooldownReader
	now      func() time.Time
}

// NewProbeGuard builds a ProbeGuard. Construction fails closed: any nil
// port, a MaxInFlightPerProvider below 1, a negative
// ContextProbeCooldown/PerAccountWindow, or any cap with a negative Max
// is rejected. now defaults to time.Now when nil.
func NewProbeGuard(policy ProbeSafetyPolicy, reserver ProbeReserver, spend ProbeSpendReader, inflight ProbeInFlightReader, cooldown ProbeCooldownReader, now func() time.Time) (*ProbeGuard, error) {
	if reserver == nil || spend == nil || inflight == nil || cooldown == nil {
		return nil, ErrNilProbeGuardPort
	}
	if policy.MaxInFlightPerProvider < 1 {
		return nil, fmt.Errorf("%w: MaxInFlightPerProvider must be >= 1, got %d", ErrInvalidProbeSafetyPolicy, policy.MaxInFlightPerProvider)
	}
	if policy.ContextProbeCooldown < 0 {
		return nil, fmt.Errorf("%w: ContextProbeCooldown must not be negative", ErrInvalidProbeSafetyPolicy)
	}
	if policy.PerAccountWindow < 0 {
		return nil, fmt.Errorf("%w: PerAccountWindow must not be negative", ErrInvalidProbeSafetyPolicy)
	}
	for _, c := range policy.PerProbe {
		if c.Max < 0 {
			return nil, fmt.Errorf("%w: PerProbe cap for unit %q must not be negative, got %v", ErrInvalidProbeSafetyPolicy, c.Unit, c.Max)
		}
	}
	for _, c := range policy.PerAccount {
		if c.Max < 0 {
			return nil, fmt.Errorf("%w: PerAccount cap for unit %q must not be negative, got %v", ErrInvalidProbeSafetyPolicy, c.Unit, c.Max)
		}
	}
	if now == nil {
		now = time.Now
	}
	return &ProbeGuard{policy: policy, reserver: reserver, spend: spend, inflight: inflight, cooldown: cooldown, now: now}, nil
}

// ProbeAdmissionRequest is one probe attempt's admission request.
type ProbeAdmissionRequest struct {
	AccountID           string
	ProviderID          string
	OfferingOperationID string
	RequestID           string
	AttemptID           string
	Operation           models.Operation
	Class               ProbeClass
	Cost                quota.EstimateInput
}

// ProbeAdmission is Admit's success result: the reservation obtained and
// the allocations it was reserved against.
type ProbeAdmission struct {
	ReservationID string
	Allocations   []quota.Allocation
}

// Admit evaluates req against every safety gate, in this exact order —
// the first gate that would refuse wins, and ReserveProbe is called only
// after every gate has passed:
//
//  1. Structural validation.
//  2. Expensive-class opt-in.
//  3. Cost estimate + per-probe caps (every emitted unit must have a
//     configured cap; a missing cap refuses).
//  4. Per-account rolling-window caps (prior spend + this probe, same
//     missing-cap rule).
//  5. Per-provider in-flight concurrency cap.
//  6. Context-probe cooldown (only for models.OperationContextWindow).
//  7. Quota reservation.
func (g *ProbeGuard) Admit(ctx context.Context, req ProbeAdmissionRequest) (ProbeAdmission, error) {
	if req.AccountID == "" || req.ProviderID == "" || req.OfferingOperationID == "" || req.RequestID == "" || req.AttemptID == "" {
		return ProbeAdmission{}, fmt.Errorf("%w: account, provider, offering-operation, request, and attempt ids are all required", ErrInvalidProbeAdmissionRequest)
	}
	if _, err := models.ParseOperation(string(req.Operation)); err != nil {
		return ProbeAdmission{}, fmt.Errorf("%w: operation: %v", ErrInvalidProbeAdmissionRequest, err)
	}
	if _, err := ParseProbeClass(string(req.Class)); err != nil {
		return ProbeAdmission{}, fmt.Errorf("%w: class: %v", ErrInvalidProbeAdmissionRequest, err)
	}

	if req.Class == ProbeExpensive && !g.policy.ExpensiveProbesEnabled {
		return ProbeAdmission{}, refuse(RefusalOptInRequired, "expensive probe class is disabled")
	}

	allocations, err := quota.Estimate(req.Cost, quota.DefaultEstimatePolicy())
	if err != nil {
		return ProbeAdmission{}, refuse(RefusalSafetyUnavailable, "cost estimate failed")
	}
	if err := checkCaps(allocations, g.policy.PerProbe); err != nil {
		return ProbeAdmission{}, refuse(RefusalCapped, err.Error())
	}

	now := g.now()
	since := now.Add(-g.policy.PerAccountWindow)
	priorSpend, err := g.spend.ProbeSpendSince(ctx, req.AccountID, since)
	if err != nil {
		return ProbeAdmission{}, refuse(RefusalSafetyUnavailable, "probe spend read failed")
	}
	combined := sumAllocationsByUnit(priorSpend, allocations)
	if err := checkCaps(combined, g.policy.PerAccount); err != nil {
		return ProbeAdmission{}, refuse(RefusalAccountCapped, err.Error())
	}

	inFlight, err := g.inflight.InFlightProbes(ctx, req.ProviderID)
	if err != nil {
		return ProbeAdmission{}, refuse(RefusalSafetyUnavailable, "in-flight probe count read failed")
	}
	if inFlight >= g.policy.MaxInFlightPerProvider {
		return ProbeAdmission{}, refuse(RefusalConcurrency, "max in-flight probes reached for provider")
	}

	if req.Operation == models.OperationContextWindow {
		until, err := g.cooldown.ProbeCooldownUntil(ctx, req.OfferingOperationID)
		if err != nil {
			return ProbeAdmission{}, refuse(RefusalSafetyUnavailable, "cooldown read failed")
		}
		if until != nil && now.Before(*until) {
			return ProbeAdmission{}, refuse(RefusalCoolingDown, "context-probe cooldown is active")
		}
	}

	reservationID, err := g.reserver.ReserveProbe(ctx, req.AccountID, req.RequestID, req.AttemptID, allocations)
	if err != nil {
		return ProbeAdmission{}, refuse(RefusalQuotaRejected, "reservation was rejected")
	}

	return ProbeAdmission{ReservationID: reservationID, Allocations: allocations}, nil
}

// checkCaps fails closed: every allocation's unit must have a configured
// cap (a missing cap is a refusal, never treated as unlimited), and its
// cost must not exceed that cap (equality admits).
func checkCaps(allocations []quota.Allocation, caps []ProbeCostCap) error {
	byUnit := make(map[quota.Unit]float64, len(caps))
	for _, c := range caps {
		byUnit[c.Unit] = c.Max
	}
	for _, a := range allocations {
		max, ok := byUnit[a.Unit]
		if !ok {
			return fmt.Errorf("no cap configured for unit %q", a.Unit)
		}
		if a.Cost > max {
			return fmt.Errorf("unit %q cost %v exceeds cap %v", a.Unit, a.Cost, max)
		}
	}
	return nil
}

// sumAllocationsByUnit merges a and b into one per-unit total, in the
// deterministic order units first appear (across a, then b) — never by
// ranging over a map to build the returned order.
func sumAllocationsByUnit(a, b []quota.Allocation) []quota.Allocation {
	totals := make(map[quota.Unit]float64)
	var order []quota.Unit
	add := func(list []quota.Allocation) {
		for _, alloc := range list {
			if _, seen := totals[alloc.Unit]; !seen {
				order = append(order, alloc.Unit)
			}
			totals[alloc.Unit] += alloc.Cost
		}
	}
	add(a)
	add(b)

	out := make([]quota.Allocation, 0, len(order))
	for _, u := range order {
		out = append(out, quota.Allocation{Unit: u, Cost: totals[u]})
	}
	return out
}

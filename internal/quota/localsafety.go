package quota

import (
	"errors"
	"fmt"
	"time"
)

// DefaultMaxConcurrency is the default cap on concurrent in-flight
// attempts for an account until its provider quota is confirmed (02 §3
// "Local routing-safety budget": "a concurrency window ... with a
// default cap of 1 in-flight until provider quota is confirmed").
const DefaultMaxConcurrency = 1

// DefaultEstimatedConsumptionLimit is the conservative owner-policy
// default cap on estimated local consumption (02 §3) for an account with
// no other guidance.
const DefaultEstimatedConsumptionLimit = 50

// DefaultEstimatedConsumptionWindow is the rolling duration the default
// estimated-consumption window covers.
const DefaultEstimatedConsumptionWindow = time.Hour

// WindowSpec describes one mandatory local-safety window to be created
// (idempotently) for an account.
type WindowSpec struct {
	Source          Source
	Unit            Unit
	WindowType      string
	Key             string
	DurationSeconds *int
	LimitValue      float64
	Confidence      float64
	Freshness       Freshness
}

// LocalSafetyPolicy is the owner-tunable policy backing every account's
// mandatory local-safety budget (02 §3): "unknown provider quota does
// not mean unlimited, and the router never skips a reservation."
type LocalSafetyPolicy struct {
	// MaxConcurrency bounds the local-safety concurrency window.
	MaxConcurrency float64
	// EstimatedConsumptionUnit is the dimension the estimated-consumption
	// window measures. It must never be UnitConcurrency — that dimension
	// already has its own dedicated window.
	EstimatedConsumptionUnit Unit
	// EstimatedConsumptionLimit bounds the estimated-consumption window.
	EstimatedConsumptionLimit float64
	// EstimatedConsumptionWindow is the rolling duration the
	// estimated-consumption window covers.
	EstimatedConsumptionWindow time.Duration
}

// DefaultLocalSafetyPolicy returns the conservative defaults (02 §3).
func DefaultLocalSafetyPolicy() LocalSafetyPolicy {
	return LocalSafetyPolicy{
		MaxConcurrency:             DefaultMaxConcurrency,
		EstimatedConsumptionUnit:   UnitRequests,
		EstimatedConsumptionLimit:  DefaultEstimatedConsumptionLimit,
		EstimatedConsumptionWindow: DefaultEstimatedConsumptionWindow,
	}
}

// ErrInvalidLocalSafetyPolicy is returned by Validate for any policy that
// cannot be turned into a sound pair of mandatory windows.
var ErrInvalidLocalSafetyPolicy = errors.New("quota: invalid local-safety policy")

// Validate fails closed: a non-positive MaxConcurrency or
// EstimatedConsumptionLimit, a non-positive EstimatedConsumptionWindow,
// or an EstimatedConsumptionUnit of UnitConcurrency (the consumption
// window must measure consumption, not concurrency — concurrency already
// has its own dedicated window) is rejected.
func (p LocalSafetyPolicy) Validate() error {
	switch {
	case p.MaxConcurrency <= 0:
		return fmt.Errorf("%w: max concurrency must be positive, got %v", ErrInvalidLocalSafetyPolicy, p.MaxConcurrency)
	case p.EstimatedConsumptionLimit <= 0:
		return fmt.Errorf("%w: estimated consumption limit must be positive, got %v", ErrInvalidLocalSafetyPolicy, p.EstimatedConsumptionLimit)
	case p.EstimatedConsumptionWindow <= 0:
		return fmt.Errorf("%w: estimated consumption window must be positive, got %v", ErrInvalidLocalSafetyPolicy, p.EstimatedConsumptionWindow)
	case p.EstimatedConsumptionUnit == UnitConcurrency:
		return fmt.Errorf("%w: estimated consumption unit must not be concurrency", ErrInvalidLocalSafetyPolicy)
	default:
		return nil
	}
}

// MandatoryWindows returns the exactly-two mandatory local-safety window
// specs (02 §3): the concurrency window, then the estimated-consumption
// window, in that deterministic order. Both carry SourceLocalSafety —
// never stamped as provider evidence.
func (p LocalSafetyPolicy) MandatoryWindows() ([]WindowSpec, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}

	concurrencyKey, err := NormalizeWindowKey(WindowKeyInput{Unit: UnitConcurrency})
	if err != nil {
		return nil, err
	}

	durationSeconds := int(p.EstimatedConsumptionWindow.Seconds())
	consumptionKey, err := NormalizeWindowKey(WindowKeyInput{
		DurationSeconds: &durationSeconds,
		Unit:            p.EstimatedConsumptionUnit,
	})
	if err != nil {
		return nil, err
	}

	return []WindowSpec{
		{
			Source:     SourceLocalSafety,
			Unit:       UnitConcurrency,
			WindowType: "concurrency",
			Key:        concurrencyKey,
			LimitValue: p.MaxConcurrency,
			Confidence: 1,
			Freshness:  FreshnessFresh,
		},
		{
			Source:          SourceLocalSafety,
			Unit:            p.EstimatedConsumptionUnit,
			WindowType:      "estimated_consumption",
			Key:             consumptionKey,
			DurationSeconds: &durationSeconds,
			LimitValue:      p.EstimatedConsumptionLimit,
			Confidence:      1,
			Freshness:       FreshnessFresh,
		},
	}, nil
}

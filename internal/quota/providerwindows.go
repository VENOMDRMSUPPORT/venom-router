package quota

import (
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// ProviderWindowSpec is one provider-evidence quota window derived from
// a QuotaAdapter's QuotaResult (03 §1 / 02 §3).
//
// DEVIATION (disclosed): this batch's prompt suggested extending
// quota.WindowSpec (internal/quota/localsafety.go) additively with these
// same fields. localsafety.go is NOT among this batch's allowed files
// (constraint #10's exclusive list), so editing it would violate that
// explicit constraint. ProviderWindowSpec is therefore a SEPARATE type,
// leaving localsafety.go, WindowSpec, and MandatoryWindows completely
// untouched — their own tests pass unmodified because nothing about them
// changed at all, which is the strongest possible form of "behaviour
// identical."
type ProviderWindowSpec struct {
	Source          Source
	Unit            Unit
	WindowType      string
	Key             string
	DurationSeconds *int
	Used            *float64
	Remaining       *float64
	Total           *float64
	ResetAt         *int64
	Confidence      float64
	Freshness       Freshness
	ObservedAt      time.Time
}

// WindowsFromProviderResult maps a QuotaAdapter's output onto
// provider-evidence window specs. Every spec is stamped
// SourceProviderEvidence and never local_safety (02 §3: the two are
// never conflated). One spec per providers.QuotaWindow — several
// concurrent windows from one fetch is the normal case (03 §1 names it
// explicitly), so this never collapses or de-duplicates by unit. A
// window whose Unit cannot be parsed fails the WHOLE mapping (a window
// we cannot classify must not silently bound nothing), returning nil and
// the typed error rather than coercing it. WindowKey "" (the documented
// "provider supplied none" case) still yields a deterministic synthetic
// key via NormalizeWindowKey. Used/Remaining/Total/ResetAt are carried
// through as-is — nil stays nil, unknown is never coerced to 0.
func WindowsFromProviderResult(res providers.QuotaResult, observedAt time.Time) ([]ProviderWindowSpec, error) {
	specs := make([]ProviderWindowSpec, 0, len(res.Windows))
	for _, w := range res.Windows {
		unit, err := ParseUnit(w.Unit)
		if err != nil {
			return nil, err
		}
		key, err := NormalizeWindowKey(WindowKeyInput{ProviderKey: w.WindowKey, DurationSeconds: w.DurationSeconds, Unit: unit})
		if err != nil {
			return nil, err
		}
		specs = append(specs, ProviderWindowSpec{
			Source:          SourceProviderEvidence,
			Unit:            unit,
			WindowType:      w.WindowType,
			Key:             key,
			DurationSeconds: w.DurationSeconds,
			Used:            w.Used,
			Remaining:       w.Remaining,
			Total:           w.Total,
			ResetAt:         w.ResetAt,
			Confidence:      w.Confidence,
			Freshness:       FreshnessFresh,
			ObservedAt:      observedAt,
		})
	}
	return specs, nil
}

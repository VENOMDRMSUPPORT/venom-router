package quota

import (
	"errors"
	"testing"
	"time"
)

func TestDefaultLocalSafetyPolicy_ConcurrencyCapIsOne(t *testing.T) {
	p := DefaultLocalSafetyPolicy()
	if p.MaxConcurrency != 1 {
		t.Fatalf("DefaultLocalSafetyPolicy().MaxConcurrency = %v, want 1", p.MaxConcurrency)
	}
}

func TestMandatoryWindows_ProducesBothWindows(t *testing.T) {
	specs, err := DefaultLocalSafetyPolicy().MandatoryWindows()
	if err != nil {
		t.Fatalf("MandatoryWindows(): %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("len(specs) = %d, want exactly 2", len(specs))
	}

	concurrency := specs[0]
	if concurrency.Unit != UnitConcurrency || concurrency.WindowType != "concurrency" || concurrency.Key != "local:concurrency" {
		t.Fatalf("concurrency spec = %+v, want unit=concurrency window_type=concurrency key=local:concurrency", concurrency)
	}
	if concurrency.LimitValue != 1 {
		t.Fatalf("concurrency spec LimitValue = %v, want 1", concurrency.LimitValue)
	}
	if concurrency.Source != SourceLocalSafety {
		t.Fatalf("concurrency spec Source = %q, want %q", concurrency.Source, SourceLocalSafety)
	}

	consumption := specs[1]
	if consumption.Unit != UnitRequests || consumption.WindowType != "estimated_consumption" || consumption.Key != "rolling:3600s" {
		t.Fatalf("consumption spec = %+v, want unit=requests window_type=estimated_consumption key=rolling:3600s", consumption)
	}
	if consumption.LimitValue != DefaultEstimatedConsumptionLimit {
		t.Fatalf("consumption spec LimitValue = %v, want %v", consumption.LimitValue, DefaultEstimatedConsumptionLimit)
	}
	if consumption.Source != SourceLocalSafety {
		t.Fatalf("consumption spec Source = %q, want %q", consumption.Source, SourceLocalSafety)
	}
}

func TestMandatoryWindows_SourceIsNeverProviderEvidence(t *testing.T) {
	specs, err := DefaultLocalSafetyPolicy().MandatoryWindows()
	if err != nil {
		t.Fatalf("MandatoryWindows(): %v", err)
	}
	for i, s := range specs {
		if s.Source != SourceLocalSafety {
			t.Fatalf("spec[%d].Source = %q, want %q (never provider evidence)", i, s.Source, SourceLocalSafety)
		}
	}
}

func TestLocalSafetyPolicy_Validate_Table(t *testing.T) {
	base := DefaultLocalSafetyPolicy()

	if err := base.Validate(); err != nil {
		t.Fatalf("default policy Validate(): %v, want success", err)
	}

	tests := []struct {
		name   string
		mutate func(p LocalSafetyPolicy) LocalSafetyPolicy
	}{
		{"zero max concurrency", func(p LocalSafetyPolicy) LocalSafetyPolicy { p.MaxConcurrency = 0; return p }},
		{"negative max concurrency", func(p LocalSafetyPolicy) LocalSafetyPolicy { p.MaxConcurrency = -1; return p }},
		{"zero consumption limit", func(p LocalSafetyPolicy) LocalSafetyPolicy { p.EstimatedConsumptionLimit = 0; return p }},
		{"negative consumption limit", func(p LocalSafetyPolicy) LocalSafetyPolicy { p.EstimatedConsumptionLimit = -1; return p }},
		{"zero consumption window", func(p LocalSafetyPolicy) LocalSafetyPolicy { p.EstimatedConsumptionWindow = 0; return p }},
		{"negative consumption window", func(p LocalSafetyPolicy) LocalSafetyPolicy { p.EstimatedConsumptionWindow = -time.Minute; return p }},
		{"consumption unit is concurrency", func(p LocalSafetyPolicy) LocalSafetyPolicy { p.EstimatedConsumptionUnit = UnitConcurrency; return p }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.mutate(base)
			if err := p.Validate(); !errors.Is(err, ErrInvalidLocalSafetyPolicy) {
				t.Fatalf("Validate() error = %v, want ErrInvalidLocalSafetyPolicy", err)
			}
		})
	}
}

package quota

import "testing"

func TestDefaultReconciliationPolicy_Defaults(t *testing.T) {
	pol := DefaultReconciliationPolicy()
	if pol.MaxRetries != 5 {
		t.Fatalf("MaxRetries = %d, want 5", pol.MaxRetries)
	}
	if pol.BaseBackoff.String() != "30s" {
		t.Fatalf("BaseBackoff = %v, want 30s", pol.BaseBackoff)
	}
	if pol.MaxBackoff.String() != "30m0s" {
		t.Fatalf("MaxBackoff = %v, want 30m", pol.MaxBackoff)
	}
	if pol.BatchSize != 20 {
		t.Fatalf("BatchSize = %d, want 20", pol.BatchSize)
	}
}

func TestReconciliationOutcome_ZeroValue(t *testing.T) {
	var o ReconciliationOutcome
	if o.ReservationID != "" || o.Outcome != "" || o.Actuals != nil {
		t.Fatalf("zero ReconciliationOutcome = %+v, want all zero", o)
	}
}

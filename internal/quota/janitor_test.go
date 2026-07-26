package quota

import (
	"testing"
	"time"
)

func TestDefaultRetryDeadline_Is30Minutes(t *testing.T) {
	if DefaultRetryDeadline != 30*time.Minute {
		t.Fatalf("DefaultRetryDeadline = %v, want 30m", DefaultRetryDeadline)
	}
}

func TestDefaultJanitorBatchSize_Is100(t *testing.T) {
	if DefaultJanitorBatchSize != 100 {
		t.Fatalf("DefaultJanitorBatchSize = %d, want 100", DefaultJanitorBatchSize)
	}
}

func TestJanitorResult_ZeroValueIsAllZero(t *testing.T) {
	var r JanitorResult
	if r.Released != 0 || r.Pended != 0 || r.UnknownConsumption != 0 {
		t.Fatalf("zero JanitorResult = %+v, want all zero", r)
	}
}

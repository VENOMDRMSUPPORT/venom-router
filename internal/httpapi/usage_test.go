package httpapi

import (
	"errors"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/routing"
)

// TestUsageStatus_MapsTerminalErrors proves each recognized terminal error maps
// to its closed typed status code — and any unrecognized error collapses to the
// generic "failure" (never leaking the raw error text as a status).
func TestUsageStatus_MapsTerminalErrors(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, "success"},
		{routing.ErrNoEligibleOffering, "no_eligible_offering"},
		{routing.ErrContextExceedsTier, "context_exceeds_tier"},
		{routing.ErrCapabilityUnsupported, "capability_unsupported"},
		{errors.New("upstream 500: secret provider detail"), "failure"},
	}
	for _, c := range cases {
		if got := usageStatus(c.err); got != c.want {
			t.Errorf("usageStatus(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

// TestBuildUsageRecord_IdsOnly proves the usage record carries correlation ids,
// tier, and a typed status — and nothing from the request or response content.
// A zero-valued FallbackResult leaves provider/account/attempts NULL (nil),
// never a fabricated empty-string id.
func TestBuildUsageRecord_IdsOnly(t *testing.T) {
	key := "key-9"
	rec := buildUsageRecord("u1", "req-1", &key, "pro",
		routing.FallbackResult{ProviderID: "opencode-zen", AccountID: "acct-1", Attempts: 3}, nil)

	if rec.ID != "u1" || rec.RequestID != "req-1" || rec.Tier != "pro" || rec.Status != "success" {
		t.Fatalf("identity fields wrong: %+v", rec)
	}
	if rec.APIKeyID == nil || *rec.APIKeyID != "key-9" {
		t.Fatalf("api key attribution missing: %+v", rec.APIKeyID)
	}
	if rec.ProviderID == nil || *rec.ProviderID != "opencode-zen" {
		t.Fatalf("provider id = %v, want opencode-zen", rec.ProviderID)
	}
	if rec.FallbackAttempts == nil || *rec.FallbackAttempts != 3 {
		t.Fatalf("fallback attempts = %v, want 3", rec.FallbackAttempts)
	}

	// A zero result → provider/account/attempts stay nil (UNKNOWN), never "".
	empty := buildUsageRecord("u2", "req-2", nil, "lite", routing.FallbackResult{}, routing.ErrNoEligibleOffering)
	if empty.ProviderID != nil || empty.AccountID != nil || empty.FallbackAttempts != nil {
		t.Fatalf("zero result must leave ids nil, got %+v", empty)
	}
	if empty.Status != "no_eligible_offering" {
		t.Fatalf("status = %q, want no_eligible_offering", empty.Status)
	}
}

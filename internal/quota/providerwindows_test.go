package quota

import (
	"errors"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

func float64PtrPW(v float64) *float64 { return &v }
func intPtrPW(v int) *int             { return &v }

// TestWindowsFromProviderResult_MultipleWindowsFromOneFetch is the
// card's named test: a single fetch reporting several concurrent
// windows (an rpm window, a rolling_7d window, and a balance window)
// yields one spec per window, all SourceProviderEvidence, each with its
// own distinct key — never collapsed or de-duplicated by unit.
func TestWindowsFromProviderResult_MultipleWindowsFromOneFetch(t *testing.T) {
	observedAt := time.Unix(5000, 0)
	res := providers.QuotaResult{
		Windows: []providers.QuotaWindow{
			{Unit: "requests", WindowType: "rpm", WindowKey: "rpm", Remaining: float64PtrPW(90), Confidence: 0.9},
			{Unit: "requests", WindowType: "rolling_7d", WindowKey: "seven_day", Remaining: float64PtrPW(900), Confidence: 0.9},
			{Unit: "balance", WindowType: "balance", WindowKey: "", Remaining: float64PtrPW(50), Confidence: 0.8},
		},
	}

	specs, err := WindowsFromProviderResult(res, observedAt)
	if err != nil {
		t.Fatalf("WindowsFromProviderResult: %v", err)
	}
	if len(specs) != 3 {
		t.Fatalf("len(specs) = %d, want 3", len(specs))
	}

	seenKeys := make(map[string]bool, 3)
	for _, s := range specs {
		if s.Source != SourceProviderEvidence {
			t.Fatalf("spec.Source = %q, want provider_evidence", s.Source)
		}
		if seenKeys[s.Key] {
			t.Fatalf("duplicate key %q across specs, want each window to keep its own", s.Key)
		}
		seenKeys[s.Key] = true
	}
}

// TestWindowsFromProviderResult_EmptyProviderKeyGetsSyntheticKey proves
// WindowKey: "" still produces a deterministic, non-empty key: a
// duration-bearing window gets "rolling:<n>s", a durationless one gets
// "local:<unit>" — never the empty string.
func TestWindowsFromProviderResult_EmptyProviderKeyGetsSyntheticKey(t *testing.T) {
	observedAt := time.Unix(5000, 0)

	withDuration := providers.QuotaResult{Windows: []providers.QuotaWindow{
		{Unit: "requests", WindowType: "rolling", WindowKey: "", DurationSeconds: intPtrPW(3600), Confidence: 0.9},
	}}
	specs, err := WindowsFromProviderResult(withDuration, observedAt)
	if err != nil {
		t.Fatalf("WindowsFromProviderResult (with duration): %v", err)
	}
	if len(specs) != 1 || specs[0].Key != "rolling:3600s" {
		t.Fatalf("specs = %+v, want key rolling:3600s", specs)
	}

	withoutDuration := providers.QuotaResult{Windows: []providers.QuotaWindow{
		{Unit: "balance", WindowType: "balance", WindowKey: "", Confidence: 0.9},
	}}
	specs2, err := WindowsFromProviderResult(withoutDuration, observedAt)
	if err != nil {
		t.Fatalf("WindowsFromProviderResult (without duration): %v", err)
	}
	if len(specs2) != 1 || specs2[0].Key != "local:balance" {
		t.Fatalf("specs = %+v, want key local:balance", specs2)
	}
}

// TestWindowsFromProviderResult_UnknownUnitFailsClosed proves a window
// reporting a unit outside quota's canonical eight fails the WHOLE
// mapping (never coerced), while a positive control with a known unit
// succeeds.
func TestWindowsFromProviderResult_UnknownUnitFailsClosed(t *testing.T) {
	bad := providers.QuotaResult{Windows: []providers.QuotaWindow{
		{Unit: "widgets", WindowType: "rpm", WindowKey: "rpm"},
	}}
	specs, err := WindowsFromProviderResult(bad, time.Unix(5000, 0))
	if !errors.Is(err, ErrUnknownUnit) {
		t.Fatalf("WindowsFromProviderResult error = %v, want ErrUnknownUnit", err)
	}
	if specs != nil {
		t.Fatalf("specs = %+v, want nil on error", specs)
	}

	good := providers.QuotaResult{Windows: []providers.QuotaWindow{
		{Unit: "requests", WindowType: "rpm", WindowKey: "rpm"},
	}}
	specs2, err := WindowsFromProviderResult(good, time.Unix(5000, 0))
	if err != nil || len(specs2) != 1 || specs2[0].Unit != UnitRequests {
		t.Fatalf("WindowsFromProviderResult (known unit) = (%+v, %v), want one requests spec", specs2, err)
	}
}

// TestWindowsFromProviderResult_PreservesUnknowns proves nil
// Used/Remaining/Total/ResetAt stay nil in the mapped spec — unknown is
// never coerced to 0.
func TestWindowsFromProviderResult_PreservesUnknowns(t *testing.T) {
	res := providers.QuotaResult{Windows: []providers.QuotaWindow{
		{Unit: "requests", WindowType: "rpm", WindowKey: "rpm"},
	}}
	specs, err := WindowsFromProviderResult(res, time.Unix(5000, 0))
	if err != nil {
		t.Fatalf("WindowsFromProviderResult: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("len(specs) = %d, want 1", len(specs))
	}
	s := specs[0]
	if s.Used != nil || s.Remaining != nil || s.Total != nil || s.ResetAt != nil {
		t.Fatalf("spec = %+v, want Used/Remaining/Total/ResetAt all nil", s)
	}
}

// TestWindowsFromProviderResult_PercentUnitSemantics proves the percent
// unit's definitional completion: a percent window reporting one side gets
// Total=100 and the 100−x complement (clinepass/claude-code report
// percentUsed; a remaining-only reporter works symmetrically) — while a
// percent window with NEITHER side stays fully unknown, so "no utilization
// data" can never read as full headroom (05 §4 fail-closed).
func TestWindowsFromProviderResult_PercentUnitSemantics(t *testing.T) {
	used := 37.0
	remaining := 25.0
	res := providers.QuotaResult{Windows: []providers.QuotaWindow{
		{Unit: "percent", WindowType: "rolling_5h", Used: &used},
		{Unit: "percent", WindowType: "rolling_7d", Remaining: &remaining},
		{Unit: "percent", WindowType: "rolling_30d"}, // neither side reported
	}}
	specs, err := WindowsFromProviderResult(res, time.Unix(5000, 0))
	if err != nil {
		t.Fatalf("WindowsFromProviderResult: %v", err)
	}
	if len(specs) != 3 {
		t.Fatalf("len(specs) = %d, want 3", len(specs))
	}

	usedSide := specs[0]
	if usedSide.Total == nil || *usedSide.Total != 100 {
		t.Fatalf("used-side Total = %v, want 100 (percent scale)", usedSide.Total)
	}
	if usedSide.Remaining == nil || *usedSide.Remaining != 63 {
		t.Fatalf("used-side Remaining = %v, want 63 (100-37)", usedSide.Remaining)
	}

	remSide := specs[1]
	if remSide.Total == nil || *remSide.Total != 100 {
		t.Fatalf("remaining-side Total = %v, want 100", remSide.Total)
	}
	if remSide.Used == nil || *remSide.Used != 75 {
		t.Fatalf("remaining-side Used = %v, want 75 (100-25)", remSide.Used)
	}

	unknown := specs[2]
	if unknown.Used != nil || unknown.Remaining != nil || unknown.Total != nil {
		t.Fatalf("no-data percent window = %+v, want Used/Remaining/Total all nil (never fabricated)", unknown)
	}
}

// TestCooldownTrigger_Validate is table-driven over the fail-closed
// cases plus the one valid trigger.
func TestCooldownTrigger_Validate(t *testing.T) {
	now := time.Unix(1000, 0)

	cases := []struct {
		name    string
		trigger CooldownTrigger
		wantErr bool
	}{
		{"valid", CooldownTrigger{Scope: CooldownScopeAccount, ScopeRef: "acct-1", Until: now.Add(time.Minute), Source: CooldownSourceRetryAfter, ReasonCode: "rate_limited"}, false},
		{"unknown scope", CooldownTrigger{Scope: "region", ScopeRef: "acct-1", Until: now.Add(time.Minute), Source: CooldownSourceRetryAfter, ReasonCode: "rate_limited"}, true},
		{"empty scope ref", CooldownTrigger{Scope: CooldownScopeAccount, ScopeRef: "", Until: now.Add(time.Minute), Source: CooldownSourceRetryAfter, ReasonCode: "rate_limited"}, true},
		{"empty reason code", CooldownTrigger{Scope: CooldownScopeAccount, ScopeRef: "acct-1", Until: now.Add(time.Minute), Source: CooldownSourceRetryAfter, ReasonCode: ""}, true},
		{"unknown source", CooldownTrigger{Scope: CooldownScopeAccount, ScopeRef: "acct-1", Until: now.Add(time.Minute), Source: "manual", ReasonCode: "rate_limited"}, true},
		{"past until", CooldownTrigger{Scope: CooldownScopeAccount, ScopeRef: "acct-1", Until: now.Add(-time.Minute), Source: CooldownSourceRetryAfter, ReasonCode: "rate_limited"}, true},
		{"until equal to now", CooldownTrigger{Scope: CooldownScopeAccount, ScopeRef: "acct-1", Until: now, Source: CooldownSourceRetryAfter, ReasonCode: "rate_limited"}, true},
	}
	for _, tc := range cases {
		err := tc.trigger.Validate(now)
		if tc.wantErr && err == nil {
			t.Fatalf("%s: Validate() succeeded, want rejection", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Fatalf("%s: Validate() = %v, want success", tc.name, err)
		}
	}
}

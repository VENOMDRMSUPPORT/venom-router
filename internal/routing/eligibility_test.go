package routing

import (
	"testing"
	"time"

	accountsdomain "github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
)

func elCand(id, provider, model string, funding accountsdomain.Funding) CandidateOffering {
	return CandidateOffering{
		AccountID:       id,
		ProviderID:      provider,
		ProviderModelID: model,
		AccountHealth:   accountsdomain.HealthHealthy,
		Funding:         funding,
	}
}

func strptr(s string) *string { return &s }

// TestFilterEligible_NeverCrossesFunding proves the fallback eligibility filter
// never admits a candidate that violates the tier's funding rule — a Lite
// (free-only) request yields NO paid candidate, even under total free
// exhaustion (no free candidate present at all).
//
// Mutation row R14-M9: skip the funding gate on the exhaustion path → a paid
// candidate is admitted for Lite → this test RED.
func TestFilterEligible_NeverCrossesFunding(t *testing.T) {
	now := drrTestNow
	paidOnly := []CandidateOffering{elCand("a", "p", "m", accountsdomain.FundingPaid)}

	got := FilterEligible(paidOnly, EligibilityInput{Funding: FundingFreeOnly}, now)
	if len(got) != 0 {
		t.Fatalf("Lite (free-only) must never admit a paid candidate; got %d", len(got))
	}

	// With a free candidate present, only the free one passes.
	mixed := []CandidateOffering{
		elCand("free", "p", "m", accountsdomain.FundingFree),
		elCand("paid", "p", "m2", accountsdomain.FundingPaid),
	}
	got = FilterEligible(mixed, EligibilityInput{Funding: FundingFreeOnly}, now)
	if len(got) != 1 || got[0].AccountID != "free" {
		t.Fatalf("Lite must admit only the free candidate; got %v", accountIDs(got))
	}
}

// TestFilterEligible_ExcludesCooldownAndBreaker proves candidates covered by an
// active cooldown or an open breaker at any scope are removed, and that a
// candidate clear on all scopes survives.
func TestFilterEligible_ExcludesCooldownAndBreaker(t *testing.T) {
	now := drrTestNow
	cands := []CandidateOffering{
		elCand("acc-cool", "prov1", "model-x", accountsdomain.FundingFree),
		elCand("acc-breaker", "prov2", "model-y", accountsdomain.FundingFree),
		elCand("acc-ok", "prov3", "model-z", accountsdomain.FundingFree),
	}
	in := EligibilityInput{
		Funding: FundingFreeAndPaid,
		Cooldowns: []quota.Cooldown{
			{Scope: quota.CooldownScopeAccount, AccountID: strptr("acc-cool"), Until: now.Add(time.Minute)},
		},
		Breakers: BreakerSet{
			Provider: map[string]Breaker{"prov2": Breaker{}.Trip(now)}, // open → blocks
		},
	}
	got := FilterEligible(cands, in, now)
	if len(got) != 1 || got[0].AccountID != "acc-ok" {
		t.Fatalf("expected only acc-ok to survive; got %v", accountIDs(got))
	}
}

// TestFilterEligible_ExpiredCooldownAndHalfOpenAdmit proves an EXPIRED cooldown
// no longer excludes, and a breaker whose window has expired (lazily half-open)
// admits the route again — recovery with no timer.
func TestFilterEligible_ExpiredCooldownAndHalfOpenAdmit(t *testing.T) {
	now := drrTestNow
	cands := []CandidateOffering{elCand("acc", "prov", "model", accountsdomain.FundingFree)}
	in := EligibilityInput{
		Funding: FundingFreeAndPaid,
		Cooldowns: []quota.Cooldown{
			{Scope: quota.CooldownScopeAccount, AccountID: strptr("acc"), Until: now.Add(-time.Second)}, // already expired
		},
		Breakers: BreakerSet{
			Provider: map[string]Breaker{"prov": Breaker{}.Trip(now.Add(-BreakerBaseTimeout))}, // window elapsed → half-open
		},
	}
	got := FilterEligible(cands, in, now)
	if len(got) != 1 {
		t.Fatalf("expired cooldown + half-open breaker should admit the route; got %d", len(got))
	}
}

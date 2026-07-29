package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accountsdomain "github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/routing"
)

// TestRoutingErrorFor_CodeAndStatus maps each stable routing error to its code
// and HTTP status (05 §5).
//
// Mutation row R15-M2: map venom_no_eligible_offering to 500 → the 503 row RED.
func TestRoutingErrorFor_CodeAndStatus(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantCode   string
		wantStatus int
		wantRetry  bool
	}{
		{"context_exceeds_tier", routing.ErrContextExceedsTier, "venom_context_exceeds_tier", 400, false},
		{"invalid_extension", fmt.Errorf("wrap: %w", routing.ErrInvalidExtension), "venom_invalid_extension", 400, false},
		{"no_eligible_offering", &routing.NoEligibleOfferingError{Attempts: 3}, "venom_no_eligible_offering", 503, true},
		{"free_capacity_exhausted", &routing.NoEligibleOfferingError{Attempts: 3, RetryAfter: 5 * time.Second}, "venom_free_capacity_exhausted", 429, true},
		{"capability_unsupported_400", &routing.CapabilityUnsupportedError{Capability: "vision", TierStructural: true}, "venom_capability_unsupported", 400, false},
		{"capability_unsupported_501", &routing.CapabilityUnsupportedError{Capability: "tools", TierStructural: false}, "venom_capability_unsupported", 501, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env, ok := RoutingErrorFor(tc.err)
			if !ok {
				t.Fatalf("RoutingErrorFor did not recognize %v", tc.err)
			}
			if env.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", env.Code, tc.wantCode)
			}
			if env.HTTPStatus != tc.wantStatus {
				t.Errorf("status = %d, want %d", env.HTTPStatus, tc.wantStatus)
			}
			if env.Retryable != tc.wantRetry {
				t.Errorf("retryable = %v, want %v", env.Retryable, tc.wantRetry)
			}
		})
	}
}

// TestRoutingErrorFor_Unrecognized proves an unrelated error is not claimed.
func TestRoutingErrorFor_Unrecognized(t *testing.T) {
	if _, ok := RoutingErrorFor(errors.New("some other error")); ok {
		t.Fatalf("RoutingErrorFor must not claim an unrelated error")
	}
}

// TestWriteRoutingError_RetryAfterSurfaced proves retry_after is surfaced on the
// 429 exhaustion path — in the body and the Retry-After header.
func TestWriteRoutingError_RetryAfterSurfaced(t *testing.T) {
	rec := httptest.NewRecorder()
	ok := writeRoutingError(rec, &routing.NoEligibleOfferingError{Attempts: 4, RetryAfter: 5 * time.Second})
	if !ok {
		t.Fatalf("writeRoutingError should have rendered the error")
	}
	if rec.Code != 429 {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "5" {
		t.Fatalf("Retry-After header = %q, want 5", got)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				RetryAfter int `json:"retry_after"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error.Code != "venom_free_capacity_exhausted" {
		t.Fatalf("code = %q", body.Error.Code)
	}
	if body.Error.Details.RetryAfter != 5 {
		t.Fatalf("body retry_after = %d, want 5", body.Error.Details.RetryAfter)
	}
}

// TestWriteRoutingError_NoSecretLeak plants BOTH a credential-shaped token and a
// plain marker in a routing error's own string field and asserts NEITHER
// reaches the rendered envelope — the renderer uses fixed safe messages and
// never echoes an error's fields or a wrapped raw provider message.
//
// Mutation row R15-M3: render err.Error() (or e.Capability) into the message →
// the canary appears → this test RED.
func TestWriteRoutingError_NoSecretLeak(t *testing.T) {
	const credCanary = "Bearer sk-SECRETcanary1234567890"
	const plainCanary = "PLAIN-CANARY-marker-7f3a"

	// The canary rides in the capability field AND in wrapped raw messages.
	capErr := &routing.CapabilityUnsupportedError{Capability: "vision " + plainCanary + " " + credCanary}
	wrapped := fmt.Errorf("provider raw: %s %s: %w", credCanary, plainCanary, routing.ErrNoEligibleOffering)

	// GOVERNOR ADDITION: `wrapped` above wraps the SENTINEL, which RoutingErrorFor
	// does not recognize (errors.As for *NoEligibleOfferingError fails), so
	// writeRoutingError writes NOTHING and that case passed vacuously — leaving
	// the exhaustion branch's rendering entirely uncovered by this canary.
	// Wrapping the TYPED error instead keeps errors.As matching, so a
	// canary-carrying error genuinely reaches the 429/503 branch. Same for the
	// context and extension branches, so all five codes are covered.
	wrappedTyped := fmt.Errorf("provider raw: %s %s: %w", credCanary, plainCanary,
		&routing.NoEligibleOfferingError{Attempts: 2, RetryAfter: 3 * time.Second})
	wrappedNoRetry := fmt.Errorf("provider raw: %s %s: %w", credCanary, plainCanary,
		&routing.NoEligibleOfferingError{Attempts: 2})
	wrappedContext := fmt.Errorf("provider raw: %s %s: %w", credCanary, plainCanary, routing.ErrContextExceedsTier)
	wrappedExtension := fmt.Errorf("provider raw: %s %s: %w", credCanary, plainCanary, routing.ErrInvalidExtension)

	for _, err := range []error{
		capErr, wrapped, &routing.NoEligibleOfferingError{Attempts: 1},
		wrappedTyped, wrappedNoRetry, wrappedContext, wrappedExtension,
	} {
		rec := httptest.NewRecorder()
		writeRoutingError(rec, err)
		body := rec.Body.String()
		if strings.Contains(body, plainCanary) || strings.Contains(body, credCanary) || strings.Contains(body, "sk-SECRET") {
			t.Fatalf("secret leaked into rendered envelope for %T: %s", err, body)
		}
	}
}

// --- Lite fail-closed, end-to-end through the loop --------------------------

// liteLoopFakes implements every routing port so the Lite exhaustion path can
// be driven through RunFallbackLoop from the composition layer.
type liteLoopFakes struct {
	executed   []string
	retryAfter time.Duration
}

func (f *liteLoopFakes) RecordAttempt(_ context.Context, _ routing.AttemptRecord) error { return nil }
func (f *liteLoopFakes) Reserve(_ context.Context, p routing.ReserveParams) (routing.ReserveResult, error) {
	return routing.ReserveResult{ReservationID: p.ReservationID}, nil
}
func (f *liteLoopFakes) MarkDispatched(_ context.Context, _ string) error { return nil }
func (f *liteLoopFakes) Settle(_ context.Context, _ string, _ map[quota.Unit]float64) error {
	return nil
}
func (f *liteLoopFakes) SettleEstimate(_ context.Context, _ string) error            { return nil }
func (f *liteLoopFakes) Release(_ context.Context, _ string) error                   { return nil }
func (f *liteLoopFakes) MarkReconciliationPending(_ context.Context, _ string) error { return nil }
func (f *liteLoopFakes) Execute(_ context.Context, a routing.ResolvedAttempt) routing.ExecOutcome {
	f.executed = append(f.executed, a.Candidate.AccountID)
	return routing.ExecOutcome{Err: errors.New("provider down"), RetryAfter: f.retryAfter}
}
func (f *liteLoopFakes) Classify(_ error) routing.ReconcileVerdict {
	return routing.VerdictUnknownConsumption
}
func (f *liteLoopFakes) ReEvaluate(_ context.Context) (routing.RoutePool, error) {
	return routing.RoutePool{}, nil
}
func (f *liteLoopFakes) MintAttemptID(requestID string, n int) string {
	return fmt.Sprintf("%s#att%d", requestID, n)
}

func f64ptr(v float64) *float64 { return &v }

// TestLiteFailsClosedEndToEnd drives a Lite request to exhaustion through the
// real loop with a paid candidate present, then maps the returned error — and
// asserts the code is one of the two allowed exhaustion codes AND the paid
// account was NEVER executed (Lite never escalates to paid).
//
// Mutation row R15-M1: let the Lite funding gate be skipped → the paid account
// is executed → this test RED.
func TestLiteFailsClosedEndToEnd(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	ps, err := routing.Policies()
	if err != nil {
		t.Fatalf("Policies: %v", err)
	}

	freeWin := quota.Window{Remaining: f64ptr(10_000), Freshness: quota.FreshnessFresh, ObservedAt: now}
	// The paid account is deliberately MORE attractive on capacity fairness
	// (10x headroom), so if the Lite funding gate were ever skipped, fair
	// selection would pick it — the mutation would then execute paid.
	paidWin := quota.Window{Remaining: f64ptr(100_000), Freshness: quota.FreshnessFresh, ObservedAt: now}
	free := routing.CandidateOffering{AccountID: "free-acc", ProviderID: "P", ProviderModelID: "M", AccountHealth: accountsdomain.HealthHealthy, Funding: accountsdomain.FundingFree, QuotaWindows: []quota.Window{freeWin}}
	paid := routing.CandidateOffering{AccountID: "paid-acc", ProviderID: "P", ProviderModelID: "M2", AccountHealth: accountsdomain.HealthHealthy, Funding: accountsdomain.FundingPaid, QuotaWindows: []quota.Window{paidWin}}

	fakes := &liteLoopFakes{retryAfter: 7 * time.Second}
	in := routing.FallbackInput{
		Tier:          routing.TierLite,
		Policy:        ps[routing.TierLite],
		Pool:          routing.RoutePool{Groups: []routing.RouteGroup{{ProviderID: "P", ProviderModelID: "M", Funding: accountsdomain.FundingFree, Members: []routing.CandidateOffering{free, paid}}}},
		Requirements:  routing.Requirements{TextModality: true, ContextTokens: 100},
		RequestID:     "req-lite",
		DRRState:      routing.DRRState{},
		Need:          1,
		EstimateInput: quota.EstimateInput{InputTokens: intptr(10), MaxOutputTokens: intptr(20)},
		Now:           now,
		StaleAfter:    quota.DefaultStalenessWindow,
		Recorder:      fakes, Reserver: fakes, Lifecycle: fakes, Executor: fakes,
		Classifier: fakes, ReEvaluator: fakes, Minter: fakes,
	}

	_, loopErr := routing.RunFallbackLoop(context.Background(), in)
	if loopErr == nil {
		t.Fatalf("expected exhaustion error")
	}
	env, ok := RoutingErrorFor(loopErr)
	if !ok {
		t.Fatalf("loop error not recognized: %v", loopErr)
	}
	if env.Code != "venom_free_capacity_exhausted" && env.Code != "venom_no_eligible_offering" {
		t.Fatalf("Lite exhaustion produced %q, want a fail-closed exhaustion code", env.Code)
	}
	if env.HTTPStatus != 429 && env.HTTPStatus != 503 {
		t.Fatalf("Lite exhaustion status = %d, want 429 or 503", env.HTTPStatus)
	}
	for _, acc := range fakes.executed {
		if acc == "paid-acc" {
			t.Fatalf("Lite escalated to a paid account: executed=%v", fakes.executed)
		}
	}
	if len(fakes.executed) == 0 {
		t.Fatalf("expected the free account to be attempted at least once")
	}
}

func intptr(v int) *int { return &v }

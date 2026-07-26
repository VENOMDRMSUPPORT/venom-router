package intelligence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// fakeAdapter is a providers.ModelDiscoveryAdapter test double.
type fakeAdapter struct {
	models []providers.DiscoveredModel
	err    error
	calls  int
}

func (f *fakeAdapter) DiscoverModels(ctx context.Context, creds providers.StoredCredentials) ([]providers.DiscoveredModel, error) {
	f.calls++
	return f.models, f.err
}

// fakeLeaser is a CredentialLeaser test double. If failWithoutCalling is
// set, Use returns err without ever invoking fn — simulating a decrypt/
// lookup failure where fn (and thus the adapter) never runs.
type fakeLeaser struct {
	err                error
	failWithoutCalling bool
	lastCredentialID   string
}

func (f *fakeLeaser) Use(ctx context.Context, credentialID string, fn func(plaintext []byte) error) error {
	f.lastCredentialID = credentialID
	if f.failWithoutCalling {
		return f.err
	}
	if err := fn([]byte("plaintext-key")); err != nil {
		return err
	}
	return f.err
}

// fakeGeneration is a GenerationAllocator test double: it hands out
// caller-scripted generations in order, one per BeginRun call.
type fakeGeneration struct {
	generations []int64
	next        int
	err         error
	calls       []struct {
		accountID string
		runID     string
	}
}

func (f *fakeGeneration) BeginRun(ctx context.Context, accountID, runID string, now time.Time) (int64, error) {
	f.calls = append(f.calls, struct {
		accountID string
		runID     string
	}{accountID, runID})
	if f.err != nil {
		return 0, f.err
	}
	if f.next >= len(f.generations) {
		return 0, errors.New("fakeGeneration: no more scripted generations")
	}
	g := f.generations[f.next]
	f.next++
	return g, nil
}

// fakeApplier is a SnapshotApplier test double that records every call it
// receives, so a test can assert both which port method fired and exactly
// what it was given.
type fakeApplier struct {
	applyResult    bool
	applyErr       error
	markFailedErr  error
	applyCalls     []DiscoverySnapshot
	applyRunIDs    []string
	markFailedRuns []string
	markFailedCode []string
}

func (f *fakeApplier) Apply(ctx context.Context, runID string, snapshot DiscoverySnapshot, now time.Time) (bool, error) {
	f.applyCalls = append(f.applyCalls, snapshot)
	f.applyRunIDs = append(f.applyRunIDs, runID)
	if f.applyErr != nil {
		return false, f.applyErr
	}
	return f.applyResult, nil
}

func (f *fakeApplier) MarkFailed(ctx context.Context, runID, reasonCode string, now time.Time) error {
	f.markFailedRuns = append(f.markFailedRuns, runID)
	f.markFailedCode = append(f.markFailedCode, reasonCode)
	return f.markFailedErr
}

func fixedNow() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

func intPtr(v int) *int { return &v }

func TestDiscoveryService_ValidNonEmptyList_Applies(t *testing.T) {
	adapter := &fakeAdapter{models: []providers.DiscoveredModel{
		{
			ProviderModelID: "gpt-x",
			DisplayName:     "GPT-X",
			ContextLength:   intPtr(128000),
			Capabilities:    []string{"chat", "vision", "not_a_real_operation"},
			Pricing:         map[string]any{"input_per_1k": 0.01},
		},
	}}
	leaser := &fakeLeaser{}
	gen := &fakeGeneration{generations: []int64{1}}
	applier := &fakeApplier{applyResult: true}

	svc := NewDiscoveryService(adapter, leaser, gen, applier, fixedNow)
	result, err := svc.Run(context.Background(), RunParams{AccountID: "acct1", ProviderID: "prov1", CredentialID: "cred1", RunID: "run1"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Outcome != OutcomeApplied || result.ModelCount != 1 || result.Generation != 1 {
		t.Fatalf("result = %+v, want Applied/1/gen1", result)
	}
	if len(applier.applyCalls) != 1 {
		t.Fatalf("Apply called %d times, want 1", len(applier.applyCalls))
	}
	snap := applier.applyCalls[0]
	if snap.Withdraw {
		t.Fatalf("snapshot.Withdraw = true, want false for a non-empty list")
	}
	if len(snap.Models) != 1 {
		t.Fatalf("snapshot has %d models, want 1", len(snap.Models))
	}
	m := snap.Models[0]
	if m.CanonicalKey == "" {
		t.Fatalf("CanonicalKey is empty")
	}
	if len(m.Operations) != 2 {
		t.Fatalf("Operations = %v, want exactly [chat, vision] (unrecognized capability must be dropped from Operations)", m.Operations)
	}
	if len(m.Capabilities) != 3 {
		t.Fatalf("Capabilities = %v, want all 3 raw strings retained", m.Capabilities)
	}
	if leaser.lastCredentialID != "cred1" {
		t.Fatalf("leaser was given credential %q, want cred1", leaser.lastCredentialID)
	}
}

// TestDiscoveryService_ExplicitEmptyList_Withdraws proves an explicit empty
// list is treated as an authoritative, applied withdrawal — never a
// failure (04 §1 "Semantics"). MUTATION: treating an empty list as a
// failure (calling MarkFailed instead of apply) turns this RED.
func TestDiscoveryService_ExplicitEmptyList_Withdraws(t *testing.T) {
	adapter := &fakeAdapter{models: nil, err: nil}
	leaser := &fakeLeaser{}
	gen := &fakeGeneration{generations: []int64{1}}
	applier := &fakeApplier{applyResult: true}

	svc := NewDiscoveryService(adapter, leaser, gen, applier, fixedNow)
	result, err := svc.Run(context.Background(), RunParams{AccountID: "acct1", ProviderID: "prov1", CredentialID: "cred1", RunID: "run1"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Outcome != OutcomeApplied {
		t.Fatalf("Outcome = %q, want applied (an explicit empty list is an authoritative withdraw, not a failure)", result.Outcome)
	}
	if len(applier.markFailedRuns) != 0 {
		t.Fatalf("MarkFailed was called, want it never called for an explicit empty list")
	}
	if len(applier.applyCalls) != 1 || !applier.applyCalls[0].Withdraw {
		t.Fatalf("applyCalls = %+v, want exactly one call with Withdraw=true", applier.applyCalls)
	}
	if len(applier.applyCalls[0].Models) != 0 {
		t.Fatalf("withdraw snapshot has %d models, want 0", len(applier.applyCalls[0].Models))
	}
}

// TestDiscoveryService_AdapterError_FailsKeepsLastKnownGood proves a
// discovery adapter error is a whole-run failure that never reaches Apply
// (so no offering is ever touched). MUTATION: ignoring discoverErr and
// falling through to apply anyway turns this RED (Apply would be called).
func TestDiscoveryService_AdapterError_FailsKeepsLastKnownGood(t *testing.T) {
	adapter := &fakeAdapter{err: errors.New("upstream exploded")}
	leaser := &fakeLeaser{}
	gen := &fakeGeneration{generations: []int64{1}}
	applier := &fakeApplier{applyResult: true}

	svc := NewDiscoveryService(adapter, leaser, gen, applier, fixedNow)
	result, err := svc.Run(context.Background(), RunParams{AccountID: "acct1", ProviderID: "prov1", CredentialID: "cred1", RunID: "run1"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Outcome != OutcomeFailed || result.ReasonCode != ReasonDiscoveryFailed {
		t.Fatalf("result = %+v, want Failed/%s", result, ReasonDiscoveryFailed)
	}
	if len(applier.applyCalls) != 0 {
		t.Fatalf("Apply was called %d times, want 0 (a discovery failure must never mutate offerings)", len(applier.applyCalls))
	}
	if len(applier.markFailedRuns) != 1 || applier.markFailedRuns[0] != "run1" {
		t.Fatalf("markFailedRuns = %v, want [run1]", applier.markFailedRuns)
	}
}

func TestDiscoveryService_CredentialLeaseFailure_Fails(t *testing.T) {
	adapter := &fakeAdapter{models: []providers.DiscoveredModel{{ProviderModelID: "m1"}}}
	leaser := &fakeLeaser{err: errors.New("decrypt failed"), failWithoutCalling: true}
	gen := &fakeGeneration{generations: []int64{1}}
	applier := &fakeApplier{applyResult: true}

	svc := NewDiscoveryService(adapter, leaser, gen, applier, fixedNow)
	result, err := svc.Run(context.Background(), RunParams{AccountID: "acct1", ProviderID: "prov1", CredentialID: "cred1", RunID: "run1"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Outcome != OutcomeFailed || result.ReasonCode != ReasonCredentialUnavailable {
		t.Fatalf("result = %+v, want Failed/%s", result, ReasonCredentialUnavailable)
	}
	if adapter.calls != 0 {
		t.Fatalf("adapter was called %d times, want 0 (fn must never run on a lease failure)", adapter.calls)
	}
	if len(applier.applyCalls) != 0 {
		t.Fatalf("Apply was called, want 0 calls")
	}
}

// TestDiscoveryService_ValidationRejectsInvalidModel_Fails and its table
// cover 04 §1 step 4's normalize/validate rules. MUTATION: removing any one
// of these checks turns the corresponding case RED (Apply gets called with
// bad data instead of MarkFailed).
func TestDiscoveryService_ValidationRejectsInvalidModel_Fails(t *testing.T) {
	cases := []struct {
		name  string
		model providers.DiscoveredModel
	}{
		{"empty provider_model_id", providers.DiscoveredModel{ProviderModelID: ""}},
		{"control char in provider_model_id", providers.DiscoveredModel{ProviderModelID: "gpt-\x00-x"}},
		{"control char in display name", providers.DiscoveredModel{ProviderModelID: "gpt-x", DisplayName: "GPT\x07X"}},
		{"invalid capability string", providers.DiscoveredModel{ProviderModelID: "gpt-x", Capabilities: []string{"vis\x01ion"}}},
		{"zero context length", providers.DiscoveredModel{ProviderModelID: "gpt-x", ContextLength: intPtr(0)}},
		{"negative context length", providers.DiscoveredModel{ProviderModelID: "gpt-x", ContextLength: intPtr(-1)}},
		{"zero max_input_tokens", providers.DiscoveredModel{ProviderModelID: "gpt-x", MaxInputTokens: intPtr(0)}},
		{"negative max_output_tokens", providers.DiscoveredModel{ProviderModelID: "gpt-x", MaxOutputTokens: intPtr(-5)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter := &fakeAdapter{models: []providers.DiscoveredModel{tc.model}}
			leaser := &fakeLeaser{}
			gen := &fakeGeneration{generations: []int64{1}}
			applier := &fakeApplier{applyResult: true}

			svc := NewDiscoveryService(adapter, leaser, gen, applier, fixedNow)
			result, err := svc.Run(context.Background(), RunParams{AccountID: "acct1", ProviderID: "prov1", CredentialID: "cred1", RunID: "run1"})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.Outcome != OutcomeFailed || result.ReasonCode != ReasonInvalidModel {
				t.Fatalf("result = %+v, want Failed/%s", result, ReasonInvalidModel)
			}
			if len(applier.applyCalls) != 0 {
				t.Fatalf("Apply was called, want 0 calls for an invalid model")
			}
		})
	}
}

// TestDiscoveryService_CountCap_Fails proves a response above
// MaxDiscoveredModels is treated as truncated/garbled — a whole-run failure
// — rather than silently truncated. MUTATION: removing the cap check (or
// truncating instead of failing) turns this RED.
func TestDiscoveryService_CountCap_Fails(t *testing.T) {
	models := make([]providers.DiscoveredModel, MaxDiscoveredModels+1)
	for i := range models {
		models[i] = providers.DiscoveredModel{ProviderModelID: "m"}
	}
	adapter := &fakeAdapter{models: models}
	leaser := &fakeLeaser{}
	gen := &fakeGeneration{generations: []int64{1}}
	applier := &fakeApplier{applyResult: true}

	svc := NewDiscoveryService(adapter, leaser, gen, applier, fixedNow)
	result, err := svc.Run(context.Background(), RunParams{AccountID: "acct1", ProviderID: "prov1", CredentialID: "cred1", RunID: "run1"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Outcome != OutcomeFailed || result.ReasonCode != ReasonTooManyModels {
		t.Fatalf("result = %+v, want Failed/%s", result, ReasonTooManyModels)
	}
	if len(applier.applyCalls) != 0 {
		t.Fatalf("Apply was called, want 0 calls when the count exceeds the cap")
	}
}

// TestDiscoveryService_GenerationGuard_Superseded proves the applier's
// applied=false outcome (a newer generation already won) is surfaced as
// OutcomeSuperseded, not an error and not a retry. MUTATION: treating
// applied=false as an error, or as Applied, turns this RED.
func TestDiscoveryService_GenerationGuard_Superseded(t *testing.T) {
	adapter := &fakeAdapter{models: []providers.DiscoveredModel{{ProviderModelID: "m1"}}}
	leaser := &fakeLeaser{}
	gen := &fakeGeneration{generations: []int64{1}}
	applier := &fakeApplier{applyResult: false}

	svc := NewDiscoveryService(adapter, leaser, gen, applier, fixedNow)
	result, err := svc.Run(context.Background(), RunParams{AccountID: "acct1", ProviderID: "prov1", CredentialID: "cred1", RunID: "run1"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Outcome != OutcomeSuperseded {
		t.Fatalf("Outcome = %q, want superseded", result.Outcome)
	}
	if result.ModelCount != 0 {
		t.Fatalf("ModelCount = %d, want 0 for a superseded run", result.ModelCount)
	}
}

func TestDiscoveryService_BeginRunError_Propagates(t *testing.T) {
	adapter := &fakeAdapter{}
	leaser := &fakeLeaser{}
	gen := &fakeGeneration{err: errors.New("db down")}
	applier := &fakeApplier{}

	svc := NewDiscoveryService(adapter, leaser, gen, applier, fixedNow)
	_, err := svc.Run(context.Background(), RunParams{AccountID: "acct1", ProviderID: "prov1", CredentialID: "cred1", RunID: "run1"})
	if err == nil {
		t.Fatalf("Run() error = nil, want a propagated infrastructure error")
	}
}

func TestDiscoveryService_ApplyError_Propagates(t *testing.T) {
	adapter := &fakeAdapter{models: []providers.DiscoveredModel{{ProviderModelID: "m1"}}}
	leaser := &fakeLeaser{}
	gen := &fakeGeneration{generations: []int64{1}}
	applier := &fakeApplier{applyErr: errors.New("db down")}

	svc := NewDiscoveryService(adapter, leaser, gen, applier, fixedNow)
	_, err := svc.Run(context.Background(), RunParams{AccountID: "acct1", ProviderID: "prov1", CredentialID: "cred1", RunID: "run1"})
	if err == nil {
		t.Fatalf("Run() error = nil, want a propagated infrastructure error")
	}
}

func TestDiscoveryService_EmptyRunParams_RejectedBeforeAnyPort(t *testing.T) {
	adapter := &fakeAdapter{}
	leaser := &fakeLeaser{}
	gen := &fakeGeneration{}
	applier := &fakeApplier{}

	svc := NewDiscoveryService(adapter, leaser, gen, applier, fixedNow)
	cases := []RunParams{
		{ProviderID: "p", CredentialID: "c", RunID: "r"},
		{AccountID: "a", CredentialID: "c", RunID: "r"},
		{AccountID: "a", ProviderID: "p", RunID: "r"},
		{AccountID: "a", ProviderID: "p", CredentialID: "c"},
	}
	for _, p := range cases {
		if _, err := svc.Run(context.Background(), p); !errors.Is(err, ErrEmptyRunParams) {
			t.Fatalf("Run(%+v) error = %v, want ErrEmptyRunParams", p, err)
		}
	}
	if len(gen.calls) != 0 {
		t.Fatalf("BeginRun was called %d times, want 0 for empty params", len(gen.calls))
	}
}

// TestCanonicalKey_ProviderScoping proves two providers exposing the same
// provider_model_id produce two distinct canonical keys — no cross-
// provider conflation (02 §3 / 04 §3).
func TestCanonicalKey_ProviderScoping(t *testing.T) {
	dm := providers.DiscoveredModel{ProviderModelID: "gpt-4"}
	a, err := normalizeDiscoveredModel("provider-a", dm)
	if err != nil {
		t.Fatalf("normalizeDiscoveredModel(a): %v", err)
	}
	b, err := normalizeDiscoveredModel("provider-b", dm)
	if err != nil {
		t.Fatalf("normalizeDiscoveredModel(b): %v", err)
	}
	if a.CanonicalKey == b.CanonicalKey {
		t.Fatalf("same provider_model_id under two providers produced the same canonical key %q, want distinct keys", a.CanonicalKey)
	}
}

// TestSanitizeMap_RedactsSecretShapedEvidence is the sanitize canary:
// planting credential-shaped fragments in Pricing/Evidence must never
// reach the snapshot handed to the applier port. MUTATION: removing the
// sanitizeMap call (or any of its branches) in normalizeDiscoveredModel
// turns this RED.
func TestSanitizeMap_RedactsSecretShapedEvidence(t *testing.T) {
	dm := providers.DiscoveredModel{
		ProviderModelID: "gpt-x",
		Pricing: map[string]any{
			"api_key":      "sk-live-should-never-appear",
			"input_per_1k": 0.01,
		},
		Evidence: map[string]any{
			"raw_snippet": "Authorization: Bearer sk-live-should-never-appear-either",
			"nested": map[string]any{
				"client_secret": "another-secret-should-never-appear",
			},
			"list": []any{"token=leaked-should-be-redacted", "harmless"},
		},
	}

	sm, err := normalizeDiscoveredModel("prov1", dm)
	if err != nil {
		t.Fatalf("normalizeDiscoveredModel: %v", err)
	}

	if sm.Pricing["api_key"] != "[REDACTED]" {
		t.Fatalf("Pricing[api_key] = %v, want [REDACTED]", sm.Pricing["api_key"])
	}
	if sm.Pricing["input_per_1k"] != 0.01 {
		t.Fatalf("Pricing[input_per_1k] = %v, want untouched 0.01", sm.Pricing["input_per_1k"])
	}
	if got := sm.Evidence["raw_snippet"].(string); containsSecret(got, "sk-live-should-never-appear-either") {
		t.Fatalf("Evidence[raw_snippet] = %q, still contains the raw bearer token", got)
	}
	nested, ok := sm.Evidence["nested"].(map[string]any)
	if !ok || nested["client_secret"] != "[REDACTED]" {
		t.Fatalf("Evidence[nested][client_secret] = %v, want [REDACTED]", nested)
	}
	list, ok := sm.Evidence["list"].([]any)
	if !ok || containsSecret(list[0].(string), "leaked-should-be-redacted") {
		t.Fatalf("Evidence[list][0] = %v, still contains the raw token value", list)
	}
	if list[1] != "harmless" {
		t.Fatalf("Evidence[list][1] = %v, want untouched %q", list[1], "harmless")
	}
}

func containsSecret(s, secret string) bool {
	for i := 0; i+len(secret) <= len(s); i++ {
		if s[i:i+len(secret)] == secret {
			return true
		}
	}
	return false
}

package httpapi

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/routing"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// fakeWindowLister provides quota windows without seeding quota_windows and
// counts how many times it is called, so a test can prove the snapshot issues
// exactly ONE batched window query for the whole request.
type fakeWindowLister struct {
	windows map[string][]quota.Window
	calls   int
}

func (f *fakeWindowLister) ListByAccounts(_ context.Context, _ []string) (map[string][]quota.Window, error) {
	f.calls++
	return f.windows, nil
}

// seedOffering seeds one fully-formed offering on a healthy, free, credentialed
// account: model, offering, chat offering_operation (+ certification when
// certified), account, funding evidence, and an active credential. nativeCtx
// and ctxLen may be nil to exercise the unknown-context fail-closed path.
func seedOffering(t *testing.T, db *storage.DB, acct, provModelID, modelID string, certified bool, nativeCtx, ctxLen *int) {
	t.Helper()
	conn := db.Conn()
	exec := func(q string, args ...any) {
		if _, err := conn.Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT OR IGNORE INTO models (id, canonical_key_sha256, display_name, native_context_tokens, native_modalities_json, quality_rating, created_at, updated_at) VALUES (?, ?, ?, ?, NULL, 0.9, 0, 0)`,
		modelID, modelID, modelID, nativeCtx)
	exec(`INSERT INTO accounts (id, provider_id, external_id, auth_type, connection_state, health_state, created_at, updated_at) VALUES (?, ?, ?, 'api_key', 'connected', 'healthy', 0, 0)`,
		acct, string(providers.OpenCodeZenID), acct)
	exec(`INSERT INTO account_funding_evidence (id, account_id, funding, source, locked, confidence, reason, observed_at, superseded_at) VALUES (?, ?, 'free', 'provider_policy', 0, 1, NULL, 0, NULL)`,
		"fe-"+acct, acct)
	exec(`INSERT INTO account_credentials (id, account_id, provider_id, kind, state, fingerprint_sha256, key_id, nonce, ciphertext, created_at, updated_at) VALUES (?, ?, ?, 'api_key', 'active', ?, 'k', 'n', 'c', 0, 0)`,
		"cred-"+acct, acct, string(providers.OpenCodeZenID), "fp-"+acct)
	exec(`INSERT INTO account_model_offerings (account_id, provider_id, provider_model_id, model_id, availability, context_length, max_input_tokens, max_output_tokens, capabilities_json, pricing_json, first_seen_at, last_seen_at) VALUES (?, ?, ?, ?, 'available', ?, NULL, NULL, NULL, NULL, 0, 0)`,
		acct, string(providers.OpenCodeZenID), provModelID, modelID, ctxLen)
	ooID := "oo-" + acct + "-" + provModelID
	exec(`INSERT INTO offering_operations (id, account_id, provider_id, provider_model_id, operation, created_at, updated_at) VALUES (?, ?, ?, ?, 'chat', 0, 0)`,
		ooID, acct, string(providers.OpenCodeZenID), provModelID)
	if certified {
		exec(`INSERT INTO certifications (offering_operation_id, status, capability_truth, version, certified_at, evidence_ref, created_at, updated_at) VALUES (?, 'certified', 'supported', 1, 0, '', 0, 0)`, ooID)
	}
}

func newSnapshotBuilderForTest(t *testing.T, db *storage.DB, windows quotaWindowLister, maxCandidates int) *SnapshotBuilder {
	t.Helper()
	if err := storage.SeedProviders(context.Background(), db, providers.BuiltinCatalog(), time.Unix(0, 0)); err != nil {
		t.Fatalf("seed providers: %v", err)
	}
	return NewSnapshotBuilder(
		storage.NewCatalogRepo(db),
		storage.NewAccountRepo(db),
		storage.NewFundingEvidenceRepo(db),
		storage.NewAccountCredentialRepo(db),
		windows,
		newInflightCounter(),
		maxCandidates,
	)
}

func intp2(v int) *int { return &v }

func poolProviderModelIDs(pool routing.RoutePool) []string {
	var out []string
	for _, g := range pool.Groups {
		out = append(out, g.ProviderModelID)
	}
	return out
}

// TestSnapshot_RealFactsDriveCandidacy proves the snapshot is built from the
// REAL catalog certification facts, not httpapi's always-nil Routable
// projection: a certified+supported offering on a healthy funded credentialed
// account appears as a candidate; an uncertified one is excluded.
//
// Mutation W3-M1: build candidates from the projection's Routable (always
// false) → the pool is empty → this test RED.
func TestSnapshot_RealFactsDriveCandidacy(t *testing.T) {
	db := testControlDB(t)
	windows := &fakeWindowLister{windows: map[string][]quota.Window{}}
	b := newSnapshotBuilderForTest(t, db, windows, 0)

	seedOffering(t, db, "acct-ok", "prov/model-a", "model-a", true, intp2(200000), intp2(128000))
	seedOffering(t, db, "acct-uncert", "prov/model-b", "model-b", false, intp2(200000), intp2(128000))

	res, err := b.Build(context.Background(), routing.TierPro, routing.Requirements{TextModality: true, ContextTokens: 100})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ids := poolProviderModelIDs(res.Pool)
	if len(ids) != 1 || ids[0] != "prov/model-a" {
		t.Fatalf("pool = %v, want exactly [prov/model-a] (only the certified offering is routable)", ids)
	}
}

// TestSnapshot_UnknownContextFailsClosed proves an offering whose context
// ceiling is unknown (both native and provider caps NULL) is excluded — never
// admitted with a fabricated 0 or default ceiling.
//
// Mutation W3-M2: default an unknown ceiling to 0/constant instead of nil → the
// offering is admitted → this test RED (pool non-empty).
func TestSnapshot_UnknownContextFailsClosed(t *testing.T) {
	db := testControlDB(t)
	b := newSnapshotBuilderForTest(t, db, &fakeWindowLister{windows: map[string][]quota.Window{}}, 0)

	seedOffering(t, db, "acct-noctx", "prov/model-x", "model-x", true, nil, nil)

	res, err := b.Build(context.Background(), routing.TierPro, routing.Requirements{TextModality: true, ContextTokens: 100})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(res.Pool.Groups) != 0 {
		t.Fatalf("pool = %v, want empty (unknown context must fail closed)", poolProviderModelIDs(res.Pool))
	}
	if res.ExclusionReasons[routing.ReasonContextUnverified] < 1 {
		t.Fatalf("expected %q exclusion; got %v", routing.ReasonContextUnverified, res.ExclusionReasons)
	}
}

// TestSnapshot_OneBatchedWindowQuery proves quota windows are read in exactly
// ONE batched call for the whole request, never per candidate.
//
// Mutation W3-M3: query windows per candidate → the fake lister is called more
// than once → this test RED.
func TestSnapshot_OneBatchedWindowQuery(t *testing.T) {
	db := testControlDB(t)
	windows := &fakeWindowLister{windows: map[string][]quota.Window{}}
	b := newSnapshotBuilderForTest(t, db, windows, 0)

	for i := 0; i < 3; i++ {
		acct := fmt.Sprintf("acct-%d", i)
		seedOffering(t, db, acct, "prov/model-"+acct, "model-"+acct, true, intp2(200000), intp2(128000))
	}

	if _, err := b.Build(context.Background(), routing.TierPro, routing.Requirements{TextModality: true, ContextTokens: 100}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if windows.calls != 1 {
		t.Fatalf("quota-window batched query called %d times, want exactly 1", windows.calls)
	}
}

// TestSnapshot_CapReported proves that when the fleet exceeds the cap the result
// is capped AND the cap is reported (never a silent truncation).
//
// Mutation W3-M7: truncate at the cap without reporting → Capped stays false →
// this test RED.
func TestSnapshot_CapReported(t *testing.T) {
	db := testControlDB(t)
	b := newSnapshotBuilderForTest(t, db, &fakeWindowLister{windows: map[string][]quota.Window{}}, 2)

	for i := 0; i < 5; i++ {
		acct := fmt.Sprintf("acct-%d", i)
		seedOffering(t, db, acct, "prov/model-"+acct, "model-"+acct, true, intp2(200000), intp2(128000))
	}

	res, err := b.Build(context.Background(), routing.TierPro, routing.Requirements{TextModality: true, ContextTokens: 100})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !res.Capped {
		t.Fatalf("Capped = false, want true (5 offerings, cap 2 — the cap must be reported)")
	}
	if res.Summary.TotalCandidates != 2 {
		t.Fatalf("TotalCandidates = %d, want 2 (capped)", res.Summary.TotalCandidates)
	}
}

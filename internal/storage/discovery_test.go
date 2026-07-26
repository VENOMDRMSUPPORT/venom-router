package storage

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

// sequentialTestIDs returns an id generator that yields "test-id-1",
// "test-id-2", ... in call order — deterministic and readable in test
// failures, unlike a random hex string.
func sequentialTestIDs() func() string {
	n := 0
	return func() string {
		n++
		return "test-id-" + itoa(n)
	}
}

func intPtr(v int) *int { return &v }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func TestDiscoveryRepo_BeginRun_AllocatesMonotonicGenerations(t *testing.T) {
	db := migratedCatalogDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")
	repo := NewDiscoveryRepo(db, sequentialTestIDs())
	ctx := context.Background()
	now := time.Unix(1000, 0)

	g1, err := repo.BeginRun(ctx, "acct1", "run1", now)
	if err != nil || g1 != 1 {
		t.Fatalf("BeginRun #1 = (%d, %v), want (1, nil)", g1, err)
	}
	g2, err := repo.BeginRun(ctx, "acct1", "run2", now)
	if err != nil || g2 != 2 {
		t.Fatalf("BeginRun #2 = (%d, %v), want (2, nil)", g2, err)
	}
	g3, err := repo.BeginRun(ctx, "acct1", "run3", now)
	if err != nil || g3 != 3 {
		t.Fatalf("BeginRun #3 = (%d, %v), want (3, nil)", g3, err)
	}

	assertDiscoveryRunStatus(t, db, "run1", "running")
	assertDiscoveryRunStatus(t, db, "run2", "running")
	assertDiscoveryRunStatus(t, db, "run3", "running")
}

// TestDiscoveryRepo_Apply_NewestGenerationApplies_OlderSuperseded is the
// core generation-guard proof (04 §1 step 5): the run holding the higher
// generation applies even though it finishes first; the older run
// finishing later is marked superseded and its snapshot never lands.
// MUTATION: removing the `snapshot.Generation < maxGen.Int64` guard in
// DiscoveryRepo.Apply (always applying) turns this RED — the older run's
// model would overwrite the newer run's.
func TestDiscoveryRepo_Apply_NewestGenerationApplies_OlderSuperseded(t *testing.T) {
	db := migratedCatalogDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")
	repo := NewDiscoveryRepo(db, sequentialTestIDs())
	ctx := context.Background()
	now := time.Unix(1000, 0)

	genOlder, err := repo.BeginRun(ctx, "acct1", "run-older", now)
	if err != nil {
		t.Fatalf("BeginRun(older): %v", err)
	}
	genNewer, err := repo.BeginRun(ctx, "acct1", "run-newer", now)
	if err != nil {
		t.Fatalf("BeginRun(newer): %v", err)
	}

	newerSnapshot := intelligence.DiscoverySnapshot{
		AccountID: "acct1", ProviderID: "prov1", Generation: genNewer,
		Models: []intelligence.DiscoverySnapshotModel{{CanonicalKey: "key-b", ProviderModelID: "model-b"}},
	}
	applied, err := repo.Apply(ctx, "run-newer", newerSnapshot, now)
	if err != nil || !applied {
		t.Fatalf("Apply(newer) = (%v, %v), want (true, nil) — the newer run must apply even though it finishes first", applied, err)
	}

	olderSnapshot := intelligence.DiscoverySnapshot{
		AccountID: "acct1", ProviderID: "prov1", Generation: genOlder,
		Models: []intelligence.DiscoverySnapshotModel{{CanonicalKey: "key-a", ProviderModelID: "model-a"}},
	}
	applied, err = repo.Apply(ctx, "run-older", olderSnapshot, now)
	if err != nil || applied {
		t.Fatalf("Apply(older) = (%v, %v), want (false, nil) — a slower older run must be superseded, not applied", applied, err)
	}

	assertDiscoveryRunStatus(t, db, "run-newer", "applied")
	assertDiscoveryRunStatus(t, db, "run-older", "superseded")

	assertOfferingAvailability(t, db, "acct1", "model-b", "available")
	assertOfferingDoesNotExist(t, db, "acct1", "model-a")
}

// TestDiscoveryRepo_Apply_ExplicitEmptyWithdrawsPriorOfferings proves an
// explicit-empty (Withdraw=true) snapshot marks every existing offering for
// the account withdrawn, and the run itself is 'applied' (not 'failed') —
// 04 §1's "explicit empty list is authoritative". MUTATION: making the
// withdraw branch a no-op (or routing it through MarkFailed) turns this RED.
func TestDiscoveryRepo_Apply_ExplicitEmptyWithdrawsPriorOfferings(t *testing.T) {
	db := migratedCatalogDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")
	repo := NewDiscoveryRepo(db, sequentialTestIDs())
	ctx := context.Background()
	now := time.Unix(1000, 0)

	gen1, err := repo.BeginRun(ctx, "acct1", "run1", now)
	if err != nil {
		t.Fatalf("BeginRun: %v", err)
	}
	seed := intelligence.DiscoverySnapshot{
		AccountID: "acct1", ProviderID: "prov1", Generation: gen1,
		Models: []intelligence.DiscoverySnapshotModel{{CanonicalKey: "key-a", ProviderModelID: "model-a"}},
	}
	if applied, err := repo.Apply(ctx, "run1", seed, now); err != nil || !applied {
		t.Fatalf("seed Apply = (%v, %v), want (true, nil)", applied, err)
	}
	assertOfferingAvailability(t, db, "acct1", "model-a", "available")

	gen2, err := repo.BeginRun(ctx, "acct1", "run2", now)
	if err != nil {
		t.Fatalf("BeginRun: %v", err)
	}
	withdraw := intelligence.DiscoverySnapshot{AccountID: "acct1", ProviderID: "prov1", Generation: gen2, Withdraw: true}
	applied, err := repo.Apply(ctx, "run2", withdraw, now)
	if err != nil || !applied {
		t.Fatalf("withdraw Apply = (%v, %v), want (true, nil) — an explicit empty list is applied, not failed", applied, err)
	}

	assertDiscoveryRunStatus(t, db, "run2", "applied")
	assertOfferingAvailability(t, db, "acct1", "model-a", "withdrawn")
}

// TestDiscoveryRepo_Apply_MissingModelInNonEmptySnapshotIsWithdrawn proves
// a successful non-empty snapshot is authoritative for the account's
// complete model list: a model reported by a prior run but absent from
// this one is withdrawn, not left stale as 'available' forever.
func TestDiscoveryRepo_Apply_MissingModelInNonEmptySnapshotIsWithdrawn(t *testing.T) {
	db := migratedCatalogDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")
	repo := NewDiscoveryRepo(db, sequentialTestIDs())
	ctx := context.Background()
	now := time.Unix(1000, 0)

	gen1, _ := repo.BeginRun(ctx, "acct1", "run1", now)
	seed := intelligence.DiscoverySnapshot{
		AccountID: "acct1", ProviderID: "prov1", Generation: gen1,
		Models: []intelligence.DiscoverySnapshotModel{
			{CanonicalKey: "key-a", ProviderModelID: "model-a"},
			{CanonicalKey: "key-b", ProviderModelID: "model-b"},
		},
	}
	if applied, err := repo.Apply(ctx, "run1", seed, now); err != nil || !applied {
		t.Fatalf("seed Apply = (%v, %v), want (true, nil)", applied, err)
	}

	gen2, _ := repo.BeginRun(ctx, "acct1", "run2", now)
	next := intelligence.DiscoverySnapshot{
		AccountID: "acct1", ProviderID: "prov1", Generation: gen2,
		Models: []intelligence.DiscoverySnapshotModel{
			{CanonicalKey: "key-b", ProviderModelID: "model-b"},
		},
	}
	if applied, err := repo.Apply(ctx, "run2", next, now); err != nil || !applied {
		t.Fatalf("next Apply = (%v, %v), want (true, nil)", applied, err)
	}

	assertOfferingAvailability(t, db, "acct1", "model-a", "withdrawn")
	assertOfferingAvailability(t, db, "acct1", "model-b", "available")
}

// TestDiscoveryRepo_MarkFailed_LeavesOfferingsUntouched proves MarkFailed
// never mutates any offering — "keep the previous snapshot intact on
// failure" holds by construction (MarkFailed touches only discovery_runs).
// MUTATION: having MarkFailed also withdraw/delete offerings turns this RED.
func TestDiscoveryRepo_MarkFailed_LeavesOfferingsUntouched(t *testing.T) {
	db := migratedCatalogDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")
	repo := NewDiscoveryRepo(db, sequentialTestIDs())
	ctx := context.Background()
	now := time.Unix(1000, 0)

	gen1, _ := repo.BeginRun(ctx, "acct1", "run1", now)
	seed := intelligence.DiscoverySnapshot{
		AccountID: "acct1", ProviderID: "prov1", Generation: gen1,
		Models: []intelligence.DiscoverySnapshotModel{{CanonicalKey: "key-a", ProviderModelID: "model-a"}},
	}
	if applied, err := repo.Apply(ctx, "run1", seed, now); err != nil || !applied {
		t.Fatalf("seed Apply = (%v, %v), want (true, nil)", applied, err)
	}
	assertRowCount(t, db, "account_model_offerings", 1)

	if _, err := repo.BeginRun(ctx, "acct1", "run2", now); err != nil {
		t.Fatalf("BeginRun: %v", err)
	}
	if err := repo.MarkFailed(ctx, "run2", "discovery_failed", now); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	assertDiscoveryRunStatus(t, db, "run2", "failed")
	assertRowCount(t, db, "account_model_offerings", 1)
	assertOfferingAvailability(t, db, "acct1", "model-a", "available")

	var reasonCode string
	if err := db.Conn().QueryRow(`SELECT reason_code FROM discovery_runs WHERE id = ?`, "run2").Scan(&reasonCode); err != nil {
		t.Fatalf("query reason_code: %v", err)
	}
	if reasonCode != "discovery_failed" {
		t.Fatalf("reason_code = %q, want discovery_failed", reasonCode)
	}
}

// TestDiscoveryRepo_Apply_FullRowSet proves one applied model produces the
// complete expected row set: a models identity row, a provider_model_alias,
// the account_model_offerings row (with sanitized evidence landing in
// lifecycle_json), an offering_operations row per recognized operation, and
// a 'discovered'/'unknown' certifications baseline for each.
func TestDiscoveryRepo_Apply_FullRowSet(t *testing.T) {
	db := migratedCatalogDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")
	repo := NewDiscoveryRepo(db, sequentialTestIDs())
	ctx := context.Background()
	now := time.Unix(1000, 0)

	canonicalKey, err := models.CanonicalKey("prov1", "model-a")
	if err != nil {
		t.Fatalf("CanonicalKey: %v", err)
	}

	gen1, _ := repo.BeginRun(ctx, "acct1", "run1", now)
	snapshot := intelligence.DiscoverySnapshot{
		AccountID: "acct1", ProviderID: "prov1", Generation: gen1,
		Models: []intelligence.DiscoverySnapshotModel{{
			CanonicalKey:    canonicalKey,
			ProviderModelID: "model-a",
			DisplayName:     "Model A",
			ContextLength:   intPtr(128000),
			Capabilities:    []string{"chat", "vision"},
			Operations:      []models.Operation{models.OperationChat, models.OperationVision},
			Pricing:         map[string]any{"input_per_1k": 0.01},
			Evidence:        map[string]any{"note": "already-sanitized"},
		}},
	}
	if applied, err := repo.Apply(ctx, "run1", snapshot, now); err != nil || !applied {
		t.Fatalf("Apply = (%v, %v), want (true, nil)", applied, err)
	}

	var modelID string
	if err := db.Conn().QueryRow(`SELECT id FROM models WHERE canonical_key_sha256 = ?`, canonicalKey).Scan(&modelID); err != nil {
		t.Fatalf("query models: %v", err)
	}

	var aliasModelID string
	if err := db.Conn().QueryRow(`SELECT model_id FROM provider_model_aliases WHERE provider_id = ? AND provider_model_id = ?`, "prov1", "model-a").Scan(&aliasModelID); err != nil {
		t.Fatalf("query provider_model_aliases: %v", err)
	}
	if aliasModelID != modelID {
		t.Fatalf("alias model_id = %q, want %q", aliasModelID, modelID)
	}

	var (
		contextLength                  sql.NullInt64
		capabilitiesJSON, pricingJSON  sql.NullString
		lifecycleJSON, offeringModelID string
	)
	row := db.Conn().QueryRow(
		`SELECT context_length, capabilities_json, pricing_json, lifecycle_json, model_id FROM account_model_offerings WHERE account_id = ? AND provider_model_id = ?`,
		"acct1", "model-a",
	)
	if err := row.Scan(&contextLength, &capabilitiesJSON, &pricingJSON, &lifecycleJSON, &offeringModelID); err != nil {
		t.Fatalf("query account_model_offerings: %v", err)
	}
	if !contextLength.Valid || contextLength.Int64 != 128000 {
		t.Fatalf("context_length = %+v, want 128000", contextLength)
	}
	if capabilitiesJSON.String != `["chat","vision"]` {
		t.Fatalf("capabilities_json = %q, want [\"chat\",\"vision\"]", capabilitiesJSON.String)
	}
	if pricingJSON.String != `{"input_per_1k":0.01}` {
		t.Fatalf("pricing_json = %q, want {\"input_per_1k\":0.01}", pricingJSON.String)
	}
	if lifecycleJSON != `{"note":"already-sanitized"}` {
		t.Fatalf("lifecycle_json = %q, want the sanitized evidence map", lifecycleJSON)
	}
	if offeringModelID != modelID {
		t.Fatalf("offering model_id = %q, want %q", offeringModelID, modelID)
	}

	for _, op := range []string{"chat", "vision"} {
		var opID, status, truth string
		if err := db.Conn().QueryRow(
			`SELECT id FROM offering_operations WHERE account_id = ? AND provider_model_id = ? AND operation = ?`,
			"acct1", "model-a", op,
		).Scan(&opID); err != nil {
			t.Fatalf("query offering_operations(%s): %v", op, err)
		}
		if err := db.Conn().QueryRow(
			`SELECT status, capability_truth FROM certifications WHERE offering_operation_id = ?`, opID,
		).Scan(&status, &truth); err != nil {
			t.Fatalf("query certifications(%s): %v", op, err)
		}
		if status != "discovered" || truth != "unknown" {
			t.Fatalf("certification(%s) = (%s, %s), want (discovered, unknown)", op, status, truth)
		}
	}
}

// TestDiscoveryRepo_Apply_ExistingOfferingOperationCertificationNeverReset
// proves re-discovering an offering-operation whose certification has
// already progressed (e.g. to certified) never resets it back to
// discovered — DISC-002 stamps a baseline once and never drives the
// certification state machine afterward. MUTATION: removing the
// existing-row check in ensureOfferingOperation (always re-inserting the
// certification) turns this RED.
func TestDiscoveryRepo_Apply_ExistingOfferingOperationCertificationNeverReset(t *testing.T) {
	db := migratedCatalogDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")
	repo := NewDiscoveryRepo(db, sequentialTestIDs())
	ctx := context.Background()
	now := time.Unix(1000, 0)

	canonicalKey, _ := models.CanonicalKey("prov1", "model-a")
	gen1, _ := repo.BeginRun(ctx, "acct1", "run1", now)
	snapshot := intelligence.DiscoverySnapshot{
		AccountID: "acct1", ProviderID: "prov1", Generation: gen1,
		Models: []intelligence.DiscoverySnapshotModel{{
			CanonicalKey: canonicalKey, ProviderModelID: "model-a",
			Operations: []models.Operation{models.OperationChat},
		}},
	}
	if applied, err := repo.Apply(ctx, "run1", snapshot, now); err != nil || !applied {
		t.Fatalf("seed Apply = (%v, %v), want (true, nil)", applied, err)
	}

	var opID string
	if err := db.Conn().QueryRow(
		`SELECT id FROM offering_operations WHERE account_id = ? AND provider_model_id = ? AND operation = ?`,
		"acct1", "model-a", "chat",
	).Scan(&opID); err != nil {
		t.Fatalf("query offering_operations: %v", err)
	}
	if _, err := db.Conn().Exec(
		`UPDATE certifications SET status = 'certified', capability_truth = 'supported', version = 2 WHERE offering_operation_id = ?`,
		opID,
	); err != nil {
		t.Fatalf("simulate prior certification progress: %v", err)
	}

	gen2, _ := repo.BeginRun(ctx, "acct1", "run2", now)
	rediscovered := intelligence.DiscoverySnapshot{
		AccountID: "acct1", ProviderID: "prov1", Generation: gen2,
		Models: []intelligence.DiscoverySnapshotModel{{
			CanonicalKey: canonicalKey, ProviderModelID: "model-a",
			Operations: []models.Operation{models.OperationChat},
		}},
	}
	if applied, err := repo.Apply(ctx, "run2", rediscovered, now); err != nil || !applied {
		t.Fatalf("rediscover Apply = (%v, %v), want (true, nil)", applied, err)
	}

	var status, truth string
	var version int
	if err := db.Conn().QueryRow(
		`SELECT status, capability_truth, version FROM certifications WHERE offering_operation_id = ?`, opID,
	).Scan(&status, &truth, &version); err != nil {
		t.Fatalf("query certifications: %v", err)
	}
	if status != "certified" || truth != "supported" || version != 2 {
		t.Fatalf("certification = (%s, %s, v%d), want (certified, supported, v2) — re-discovery must never reset progress", status, truth, version)
	}
}

func assertDiscoveryRunStatus(t *testing.T, db *DB, runID, want string) {
	t.Helper()
	var got string
	if err := db.Conn().QueryRow(`SELECT status FROM discovery_runs WHERE id = ?`, runID).Scan(&got); err != nil {
		t.Fatalf("query discovery_runs status for %q: %v", runID, err)
	}
	if got != want {
		t.Fatalf("discovery_runs(%q).status = %q, want %q", runID, got, want)
	}
}

func assertOfferingAvailability(t *testing.T, db *DB, accountID, providerModelID, want string) {
	t.Helper()
	var got string
	if err := db.Conn().QueryRow(
		`SELECT availability FROM account_model_offerings WHERE account_id = ? AND provider_model_id = ?`,
		accountID, providerModelID,
	).Scan(&got); err != nil {
		t.Fatalf("query offering availability (%q,%q): %v", accountID, providerModelID, err)
	}
	if got != want {
		t.Fatalf("offering(%q,%q).availability = %q, want %q", accountID, providerModelID, got, want)
	}
}

func assertOfferingDoesNotExist(t *testing.T, db *DB, accountID, providerModelID string) {
	t.Helper()
	var count int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM account_model_offerings WHERE account_id = ? AND provider_model_id = ?`,
		accountID, providerModelID,
	).Scan(&count); err != nil {
		t.Fatalf("count offerings (%q,%q): %v", accountID, providerModelID, err)
	}
	if count != 0 {
		t.Fatalf("offering (%q,%q) exists (%d rows), want 0 — a superseded run must never write its snapshot", accountID, providerModelID, count)
	}
}

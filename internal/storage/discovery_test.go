package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
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
// a 'discovered'/'unknown' certifications baseline for each — the very
// first sighting of a brand-new offering-operation IS the baseline, not
// yet "recorded evidence" for something already on file (P3c-CERT-008's
// edge 1 fires on a SUBSEQUENT re-discovery instead; see
// TestDiscoveryApply_AdvancesDiscoveredToObserved).
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
		// observed, not discovered: the snapshot that produced this operation
		// is 04 §5 edge 1's "first concrete evidence recorded". Truth stays
		// unknown — only a probe verdict resolves that.
		if status != "observed" || truth != "unknown" {
			t.Fatalf("certification(%s) = (%s, %s), want (observed, unknown)", op, status, truth)
		}
	}
}

// TestDiscoveryRepo_Apply_CandidateOperationsCreateRowsButStayUndeclared
// proves Task 3's honesty rule end to end at the storage layer: a
// DiscoverySnapshotModel with one declared operation (chat) and two CANDIDATE
// operations (tools, structured_output — e.g. clinepass, whose wire has no
// capability metadata at all) produces THREE offering_operations rows — every
// declared and candidate operation must be probeable — but
// account_model_offerings.capabilities_json contains ONLY "chat". A candidate
// creating a row is what makes it probeable at all (no row, no probe target);
// a candidate NOT reaching capabilities_json is what stops
// ListNonChatOperationsToCertify from certifying it as if the provider had
// declared it. MUTATION: writing capabilities_json from the union (or from
// CandidateOperations at all) turns this false; writing offering_operations
// rows for Operations only (dropping the CandidateOperations loop) leaves
// only 1 row instead of 3.
func TestDiscoveryRepo_Apply_CandidateOperationsCreateRowsButStayUndeclared(t *testing.T) {
	db := migratedCatalogDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")
	repo := NewDiscoveryRepo(db, sequentialTestIDs())
	ctx := context.Background()
	now := time.Unix(1000, 0)

	canonicalKey, err := models.CanonicalKey("prov1", "clinepass-model")
	if err != nil {
		t.Fatalf("CanonicalKey: %v", err)
	}

	gen1, _ := repo.BeginRun(ctx, "acct1", "run1", now)
	snapshot := intelligence.DiscoverySnapshot{
		AccountID: "acct1", ProviderID: "prov1", Generation: gen1,
		Models: []intelligence.DiscoverySnapshotModel{{
			CanonicalKey:        canonicalKey,
			ProviderModelID:     "clinepass-model",
			DisplayName:         "ClinePass Model",
			Capabilities:        []string{"chat"},
			Operations:          []models.Operation{models.OperationChat},
			CandidateOperations: []models.Operation{models.OperationTools, models.OperationStructuredOutput},
		}},
	}
	if applied, err := repo.Apply(ctx, "run1", snapshot, now); err != nil || !applied {
		t.Fatalf("Apply = (%v, %v), want (true, nil)", applied, err)
	}

	var capabilitiesJSON sql.NullString
	if err := db.Conn().QueryRow(
		`SELECT capabilities_json FROM account_model_offerings WHERE account_id = ? AND provider_model_id = ?`,
		"acct1", "clinepass-model",
	).Scan(&capabilitiesJSON); err != nil {
		t.Fatalf("query account_model_offerings: %v", err)
	}
	if capabilitiesJSON.String != `["chat"]` {
		t.Fatalf("capabilities_json = %q, want [\"chat\"] (candidates must never be declared)", capabilitiesJSON.String)
	}

	for _, op := range []string{"chat", "tools", "structured_output"} {
		var opID string
		if err := db.Conn().QueryRow(
			`SELECT id FROM offering_operations WHERE account_id = ? AND provider_model_id = ? AND operation = ?`,
			"acct1", "clinepass-model", op,
		).Scan(&opID); err != nil {
			t.Fatalf("query offering_operations(%s): every declared+candidate op must have a probeable row: %v", op, err)
		}
	}

	var opCount int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM offering_operations WHERE account_id = ? AND provider_model_id = ?`,
		"acct1", "clinepass-model",
	).Scan(&opCount); err != nil {
		t.Fatalf("count offering_operations: %v", err)
	}
	if opCount != 3 {
		t.Fatalf("offering_operations count = %d, want exactly 3 (chat + tools + structured_output, no duplicates)", opCount)
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

// TestDiscoveryApply_AdvancesDiscoveredToObserved is P3c-CERT-008's
// direct pinning test: an offering-operation's very first sighting stays
// 'discovered' (it IS the baseline), but a SECOND snapshot recording
// concrete evidence for that SAME, already-known offering-operation
// advances its certification to 'observed' — 04 §5 edge 1, driven by
// discovery itself rather than left permanently un-driven.
func TestDiscoveryApply_AdvancesDiscoveredToObserved(t *testing.T) {
	db := migratedCatalogDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")
	repo := NewDiscoveryRepo(db, sequentialTestIDs())
	ctx := context.Background()
	now := time.Unix(1000, 0)

	snapshotFor := func(gen int64) intelligence.DiscoverySnapshot {
		return intelligence.DiscoverySnapshot{
			AccountID: "acct1", ProviderID: "prov1", Generation: gen,
			Models: []intelligence.DiscoverySnapshotModel{{
				CanonicalKey: "key-a", ProviderModelID: "model-a",
				Operations: []models.Operation{models.OperationChat},
			}},
		}
	}

	gen1, _ := repo.BeginRun(ctx, "acct1", "run1", now)
	if applied, err := repo.Apply(ctx, "run1", snapshotFor(gen1), now); err != nil || !applied {
		t.Fatalf("first Apply = (%v, %v), want (true, nil)", applied, err)
	}

	var opID, status string
	if err := db.Conn().QueryRow(
		`SELECT id FROM offering_operations WHERE account_id = ? AND provider_model_id = ? AND operation = ?`,
		"acct1", "model-a", "chat",
	).Scan(&opID); err != nil {
		t.Fatalf("query offering_operations: %v", err)
	}
	if err := db.Conn().QueryRow(`SELECT status FROM certifications WHERE offering_operation_id = ?`, opID).Scan(&status); err != nil {
		t.Fatalf("query certifications: %v", err)
	}
	// The FIRST snapshot already advances edge 1 — a freshly discovered
	// offering-operation must be probeable without the owner triggering
	// discovery a second time (nothing schedules discovery).
	if status != "observed" {
		t.Fatalf("certification status after the first snapshot = %q, want observed", status)
	}

	gen2, _ := repo.BeginRun(ctx, "acct1", "run2", now)
	if applied, err := repo.Apply(ctx, "run2", snapshotFor(gen2), now); err != nil || !applied {
		t.Fatalf("second Apply = (%v, %v), want (true, nil)", applied, err)
	}

	if err := db.Conn().QueryRow(`SELECT status FROM certifications WHERE offering_operation_id = ?`, opID).Scan(&status); err != nil {
		t.Fatalf("query certifications: %v", err)
	}
	if status != "observed" {
		t.Fatalf("certification status after re-discovery = %q, want observed", status)
	}
}

// TestDiscoveryApply_NeverResetsANonDiscoveredCertification proves
// recordEvidenceObserved's status='discovered' guard protects every
// state a probe can have reached: a re-discovery leaves probing/
// certified/suspended/expired byte-identical (state, truth, version).
// MUTATION 2.2/2.3: removing the guard (or narrowing it incorrectly)
// turns this RED.
func TestDiscoveryApply_NeverResetsANonDiscoveredCertification(t *testing.T) {
	for _, tc := range []struct {
		status string
		truth  string
	}{
		{"probing", "unknown"},
		{"certified", "supported"},
		{"suspended", "unknown"},
		{"expired", "supported"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			db := migratedCatalogDB(t)
			insertProvider(t, db, "prov1")
			insertAccount(t, db, "acct1", "prov1")
			repo := NewDiscoveryRepo(db, sequentialTestIDs())
			ctx := context.Background()
			now := time.Unix(1000, 0)

			gen1, _ := repo.BeginRun(ctx, "acct1", "run1", now)
			seed := intelligence.DiscoverySnapshot{
				AccountID: "acct1", ProviderID: "prov1", Generation: gen1,
				Models: []intelligence.DiscoverySnapshotModel{{
					CanonicalKey: "key-a", ProviderModelID: "model-a",
					Operations: []models.Operation{models.OperationChat},
				}},
			}
			if applied, err := repo.Apply(ctx, "run1", seed, now); err != nil || !applied {
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
				`UPDATE certifications SET status = ?, capability_truth = ?, version = 7 WHERE offering_operation_id = ?`,
				tc.status, tc.truth, opID,
			); err != nil {
				t.Fatalf("simulate probe progress: %v", err)
			}

			gen2, _ := repo.BeginRun(ctx, "acct1", "run2", now)
			rediscovered := intelligence.DiscoverySnapshot{
				AccountID: "acct1", ProviderID: "prov1", Generation: gen2,
				Models: []intelligence.DiscoverySnapshotModel{{
					CanonicalKey: "key-a", ProviderModelID: "model-a",
					Operations: []models.Operation{models.OperationChat},
				}},
			}
			if applied, err := repo.Apply(ctx, "run2", rediscovered, now); err != nil || !applied {
				t.Fatalf("rediscover Apply = (%v, %v), want (true, nil)", applied, err)
			}

			var gotStatus, gotTruth string
			var gotVersion int
			if err := db.Conn().QueryRow(
				`SELECT status, capability_truth, version FROM certifications WHERE offering_operation_id = ?`, opID,
			).Scan(&gotStatus, &gotTruth, &gotVersion); err != nil {
				t.Fatalf("query certifications: %v", err)
			}
			if gotStatus != tc.status || gotTruth != tc.truth || gotVersion != 7 {
				t.Fatalf("certification = (%s, %s, v%d), want (%s, %s, v7) unchanged", gotStatus, gotTruth, gotVersion, tc.status, tc.truth)
			}
		})
	}
}

// TestDiscoveryApply_ObserveIsIdempotent proves two applies of the same
// evidence leave exactly one 'observed' row, with its version unchanged
// by the second apply — recordEvidenceObserved never bumps version.
// MUTATION 2.4: bumping the version on an already-observed row turns
// this RED.
func TestDiscoveryApply_ObserveIsIdempotent(t *testing.T) {
	db := migratedCatalogDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")
	repo := NewDiscoveryRepo(db, sequentialTestIDs())
	ctx := context.Background()
	now := time.Unix(1000, 0)

	snapshotFor := func(gen int64) intelligence.DiscoverySnapshot {
		return intelligence.DiscoverySnapshot{
			AccountID: "acct1", ProviderID: "prov1", Generation: gen,
			Models: []intelligence.DiscoverySnapshotModel{{
				CanonicalKey: "key-a", ProviderModelID: "model-a",
				Operations: []models.Operation{models.OperationChat},
			}},
		}
	}

	gen1, _ := repo.BeginRun(ctx, "acct1", "run1", now)
	if applied, err := repo.Apply(ctx, "run1", snapshotFor(gen1), now); err != nil || !applied {
		t.Fatalf("first Apply = (%v, %v), want (true, nil)", applied, err)
	}

	var opID string
	if err := db.Conn().QueryRow(
		`SELECT id FROM offering_operations WHERE account_id = ? AND provider_model_id = ? AND operation = ?`,
		"acct1", "model-a", "chat",
	).Scan(&opID); err != nil {
		t.Fatalf("query offering_operations: %v", err)
	}
	var versionAfterFirst int
	if err := db.Conn().QueryRow(`SELECT version FROM certifications WHERE offering_operation_id = ?`, opID).Scan(&versionAfterFirst); err != nil {
		t.Fatalf("query certifications: %v", err)
	}

	gen2, _ := repo.BeginRun(ctx, "acct1", "run2", now)
	if applied, err := repo.Apply(ctx, "run2", snapshotFor(gen2), now); err != nil || !applied {
		t.Fatalf("second Apply = (%v, %v), want (true, nil)", applied, err)
	}

	var rowCount int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM certifications WHERE offering_operation_id = ?`, opID).Scan(&rowCount); err != nil {
		t.Fatalf("count certifications: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("certifications row count = %d, want exactly 1", rowCount)
	}

	var status string
	var versionAfterSecond int
	if err := db.Conn().QueryRow(`SELECT status, version FROM certifications WHERE offering_operation_id = ?`, opID).Scan(&status, &versionAfterSecond); err != nil {
		t.Fatalf("query certifications: %v", err)
	}
	if status != "observed" {
		t.Fatalf("status = %q, want observed", status)
	}
	if versionAfterSecond != versionAfterFirst {
		t.Fatalf("version changed from %d to %d on the second (idempotent) apply", versionAfterFirst, versionAfterSecond)
	}
	// And edge 1 must not bump the version AT ALL — comparing before/after
	// the second apply alone would still pass if the FIRST one bumped it.
	// models.Certification.Transition's default branch carries Version
	// forward untouched; only probing -> certified (edge 4) ever bumps it,
	// so the baseline 1 must survive both applies.
	if versionAfterFirst != 1 {
		t.Fatalf("version after the first apply = %d, want the untouched baseline 1 — edge 1 never bumps version", versionAfterFirst)
	}
}

// TestDiscoveryApply_NoSecondConnection proves the apply path completes
// under a bounded context: recordEvidenceObserved runs the advance on tx
// itself, never a second connection against db's own SetMaxOpenConns(1)
// pool. MUTATION 2.5: switching the advance to r.db.Conn() would try to
// acquire a second connection while tx holds the pool's only one — a
// deadlock this timeout would catch instead of hanging forever.
func TestDiscoveryApply_NoSecondConnection(t *testing.T) {
	db := migratedCatalogDB(t)
	insertProvider(t, db, "prov1")
	insertAccount(t, db, "acct1", "prov1")
	repo := NewDiscoveryRepo(db, sequentialTestIDs())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := time.Unix(1000, 0)

	snapshotFor := func(gen int64) intelligence.DiscoverySnapshot {
		return intelligence.DiscoverySnapshot{
			AccountID: "acct1", ProviderID: "prov1", Generation: gen,
			Models: []intelligence.DiscoverySnapshotModel{{
				CanonicalKey: "key-a", ProviderModelID: "model-a",
				Operations: []models.Operation{models.OperationChat},
			}},
		}
	}

	// The first Apply only creates the 'discovered' baseline — it never
	// calls recordEvidenceObserved (see ensureOfferingOperation's own doc
	// comment). It is the SECOND Apply (re-discovery of an already-known
	// offering-operation) that exercises recordEvidenceObserved, so it
	// must run inside the monitored goroutine below, not before it.
	gen1, err := repo.BeginRun(ctx, "acct1", "run1", now)
	if err != nil {
		t.Fatalf("BeginRun: %v", err)
	}
	if _, err := repo.Apply(ctx, "run1", snapshotFor(gen1), now); err != nil {
		t.Fatalf("first Apply: %v", err)
	}

	gen2, err := repo.BeginRun(ctx, "acct1", "run2", now)
	if err != nil {
		t.Fatalf("BeginRun: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, applyErr := repo.Apply(ctx, "run2", snapshotFor(gen2), now)
		done <- applyErr
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("second Apply: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("second Apply did not complete within the timeout — a second connection against the single-connection pool deadlocked")
	}
}

// TestDiscoveryRepo_SetNativeContextTokens_RoundTrip proves a positive
// probed limit is durably written onto the model's canonical row (this
// batch's whole point: the context probe's extracted number no longer gets
// thrown away). MUTATION: returning nil without executing the UPDATE turns
// this RED.
func TestDiscoveryRepo_SetNativeContextTokens_RoundTrip(t *testing.T) {
	db := migratedCatalogDB(t)
	insertModel(t, db, "model-a", "key-a")
	repo := NewDiscoveryRepo(db, sequentialTestIDs())
	ctx := context.Background()

	assertNativeContextTokens(t, db, "model-a", nil)

	if err := repo.SetNativeContextTokens(ctx, "model-a", 128000); err != nil {
		t.Fatalf("SetNativeContextTokens(128000) error = %v, want nil", err)
	}

	assertNativeContextTokens(t, db, "model-a", intPtr(128000))
}

// TestDiscoveryRepo_SetNativeContextTokens_NonPositiveRejected proves a
// non-positive limit is never written as if it were a real fact (04 §2: "a
// zero/negative declared limit fails the record rather than being
// stored") — the row is left exactly as it was. MUTATION: dropping the
// `tokens <= 0` guard turns this RED (0 would round-trip as a "fact").
func TestDiscoveryRepo_SetNativeContextTokens_NonPositiveRejected(t *testing.T) {
	db := migratedCatalogDB(t)
	insertModel(t, db, "model-a", "key-a")
	repo := NewDiscoveryRepo(db, sequentialTestIDs())
	ctx := context.Background()

	if err := repo.SetNativeContextTokens(ctx, "model-a", 0); err == nil {
		t.Fatalf("SetNativeContextTokens(0) error = nil, want an error — a non-positive limit is never a fact")
	}

	assertNativeContextTokens(t, db, "model-a", nil)
}

// TestDiscoveryRepo_SetNativeContextTokens_UnknownModelErrors proves a
// write against a modelID that matches no row is a typed error, not a
// silent no-op (mirrors CatalogRepo.SetQualityRating's identical
// ErrModelNotFound contract for a vanished-model write).
func TestDiscoveryRepo_SetNativeContextTokens_UnknownModelErrors(t *testing.T) {
	db := migratedCatalogDB(t)
	repo := NewDiscoveryRepo(db, sequentialTestIDs())
	ctx := context.Background()

	err := repo.SetNativeContextTokens(ctx, "does-not-exist", 128000)
	if !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("SetNativeContextTokens(unknown model) error = %v, want ErrModelNotFound", err)
	}
}

// TestApply_PrunesUndeclaredUncertifiedOperationRows and
// TestApply_KeepsUndeclaredButCertifiedOperationRows pin the two sides of
// discovery's operation-row reconciliation (the fix for the CandidateOperations
// mechanism removed in bc56160, whose rows outlived the code that created
// them): an operation row this run no longer declares is pruned ONLY when it
// also carries no evidence (capability_truth = 'unknown'); a row already
// certified 'supported' or 'unsupported' is evidence and must survive a run
// that declares less, because a run that cannot reach the catalog falls back
// to a narrower declaration set and pruning certified rows there would be a
// downgrade on missing evidence.

// newDiscoveryTestRepo returns a migrated storage DB seeded with provider
// "prov-1" and account "acct-1", plus a DiscoveryRepo over it — the shared
// setup for the operation-row reconciliation tests below.
func newDiscoveryTestRepo(t *testing.T) (*DiscoveryRepo, *DB) {
	t.Helper()
	db := migratedCatalogDB(t)
	insertProvider(t, db, "prov-1")
	insertAccount(t, db, "acct-1", "prov-1")
	return NewDiscoveryRepo(db, sequentialTestIDs()), db
}

// discoveryTestRunSeq backs nextDiscoveryTestRunID. The storage package's
// tests never run in parallel (no t.Parallel call anywhere in this package),
// so a bare package-level counter is race-free.
var discoveryTestRunSeq int

// nextDiscoveryTestRunID returns a fresh discovery_runs.id for applySnapshot
// — a distinct run id per Apply call, since discovery_runs.id is a primary
// key and BeginRun's generation is not known until after the id is chosen.
func nextDiscoveryTestRunID() string {
	discoveryTestRunSeq++
	return fmt.Sprintf("recon-run-%d", discoveryTestRunSeq)
}

// snapshotModel builds a minimal DiscoverySnapshotModel declaring ops for
// providerModelID, with a stable synthetic canonical key derived from the id.
func snapshotModel(providerModelID string, ops []models.Operation) intelligence.DiscoverySnapshotModel {
	return intelligence.DiscoverySnapshotModel{
		CanonicalKey:    "key-" + providerModelID,
		ProviderModelID: providerModelID,
		DisplayName:     providerModelID,
		Operations:      ops,
	}
}

// applySnapshot runs one full BeginRun+Apply cycle for accountID/providerID
// with ms as the declared models, failing the test on any error or on an
// unexpectedly superseded run.
func applySnapshot(t *testing.T, repo *DiscoveryRepo, ctx context.Context, accountID, providerID string, ms []intelligence.DiscoverySnapshotModel) {
	t.Helper()
	now := time.Unix(1000, 0)
	runID := nextDiscoveryTestRunID()
	gen, err := repo.BeginRun(ctx, accountID, runID, now)
	if err != nil {
		t.Fatalf("BeginRun(%q): %v", runID, err)
	}
	snapshot := intelligence.DiscoverySnapshot{
		AccountID: accountID, ProviderID: providerID, Generation: gen, Models: ms,
	}
	applied, err := repo.Apply(ctx, runID, snapshot, now)
	if err != nil {
		t.Fatalf("Apply(%q): %v", runID, err)
	}
	if !applied {
		t.Fatalf("Apply(%q) applied = false, want true", runID)
	}
}

// operationsFor returns the sorted operation strings currently recorded in
// offering_operations for (accountID, providerModelID).
func operationsFor(t *testing.T, db *DB, accountID, providerModelID string) []string {
	t.Helper()
	rows, err := db.Conn().Query(
		`SELECT operation FROM offering_operations WHERE account_id = ? AND provider_model_id = ? ORDER BY operation`,
		accountID, providerModelID,
	)
	if err != nil {
		t.Fatalf("query offering_operations for (%q,%q): %v", accountID, providerModelID, err)
	}
	defer func() { _ = rows.Close() }()
	var ops []string
	for rows.Next() {
		var op string
		if err := rows.Scan(&op); err != nil {
			t.Fatalf("scan operation for (%q,%q): %v", accountID, providerModelID, err)
		}
		ops = append(ops, op)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate offering_operations for (%q,%q): %v", accountID, providerModelID, err)
	}
	return ops
}

// certificationCountFor returns how many certifications rows remain joined
// to (accountID, providerModelID)'s offering_operations rows.
func certificationCountFor(t *testing.T, db *DB, accountID, providerModelID string) int {
	t.Helper()
	var n int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM certifications c
		 JOIN offering_operations oo ON oo.id = c.offering_operation_id
		 WHERE oo.account_id = ? AND oo.provider_model_id = ?`,
		accountID, providerModelID,
	).Scan(&n); err != nil {
		t.Fatalf("count certifications for (%q,%q): %v", accountID, providerModelID, err)
	}
	return n
}

// certifyOperationRow drives (accountID, providerModelID, operation)'s
// certification to a resolved verdict through the SAME production writer
// the real certification state machine uses —
// CertificationRepo.Load/CompareAndSwap plus models.Certification.Transition
// — rather than a hand-built UPDATE. The truth transition is exactly what
// TestApply_KeepsUndeclaredButCertifiedOperationRows's keep-guard depends
// on, so that test only proves what it claims if the certified row it
// builds has the shape a real probe verdict actually leaves behind: 04 §5
// edges 1/2/4 (discovered -> observed -> probing -> certified), walked one
// legal Transition + CompareAndSwap at a time, never a row shape production
// itself never produces (fix round 1, MINOR 5).
//
// truth must be "supported" or "unsupported" — Transition's own edge 4
// (probing -> certified) requires a resolved verdict (ErrVerdictRequired)
// and every row this helper is handed already exists at
// discovered/observed/unknown from a prior applySnapshot call, so there is
// nothing to walk toward "unknown".
func certifyOperationRow(t *testing.T, db *DB, accountID, providerModelID, operation, truth string) {
	t.Helper()
	verdict, err := models.ParseCapabilityTruth(truth)
	if err != nil || verdict == models.TruthUnknown {
		t.Fatalf("certifyOperationRow(%q,%q,%q,%q): truth must be %q or %q", accountID, providerModelID, operation, truth, models.TruthSupported, models.TruthUnsupported)
	}

	var opID string
	if err := db.Conn().QueryRow(
		`SELECT id FROM offering_operations WHERE account_id = ? AND provider_model_id = ? AND operation = ?`,
		accountID, providerModelID, operation,
	).Scan(&opID); err != nil {
		t.Fatalf("certify operation row (%q,%q,%q): lookup id: %v", accountID, providerModelID, operation, err)
	}

	ctx := context.Background()
	now := time.Unix(2000, 0)
	retry := models.RetryPolicy{Attempts: 1, Budget: 3}
	certRepo := NewCertificationRepo(db, nil)

	cert, err := certRepo.Load(ctx, opID)
	if err != nil {
		t.Fatalf("certify operation row (%q,%q,%q): load: %v", accountID, providerModelID, operation, err)
	}

	// Walk the legal transition graph up to certified. Production always
	// advances a freshly-applied row straight to 'observed'
	// (ensureOfferingOperation), so in practice this is observed -> probing
	// -> certified; the discovered branch is defensive, not load-bearing.
	for cert.State != models.CertCertified {
		var target models.CertificationState
		switch cert.State {
		case models.CertDiscovered:
			target = models.CertObserved
		case models.CertObserved:
			target = models.CertProbing
		case models.CertProbing:
			target = models.CertCertified
		default:
			t.Fatalf("certify operation row (%q,%q,%q): no legal path to certified from %s", accountID, providerModelID, operation, cert.State)
		}
		next, err := cert.Transition(target, verdict, retry, now)
		if err != nil {
			t.Fatalf("certify operation row (%q,%q,%q): transition %s -> %s: %v", accountID, providerModelID, operation, cert.State, target, err)
		}
		if err := certRepo.CompareAndSwap(ctx, cert, next); err != nil {
			t.Fatalf("certify operation row (%q,%q,%q): compare-and-swap %s -> %s: %v", accountID, providerModelID, operation, cert.State, target, err)
		}
		cert = next
	}
}

// TestApply_PrunesUndeclaredUncertifiedOperationRows proves the safety
// rule's prune side: an operation row this run no longer declares, and whose
// certification was never resolved by a probe or a prior declaration, is
// exactly the CandidateOperations leftover the live audit found — it cannot
// be certified (ListNonChatOperationsToCertify requires the operation to
// appear in capabilities_json) and no probe path can reach it, so without
// this it sits in the review backlog forever.
//
// It also proves the prune is account-scoped, not merely operation-set-
// scoped: acct-2 offers the SAME provider_model_id ("m-1" — think two
// ClinePass accounts both offering mimo-v2.5) with its own "tools" row,
// whose certification is truth='unknown' (never probed) and whose operation
// is not in acct-1's second-run declared set either. To a query that joins
// only on provider_model_id and the declared-set/truth predicate — dropping
// the account_id filter — acct-2's row is indistinguishable from acct-1's
// genuinely stale one. It must survive acct-1's reconciliation untouched;
// only acct-2's own next run may ever prune it. Fix round 1 (adversarial
// review): a mutation removing `oo.account_id = ?` from the certifications
// DELETE while leaving the placeholder bound left the ENTIRE
// internal/storage suite green before this assertion existed — see the
// mutation trace in the task report.
func TestApply_PrunesUndeclaredUncertifiedOperationRows(t *testing.T) {
	repo, db := newDiscoveryTestRepo(t)
	ctx := context.Background()

	insertAccount(t, db, "acct-2", "prov-1")
	applySnapshot(t, repo, ctx, "acct-2", "prov-1", []intelligence.DiscoverySnapshotModel{
		snapshotModel("m-1", []models.Operation{models.OperationChat, models.OperationTools}),
	})

	// Run 1: the model declares chat and tools.
	applySnapshot(t, repo, ctx, "acct-1", "prov-1", []intelligence.DiscoverySnapshotModel{
		snapshotModel("m-1", []models.Operation{models.OperationChat, models.OperationTools}),
	})
	if got := operationsFor(t, db, "acct-1", "m-1"); !reflect.DeepEqual(got, []string{"chat", "tools"}) {
		t.Fatalf("after run 1 operations = %v, want [chat tools]", got)
	}

	// Run 2: tools is no longer declared and was never certified.
	applySnapshot(t, repo, ctx, "acct-1", "prov-1", []intelligence.DiscoverySnapshotModel{
		snapshotModel("m-1", []models.Operation{models.OperationChat}),
	})
	if got := operationsFor(t, db, "acct-1", "m-1"); !reflect.DeepEqual(got, []string{"chat"}) {
		t.Fatalf("after run 2 operations = %v, want [chat] — an undeclared, never-certified row must not survive; it is what the review backlog counts and no probe can resolve it", got)
	}
	if n := certificationCountFor(t, db, "acct-1", "m-1"); n != 1 {
		t.Fatalf("certifications for m-1 = %d, want 1 — the pruned operation's certification row must go with it", n)
	}

	// acct-2's own row set for the SAME provider_model_id must be
	// completely untouched by acct-1's reconciliation — the account_id
	// predicate, not the operation set, is what must keep the two accounts
	// apart. If this regresses, account B's certified rows can be silently
	// deleted by account A's discovery run, orphaning B's operation rows;
	// B's rows then vanish on B's own next run even though they were
	// certified.
	if got := operationsFor(t, db, "acct-2", "m-1"); !reflect.DeepEqual(got, []string{"chat", "tools"}) {
		t.Fatalf("acct-2 operations = %v, want [chat tools] unchanged — acct-1's reconciliation must never cross into another account's rows for the same provider_model_id", got)
	}
	if n := certificationCountFor(t, db, "acct-2", "m-1"); n != 2 {
		t.Fatalf("acct-2 certifications = %d, want 2 unchanged — acct-1's reconciliation must never delete another account's certification rows", n)
	}
}

// TestApply_KeepsUndeclaredButCertifiedOperationRows proves the safety
// rule's keep side: a certified capability is evidence and must survive a
// run that could not see the catalog (the never-downgrade invariant) — the
// exact live failure mode the brief warns about, where a ClinePass run that
// cannot reach models.dev falls back to declaring only chat.
func TestApply_KeepsUndeclaredButCertifiedOperationRows(t *testing.T) {
	repo, db := newDiscoveryTestRepo(t)
	ctx := context.Background()

	applySnapshot(t, repo, ctx, "acct-1", "prov-1", []intelligence.DiscoverySnapshotModel{
		snapshotModel("m-1", []models.Operation{models.OperationChat, models.OperationTools}),
	})
	certifyOperationRow(t, db, "acct-1", "m-1", "tools", "supported")

	// The catalog was unreachable this run, so only chat is declared.
	applySnapshot(t, repo, ctx, "acct-1", "prov-1", []intelligence.DiscoverySnapshotModel{
		snapshotModel("m-1", []models.Operation{models.OperationChat}),
	})

	got := operationsFor(t, db, "acct-1", "m-1")
	if !reflect.DeepEqual(got, []string{"chat", "tools"}) {
		t.Fatalf("operations = %v, want [chat tools] — a certified capability is evidence and must survive a run that could not see the catalog (never-downgrade invariant)", got)
	}
}

func assertNativeContextTokens(t *testing.T, db *DB, modelID string, want *int) {
	t.Helper()
	var got sql.NullInt64
	if err := db.Conn().QueryRow(`SELECT native_context_tokens FROM models WHERE id = ?`, modelID).Scan(&got); err != nil {
		t.Fatalf("query native_context_tokens for %q: %v", modelID, err)
	}
	switch {
	case want == nil && got.Valid:
		t.Fatalf("models(%q).native_context_tokens = %d, want NULL", modelID, got.Int64)
	case want != nil && !got.Valid:
		t.Fatalf("models(%q).native_context_tokens = NULL, want %d", modelID, *want)
	case want != nil && got.Valid && got.Int64 != int64(*want):
		t.Fatalf("models(%q).native_context_tokens = %d, want %d", modelID, got.Int64, *want)
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

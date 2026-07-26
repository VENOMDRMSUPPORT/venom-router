package storage

import (
	"context"
	"testing"
)

// catalogVersion is the goose version of the M4 catalog-discovery migration
// (00006_catalog_discovery.sql).
const catalogVersion = 6

// TestMigrateCatalog_UpDownUp proves M4 (models, provider_model_aliases,
// account_model_offerings, offering_operations, certifications,
// discovery_runs) applies, rolls back to exactly the M3 state (every lower
// table survives), and re-applies. The rollback loop is count-agnostic: it
// rolls back every migration at or above catalogVersion, so a later M5
// lands without silently breaking this test (mirrors the M2/M3/M5
// up/down tests' robustness shape).
func TestMigrateCatalog_UpDownUp(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() (up) error = %v", err)
	}
	assertTableExists(t, db, "models", true)
	assertTableExists(t, db, "provider_model_aliases", true)
	assertTableExists(t, db, "account_model_offerings", true)
	assertTableExists(t, db, "offering_operations", true)
	assertTableExists(t, db, "certifications", true)
	assertTableExists(t, db, "discovery_runs", true)

	// Roll back every migration applied at or above M4, then M4 itself, so
	// this proves M4's own down path no matter how many later migrations
	// land (catalogVersion is M4's goose version).
	for currentVersion(t, db) >= catalogVersion {
		if _, err := DownOne(ctx, db); err != nil {
			t.Fatalf("DownOne() error = %v", err)
		}
	}
	assertTableExists(t, db, "models", false)
	assertTableExists(t, db, "provider_model_aliases", false)
	assertTableExists(t, db, "account_model_offerings", false)
	assertTableExists(t, db, "offering_operations", false)
	assertTableExists(t, db, "certifications", false)
	assertTableExists(t, db, "discovery_runs", false)
	// Every lower table must survive rolling back only M4.
	assertTableExists(t, db, "owner_settings", true)
	assertTableExists(t, db, "audit_events", true)
	assertTableExists(t, db, "jobs", true)
	assertTableExists(t, db, "providers", true)
	assertTableExists(t, db, "accounts", true)
	assertTableExists(t, db, "account_credentials", true)
	assertTableExists(t, db, "account_funding_evidence", true)
	assertTableExists(t, db, "oauth_transactions", true)
	assertTableExists(t, db, "owner_auth", true)
	assertTableExists(t, db, "owner_sessions", true)
	assertTableExists(t, db, "auth_events", true)
	assertBaselineTableExists(t, db, true)

	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() (re-up) error = %v", err)
	}
	assertTableExists(t, db, "models", true)
	assertTableExists(t, db, "provider_model_aliases", true)
	assertTableExists(t, db, "account_model_offerings", true)
	assertTableExists(t, db, "offering_operations", true)
	assertTableExists(t, db, "certifications", true)
	assertTableExists(t, db, "discovery_runs", true)
}

// TestCertifications_StatusCheck proves the six-state certification lifecycle
// CHECK is a mutation-provable DB-level invariant: every one of the six
// frozen states (discovered, observed, probing, certified, suspended,
// expired) is accepted, and there is deliberately no seventh `rejected`
// state (04 §5). A reviewer re-proves this by dropping the CHECK and
// confirming this test goes RED.
func TestCertifications_StatusCheck(t *testing.T) {
	db := migratedCatalogDB(t)
	opID := seedOfferingOperationChain(t, db, "acct-status", "prov-status", "model-status", "chat")

	for i, status := range []string{"discovered", "observed", "probing", "certified", "suspended", "expired"} {
		id := opID
		if i > 0 {
			// Each certification row is 1:1 with its offering_operation, so
			// exercise every status via a fresh offering_operation chain.
			id = seedOfferingOperationChain(t, db, "acct-status", "prov-status", "model-status", status+"-op")
		}
		if err := insertCertification(db, id, status, "unknown"); err != nil {
			t.Fatalf("insert certification with allowed status %q: %v, want success", status, err)
		}
	}

	badID := seedOfferingOperationChain(t, db, "acct-status", "prov-status", "model-status", "rejected-op")
	if err := insertCertification(db, badID, "rejected", "unknown"); err == nil {
		t.Fatalf("insert certification with disallowed status %q succeeded, want CHECK rejection", "rejected")
	}
}

// TestCertifications_CapabilityTruthSeparateFromStatus proves capability
// truth (unknown/supported/unsupported) is its own column/dimension,
// independent from the certification-state column (04 §5): the CHECK on
// capability_truth is its own mutation-provable invariant, and updating one
// column never touches the other. A reviewer re-proves this by dropping the
// capability_truth CHECK and confirming the rejection assertion goes RED.
func TestCertifications_CapabilityTruthSeparateFromStatus(t *testing.T) {
	db := migratedCatalogDB(t)
	opID := seedOfferingOperationChain(t, db, "acct-truth", "prov-truth", "model-truth", "chat")

	for _, truth := range []string{"unknown", "supported", "unsupported"} {
		id := seedOfferingOperationChain(t, db, "acct-truth", "prov-truth", "model-truth", "op-"+truth)
		if err := insertCertification(db, id, "discovered", truth); err != nil {
			t.Fatalf("insert certification with allowed capability_truth %q: %v, want success", truth, err)
		}
	}

	if err := insertCertification(db, opID, "discovered", "maybe"); err == nil {
		t.Fatalf("insert certification with disallowed capability_truth %q succeeded, want CHECK rejection", "maybe")
	}

	// The two columns are independent: certified + unsupported is a
	// legitimate (if not routable) combination, proving status alone never
	// implies a particular capability_truth value or vice versa.
	certifiedID := seedOfferingOperationChain(t, db, "acct-truth", "prov-truth", "model-truth", "op-certified-unsupported")
	if err := insertCertification(db, certifiedID, "certified", "unsupported"); err != nil {
		t.Fatalf("insert certification status=certified truth=unsupported: %v, want success (separate dimensions)", err)
	}

	// Updating status alone must not change capability_truth, and vice
	// versa — proving these are genuinely separate columns, not one
	// conflated field.
	if _, err := db.Conn().Exec(
		`UPDATE certifications SET status = 'suspended' WHERE offering_operation_id = ?`, certifiedID,
	); err != nil {
		t.Fatalf("update status: %v", err)
	}
	var truthAfter string
	if err := db.Conn().QueryRow(
		`SELECT capability_truth FROM certifications WHERE offering_operation_id = ?`, certifiedID,
	).Scan(&truthAfter); err != nil {
		t.Fatalf("query capability_truth after status update: %v", err)
	}
	if truthAfter != "unsupported" {
		t.Fatalf("capability_truth after unrelated status update = %q, want %q (columns must be independent)", truthAfter, "unsupported")
	}
}

// TestAccountModelOfferings_OfferingIdentityUnique proves offering identity
// `UNIQUE(account_id, provider_model_id)` (02 §5) is a mutation-provable
// DB-level invariant. A reviewer re-proves this by dropping the UNIQUE
// constraint and confirming this test goes RED.
func TestAccountModelOfferings_OfferingIdentityUnique(t *testing.T) {
	db := migratedCatalogDB(t)
	insertProvider(t, db, "prov-identity")
	insertAccount(t, db, "acct-identity", "prov-identity")
	modelID := insertModel(t, db, "model-identity", "canonical-identity-sha")

	if err := insertOffering(db, "acct-identity", "prov-identity", "gpt-identity", modelID); err != nil {
		t.Fatalf("insert first offering: %v, want success", err)
	}
	if err := insertOffering(db, "acct-identity", "prov-identity", "gpt-identity", modelID); err == nil {
		t.Fatalf("insert duplicate offering (account_id, provider_model_id) succeeded, want UNIQUE rejection")
	}
}

// TestModels_CanonicalKeyUnique proves `canonical_key_sha256 UNIQUE` (02 §5)
// is a mutation-provable DB-level invariant: two models cannot share a
// canonical key. A reviewer re-proves this by dropping the UNIQUE
// constraint and confirming this test goes RED.
func TestModels_CanonicalKeyUnique(t *testing.T) {
	db := migratedCatalogDB(t)

	insertModel(t, db, "model-a", "same-canonical-key")
	if _, err := db.Conn().Exec(
		`INSERT INTO models (id, canonical_key_sha256, created_at, updated_at) VALUES (?, ?, 0, 0)`,
		"model-b", "same-canonical-key",
	); err == nil {
		t.Fatalf("insert second model with duplicate canonical_key_sha256 succeeded, want UNIQUE rejection")
	}
}

func migratedCatalogDB(t *testing.T) *DB {
	t.Helper()

	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return db
}

// insertModel seeds a minimal models row and returns its id.
func insertModel(t *testing.T, db *DB, id, canonicalKeySHA256 string) string {
	t.Helper()
	_, err := db.Conn().Exec(
		`INSERT INTO models (id, canonical_key_sha256, created_at, updated_at) VALUES (?, ?, 0, 0)`,
		id, canonicalKeySHA256,
	)
	if err != nil {
		t.Fatalf("insert model %s: %v", id, err)
	}
	return id
}

// insertOffering seeds a minimal account_model_offerings row.
func insertOffering(db *DB, accountID, providerID, providerModelID, modelID string) error {
	_, err := db.Conn().Exec(
		`INSERT INTO account_model_offerings
		    (account_id, provider_id, provider_model_id, model_id, availability, first_seen_at, last_seen_at)
		 VALUES (?, ?, ?, ?, 'available', 0, 0)`,
		accountID, providerID, providerModelID, modelID,
	)
	return err
}

// insertOfferingOperation seeds a minimal offering_operations row and
// returns its id.
func insertOfferingOperation(db *DB, id, accountID, providerID, providerModelID, operation string) error {
	_, err := db.Conn().Exec(
		`INSERT INTO offering_operations
		    (id, account_id, provider_id, provider_model_id, operation, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 0, 0)`,
		id, accountID, providerID, providerModelID, operation,
	)
	return err
}

// insertCertification seeds a minimal certifications row keyed by
// offering_operation_id.
func insertCertification(db *DB, offeringOperationID, status, capabilityTruth string) error {
	_, err := db.Conn().Exec(
		`INSERT INTO certifications
		    (offering_operation_id, status, capability_truth, created_at, updated_at)
		 VALUES (?, ?, ?, 0, 0)`,
		offeringOperationID, status, capabilityTruth,
	)
	return err
}

// seedOfferingOperationChain seeds the full parent chain an
// offering_operations row requires under foreign_keys=ON (provider, account,
// model, offering), then the offering_operation itself, returning its id.
// The provider/account/model rows are reseeded idempotently (INSERT OR
// IGNORE-equivalent via a pre-check) so callers can invoke this repeatedly
// for the same account/provider/model with a distinct provider_model_id per
// operation.
func seedOfferingOperationChain(t *testing.T, db *DB, accountID, providerID, modelKey, providerModelID string) string {
	t.Helper()

	if !rowExists(t, db, "providers", "id", providerID) {
		insertProvider(t, db, providerID)
	}
	if !rowExists(t, db, "accounts", "id", accountID) {
		insertAccount(t, db, accountID, providerID)
	}
	modelID := modelKey + "-model"
	if !rowExists(t, db, "models", "id", modelID) {
		insertModel(t, db, modelID, modelKey+"-canonical")
	}
	if err := insertOffering(db, accountID, providerID, providerModelID, modelID); err != nil {
		t.Fatalf("seed offering (%s, %s): %v", accountID, providerModelID, err)
	}

	opID := providerModelID + "-op"
	if err := insertOfferingOperation(db, opID, accountID, providerID, providerModelID, "chat"); err != nil {
		t.Fatalf("seed offering_operation %s: %v", opID, err)
	}
	return opID
}

func rowExists(t *testing.T, db *DB, table, column, value string) bool {
	t.Helper()
	var got string
	err := db.Conn().QueryRow(
		`SELECT `+column+` FROM `+table+` WHERE `+column+` = ?`, value,
	).Scan(&got)
	return err == nil
}

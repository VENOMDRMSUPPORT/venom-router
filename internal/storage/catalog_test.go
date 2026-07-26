package storage

import (
	"context"
	"testing"
)

// migratedCatalogRepoDB opens and fully migrates a fresh temp-dir DB,
// returning it (with a t.Cleanup Close). Mirrors migratedCatalogDB
// (migrate_catalog_test.go), kept separate so this file's tests do not
// depend on that file's naming.
func migratedCatalogRepoDB(t *testing.T) *DB {
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

// insertModelFull seeds a fully-populated models row.
func insertModelFull(t *testing.T, db *DB, id, canonicalKey, displayName string, nativeContextTokens *int, nativeModalitiesJSON *string, qualityRating *float64) {
	t.Helper()
	_, err := db.Conn().Exec(
		`INSERT INTO models (id, canonical_key_sha256, display_name, native_context_tokens, native_modalities_json, quality_rating, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, 0, 0)`,
		id, canonicalKey, displayName, nativeContextTokens, nativeModalitiesJSON, qualityRating,
	)
	if err != nil {
		t.Fatalf("insert model %s: %v", id, err)
	}
}

// insertOfferingFull seeds a fully-populated account_model_offerings row.
func insertOfferingFull(t *testing.T, db *DB, accountID, providerID, providerModelID, modelID string, contextLength, maxInputTokens, maxOutputTokens *int, capabilitiesJSON, pricingJSON *string, firstSeenAt, lastSeenAt int64) {
	t.Helper()
	_, err := db.Conn().Exec(
		`INSERT INTO account_model_offerings
		    (account_id, provider_id, provider_model_id, model_id, availability, context_length, max_input_tokens, max_output_tokens, capabilities_json, pricing_json, first_seen_at, last_seen_at)
		 VALUES (?, ?, ?, ?, 'available', ?, ?, ?, ?, ?, ?, ?)`,
		accountID, providerID, providerModelID, modelID, contextLength, maxInputTokens, maxOutputTokens, capabilitiesJSON, pricingJSON, firstSeenAt, lastSeenAt,
	)
	if err != nil {
		t.Fatalf("insert offering (%s,%s): %v", accountID, providerModelID, err)
	}
}

// insertOfferingOperationFull seeds an offering_operations row and,
// optionally, its 1:1 certifications row (certStatus == "" skips the
// certification insert entirely, exercising the LEFT JOIN's no-cert path).
func insertOfferingOperationFull(t *testing.T, db *DB, id, accountID, providerID, providerModelID, operation, certStatus, certTruth string, certVersion int, certifiedAt *int64, evidenceRef string) {
	t.Helper()
	if err := insertOfferingOperation(db, id, accountID, providerID, providerModelID, operation); err != nil {
		t.Fatalf("insert offering_operation %s: %v", id, err)
	}
	if certStatus == "" {
		return
	}
	_, err := db.Conn().Exec(
		`INSERT INTO certifications (offering_operation_id, status, capability_truth, version, certified_at, evidence_ref, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, 0, 0)`,
		id, certStatus, certTruth, certVersion, certifiedAt, evidenceRef,
	)
	if err != nil {
		t.Fatalf("insert certification for %s: %v", id, err)
	}
}

func catalogFloat64Ptr(v float64) *float64 { return &v }
func catalogInt64Ptr(v int64) *int64       { return &v }
func catalogStrPtr(v string) *string       { return &v }

// TestCatalogRepo_ListOfferings_JoinsModelAndCertifications proves
// ListOfferings joins account_model_offerings with its parent models row
// and every offering_operations/certifications row scoped to it — including
// an offering-operation with NO certifications row (LEFT JOIN fallback to
// the discovered/unknown baseline) and one WITH a fully-populated
// certification.
func TestCatalogRepo_ListOfferings_JoinsModelAndCertifications(t *testing.T) {
	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-1")
	insertAccount(t, db, "acct-1", "prov-1")
	insertModelFull(t, db, "model-1", "ck-1", "GPT Test", intPtr(8000), catalogStrPtr(`["text"]`), catalogFloat64Ptr(87.5))
	insertOfferingFull(t, db, "acct-1", "prov-1", "gpt-test", "model-1",
		intPtr(4096), intPtr(3000), intPtr(1000),
		catalogStrPtr(`["chat","vision"]`), catalogStrPtr(`{"cost":{"input":0,"output":0}}`),
		100, 200)
	insertOfferingOperationFull(t, db, "op-chat", "acct-1", "prov-1", "gpt-test", "chat",
		"certified", "supported", 2, catalogInt64Ptr(150), "ev-1")
	// vision has NO certifications row at all.
	if err := insertOfferingOperation(db, "op-vision", "acct-1", "prov-1", "gpt-test", "vision"); err != nil {
		t.Fatalf("insert offering_operation op-vision: %v", err)
	}

	repo := NewCatalogRepo(db)
	rows, next, err := repo.ListOfferings(context.Background(), CatalogListParams{AccountID: "acct-1", Limit: 10})
	if err != nil {
		t.Fatalf("ListOfferings: %v", err)
	}
	if next != "" {
		t.Fatalf("next cursor = %q, want '' (single page)", next)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}

	assertJoinedOfferingFields(t, rows[0])
	assertJoinedOfferingOperations(t, rows[0])
}

// assertJoinedOfferingFields checks the offering/model-joined scalar fields
// for TestCatalogRepo_ListOfferings_JoinsModelAndCertifications, split out
// to keep that test's cyclomatic complexity down.
func assertJoinedOfferingFields(t *testing.T, row CatalogOfferingRow) {
	t.Helper()
	if row.ProviderID != "prov-1" || row.ProviderModelID != "gpt-test" || row.ModelID != "model-1" {
		t.Fatalf("identity = %+v, want prov-1/gpt-test/model-1", row)
	}
	if row.ContextLength == nil || *row.ContextLength != 4096 {
		t.Fatalf("ContextLength = %v, want 4096", row.ContextLength)
	}
	if row.MaxInputTokens == nil || *row.MaxInputTokens != 3000 {
		t.Fatalf("MaxInputTokens = %v, want 3000", row.MaxInputTokens)
	}
	if row.MaxOutputTokens == nil || *row.MaxOutputTokens != 1000 {
		t.Fatalf("MaxOutputTokens = %v, want 1000", row.MaxOutputTokens)
	}
	if len(row.Capabilities) != 2 || row.Capabilities[0] != "chat" || row.Capabilities[1] != "vision" {
		t.Fatalf("Capabilities = %v, want [chat vision]", row.Capabilities)
	}
	if row.Pricing == nil {
		t.Fatalf("Pricing = nil, want decoded map")
	}
	if row.ModelDisplayName != "GPT Test" {
		t.Fatalf("ModelDisplayName = %q, want GPT Test", row.ModelDisplayName)
	}
	if row.NativeContextTokens == nil || *row.NativeContextTokens != 8000 {
		t.Fatalf("NativeContextTokens = %v, want 8000", row.NativeContextTokens)
	}
	if len(row.NativeModalities) != 1 || row.NativeModalities[0] != "text" {
		t.Fatalf("NativeModalities = %v, want [text]", row.NativeModalities)
	}
	if row.QualityRating == nil || *row.QualityRating != 87.5 {
		t.Fatalf("QualityRating = %v, want 87.5", row.QualityRating)
	}
}

// assertJoinedOfferingOperations checks the offering_operations/
// certifications join for TestCatalogRepo_ListOfferings_JoinsModelAndCertifications
// — one operation WITH a certification row, one WITHOUT.
func assertJoinedOfferingOperations(t *testing.T, row CatalogOfferingRow) {
	t.Helper()
	if len(row.Operations) != 2 {
		t.Fatalf("len(Operations) = %d, want 2", len(row.Operations))
	}
	var chatOp, visionOp *CatalogOperationRow
	for i := range row.Operations {
		switch row.Operations[i].Operation {
		case "chat":
			chatOp = &row.Operations[i]
		case "vision":
			visionOp = &row.Operations[i]
		}
	}
	if chatOp == nil || visionOp == nil {
		t.Fatalf("Operations = %+v, want chat and vision", row.Operations)
	}
	if chatOp.CertificationStatus != "certified" || chatOp.CapabilityTruth != "supported" || chatOp.CertificationVersion != 2 {
		t.Fatalf("chat op = %+v, want certified/supported/2", chatOp)
	}
	if chatOp.CertifiedAt == nil || chatOp.CertifiedAt.Unix() != 150 {
		t.Fatalf("chat op CertifiedAt = %v, want 150", chatOp.CertifiedAt)
	}
	if chatOp.EvidenceRef != "ev-1" {
		t.Fatalf("chat op EvidenceRef = %q, want ev-1", chatOp.EvidenceRef)
	}
	// vision has no certifications row: the LEFT JOIN fallback baseline.
	if visionOp.CertificationStatus != "discovered" || visionOp.CapabilityTruth != "unknown" || visionOp.CertificationVersion != 1 {
		t.Fatalf("vision op (no cert row) = %+v, want discovered/unknown/1 fallback", visionOp)
	}
	if visionOp.CertifiedAt != nil {
		t.Fatalf("vision op CertifiedAt = %v, want nil (no cert row)", visionOp.CertifiedAt)
	}
}

// TestCatalogRepo_NullableColumnsStayUnknown proves absent context/limits/
// quality/native-modality columns decode to nil, and a malformed
// capabilities_json/pricing_json value yields the unknown form (nil)
// without failing the list.
func TestCatalogRepo_NullableColumnsStayUnknown(t *testing.T) {
	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-2")
	insertAccount(t, db, "acct-2", "prov-2")
	insertModelFull(t, db, "model-2", "ck-2", "", nil, nil, nil)
	insertOfferingFull(t, db, "acct-2", "prov-2", "unknown-model", "model-2",
		nil, nil, nil,
		catalogStrPtr("{not-valid-json"), catalogStrPtr("also not json"),
		0, 0)

	repo := NewCatalogRepo(db)
	rows, _, err := repo.ListOfferings(context.Background(), CatalogListParams{AccountID: "acct-2", Limit: 10})
	if err != nil {
		t.Fatalf("ListOfferings: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	row := rows[0]

	if row.ContextLength != nil {
		t.Fatalf("ContextLength = %v, want nil", row.ContextLength)
	}
	if row.MaxInputTokens != nil {
		t.Fatalf("MaxInputTokens = %v, want nil", row.MaxInputTokens)
	}
	if row.MaxOutputTokens != nil {
		t.Fatalf("MaxOutputTokens = %v, want nil", row.MaxOutputTokens)
	}
	if row.Capabilities != nil {
		t.Fatalf("Capabilities (malformed JSON) = %v, want nil", row.Capabilities)
	}
	if row.Pricing != nil {
		t.Fatalf("Pricing (malformed JSON) = %v, want nil", row.Pricing)
	}
	if row.NativeContextTokens != nil {
		t.Fatalf("NativeContextTokens = %v, want nil", row.NativeContextTokens)
	}
	if row.NativeModalities != nil {
		t.Fatalf("NativeModalities = %v, want nil", row.NativeModalities)
	}
	if row.QualityRating != nil {
		t.Fatalf("QualityRating = %v, want nil", row.QualityRating)
	}
}

// TestCatalogRepo_ListOfferings_CursorPagination proves deterministic
// ordering by (account_id, provider_model_id): no row is repeated or
// skipped across pages, and the last page returns "".
func TestCatalogRepo_ListOfferings_CursorPagination(t *testing.T) {
	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-page")
	insertAccount(t, db, "acct-page", "prov-page")
	insertModelFull(t, db, "model-page", "ck-page", "Page Model", nil, nil, nil)
	for _, pmID := range []string{"m-a", "m-b", "m-c"} {
		insertOfferingFull(t, db, "acct-page", "prov-page", pmID, "model-page", nil, nil, nil, nil, nil, 0, 0)
	}

	repo := NewCatalogRepo(db)
	ctx := context.Background()

	page1, cursor1, err := repo.ListOfferings(ctx, CatalogListParams{AccountID: "acct-page", Limit: 2})
	if err != nil {
		t.Fatalf("ListOfferings page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("len(page1) = %d, want 2", len(page1))
	}
	if page1[0].ProviderModelID != "m-a" || page1[1].ProviderModelID != "m-b" {
		t.Fatalf("page1 order = [%s %s], want [m-a m-b]", page1[0].ProviderModelID, page1[1].ProviderModelID)
	}
	if cursor1 == "" {
		t.Fatalf("cursor1 = '', want a non-empty next-page cursor")
	}

	page2, cursor2, err := repo.ListOfferings(ctx, CatalogListParams{AccountID: "acct-page", Limit: 2, Cursor: cursor1})
	if err != nil {
		t.Fatalf("ListOfferings page2: %v", err)
	}
	if len(page2) != 1 {
		t.Fatalf("len(page2) = %d, want 1", len(page2))
	}
	if page2[0].ProviderModelID != "m-c" {
		t.Fatalf("page2[0] = %s, want m-c", page2[0].ProviderModelID)
	}
	if cursor2 != "" {
		t.Fatalf("cursor2 = %q, want '' (last page)", cursor2)
	}
}

// TestCatalogRepo_GetOperationCertification_UnknownIDNotFound proves an
// unknown offering_operation id resolves to ok=false, never an error.
func TestCatalogRepo_GetOperationCertification_UnknownIDNotFound(t *testing.T) {
	db := migratedCatalogRepoDB(t)
	repo := NewCatalogRepo(db)

	_, ok, err := repo.GetOperationCertification(context.Background(), "does-not-exist")
	if err != nil {
		t.Fatalf("GetOperationCertification: %v", err)
	}
	if ok {
		t.Fatalf("ok = true for an unknown offering_operation id, want false")
	}
}

// TestCatalogRepo_GetOperationCertification_ReadsSeededRow proves a seeded
// offering_operation + certification round-trips through
// GetOperationCertification, including AccountID/ProviderModelID (which the
// CAPI-002 certification-read handler needs).
func TestCatalogRepo_GetOperationCertification_ReadsSeededRow(t *testing.T) {
	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-cert")
	insertAccount(t, db, "acct-cert", "prov-cert")
	insertModelFull(t, db, "model-cert", "ck-cert", "Cert Model", nil, nil, nil)
	insertOfferingFull(t, db, "acct-cert", "prov-cert", "cert-model", "model-cert", nil, nil, nil, nil, nil, 0, 0)
	insertOfferingOperationFull(t, db, "op-cert-1", "acct-cert", "prov-cert", "cert-model", "chat",
		"probing", "unknown", 1, nil, "")

	repo := NewCatalogRepo(db)
	op, ok, err := repo.GetOperationCertification(context.Background(), "op-cert-1")
	if err != nil {
		t.Fatalf("GetOperationCertification: %v", err)
	}
	if !ok {
		t.Fatalf("ok = false for a seeded offering_operation, want true")
	}
	if op.AccountID != "acct-cert" || op.ProviderModelID != "cert-model" || op.Operation != "chat" {
		t.Fatalf("op identity = %+v, want acct-cert/cert-model/chat", op)
	}
	if op.CertificationStatus != "probing" || op.CapabilityTruth != "unknown" {
		t.Fatalf("op state = %+v, want probing/unknown", op)
	}
}

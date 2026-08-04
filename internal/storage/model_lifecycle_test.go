package storage

import (
	"context"
	"testing"
)

// TestModelLifecycleRepo_PurgeInactiveKeepsOnlyLiveCatalog catches the
// operational lie where models from expired/degraded accounts or withdrawn
// offerings survive as if they were usable. A model is retained only while at
// least one available offering belongs to a connected, healthy account that is
// not reauthenticating.
func TestModelLifecycleRepo_PurgeInactiveKeepsOnlyLiveCatalog(t *testing.T) {
	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "provider-live")
	insertProvider(t, db, "provider-dead")
	insertAccount(t, db, "acct-live", "provider-live")
	insertAccount(t, db, "acct-expired", "provider-dead")
	insertAccount(t, db, "acct-reauth", "provider-dead")
	mustExec(t, db, `UPDATE accounts SET connection_state = 'connected', health_state = 'healthy' WHERE id = 'acct-live'`)
	mustExec(t, db, `UPDATE accounts SET connection_state = 'connected', health_state = 'expired' WHERE id = 'acct-expired'`)
	mustExec(t, db, `UPDATE accounts SET connection_state = 'connected', health_state = 'healthy', reauth_in_progress = 1 WHERE id = 'acct-reauth'`)

	insertModelFull(t, db, "model-live", "key-live", "Live Model", nil, nil, nil)
	insertModelFull(t, db, "model-dead", "key-dead", "Dead Model", nil, nil, nil)
	insertModelFull(t, db, "model-withdrawn", "key-withdrawn", "Withdrawn Model", nil, nil, nil)
	insertModelFull(t, db, "model-reauth", "key-reauth", "Reauth Model", nil, nil, nil)

	insertOfferingFull(t, db, "acct-live", "provider-live", "live-1", "model-live", nil, nil, nil, nil, nil, 0, 0)
	insertOfferingFull(t, db, "acct-expired", "provider-dead", "dead-1", "model-dead", nil, nil, nil, nil, nil, 0, 0)
	insertOfferingFull(t, db, "acct-live", "provider-live", "withdrawn-1", "model-withdrawn", nil, nil, nil, nil, nil, 0, 0)
	mustExec(t, db, `UPDATE account_model_offerings SET availability = 'withdrawn' WHERE account_id = 'acct-live' AND provider_model_id = 'withdrawn-1'`)
	insertOfferingFull(t, db, "acct-reauth", "provider-dead", "reauth-1", "model-reauth", nil, nil, nil, nil, nil, 0, 0)

	for _, row := range []struct{ providerID, providerModelID, modelID string }{
		{"provider-live", "live-1", "model-live"},
		{"provider-dead", "dead-1", "model-dead"},
		{"provider-live", "withdrawn-1", "model-withdrawn"},
		{"provider-dead", "reauth-1", "model-reauth"},
	} {
		mustExec(t, db, `INSERT INTO provider_model_aliases (provider_id, provider_model_id, model_id) VALUES (?, ?, ?)`, row.providerID, row.providerModelID, row.modelID)
	}
	insertOfferingOperationFull(t, db, "op-live", "acct-live", "provider-live", "live-1", "chat", "certified", "supported", 1, nil, "")
	insertOfferingOperationFull(t, db, "op-dead", "acct-expired", "provider-dead", "dead-1", "chat", "certified", "supported", 1, nil, "")

	got, err := NewModelLifecycleRepo(db).PurgeInactive(context.Background())
	if err != nil {
		t.Fatalf("PurgeInactive() error = %v", err)
	}
	if got.OfferingsDeleted != 3 || got.AliasesDeleted != 3 || got.ModelsDeleted != 3 {
		t.Fatalf("PurgeInactive() = %+v, want 3 offerings, 3 aliases, 3 models deleted", got)
	}

	if n := countWhere(t, db, "account_model_offerings", "1 = 1"); n != 1 {
		t.Fatalf("offerings left = %d, want only the live offering", n)
	}
	if n := countWhere(t, db, "account_model_offerings", "account_id = 'acct-live' AND provider_model_id = 'live-1'"); n != 1 {
		t.Fatal("the healthy account's available offering was removed")
	}
	if n := countWhere(t, db, "provider_model_aliases", "1 = 1"); n != 1 {
		t.Fatalf("aliases left = %d, want only the live alias", n)
	}
	if n := countWhere(t, db, "models", "1 = 1"); n != 1 {
		t.Fatalf("models left = %d, want only the live canonical model", n)
	}
	if n := countWhere(t, db, "offering_operations", "id = 'op-dead'"); n != 0 {
		t.Fatal("dead offering operation survived the offering cascade")
	}
	if n := countWhere(t, db, "certifications", "offering_operation_id = 'op-dead'"); n != 0 {
		t.Fatal("dead offering certification survived the offering cascade")
	}
}

func TestModelLifecycleRepo_PurgeInactiveIsIdempotent(t *testing.T) {
	db := migratedCatalogRepoDB(t)
	repo := NewModelLifecycleRepo(db)

	first, err := repo.PurgeInactive(context.Background())
	if err != nil {
		t.Fatalf("first PurgeInactive() error = %v", err)
	}
	second, err := repo.PurgeInactive(context.Background())
	if err != nil {
		t.Fatalf("second PurgeInactive() error = %v", err)
	}
	if first != (ModelLifecyclePurgeResult{}) || second != (ModelLifecyclePurgeResult{}) {
		t.Fatalf("empty purge results = first:%+v second:%+v, want zero values", first, second)
	}
}

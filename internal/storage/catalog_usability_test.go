package storage

import (
	"context"
	"testing"
)

// TestCatalogRepo_ListChatOfferingsToVerify_ObservedChatOpsOnly proves the
// usability-verification lister returns exactly the account's chat
// offering-operations whose certification is still `observed` (the ones a
// usability probe should run against) — never a non-chat operation, never a
// chat op already past observed, and never another account's rows.
func TestCatalogRepo_ListChatOfferingsToVerify_ObservedChatOpsOnly(t *testing.T) {
	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-1")
	insertAccount(t, db, "acct-1", "prov-1")
	insertAccount(t, db, "acct-2", "prov-1")
	insertModelFull(t, db, "model-free", "ck-free", "Free Model", nil, nil, nil)
	insertModelFull(t, db, "model-two", "ck-two", "Second Model", nil, nil, nil)

	// acct-1 offerings.
	insertOfferingFull(t, db, "acct-1", "prov-1", "big-pickle", "model-free", nil, nil, nil, nil, nil, 0, 0)
	insertOfferingFull(t, db, "acct-1", "prov-1", "gpt-5.5-pro", "model-two", nil, nil, nil, nil, nil, 0, 0)
	// acct-2 offering (must never appear in an acct-1 query).
	insertOfferingFull(t, db, "acct-2", "prov-1", "big-pickle", "model-free", nil, nil, nil, nil, nil, 0, 0)

	// The one that MUST be returned: an observed chat op.
	insertOfferingOperationFull(t, db, "op-observed-chat", "acct-1", "prov-1", "big-pickle", "chat", "observed", "unknown", 0, nil, "")
	// Excluded: a chat op already certified (not awaiting a probe).
	insertOfferingOperationFull(t, db, "op-certified-chat", "acct-1", "prov-1", "gpt-5.5-pro", "chat", "certified", "supported", 1, nil, "")
	// Excluded: an observed op for a non-chat operation.
	insertOfferingOperationFull(t, db, "op-observed-tools", "acct-1", "prov-1", "big-pickle", "tools", "observed", "unknown", 0, nil, "")
	// Excluded: another account's observed chat op.
	insertOfferingOperationFull(t, db, "op-other-account", "acct-2", "prov-1", "big-pickle", "chat", "observed", "unknown", 0, nil, "")

	repo := NewCatalogRepo(db)
	got, err := repo.ListChatOfferingsToVerify(context.Background(), "acct-1")
	if err != nil {
		t.Fatalf("ListChatOfferingsToVerify() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("returned %d offerings, want 1; got %+v", len(got), got)
	}
	if got[0].OfferingOperationID != "op-observed-chat" || got[0].ProviderModelID != "big-pickle" {
		t.Fatalf("returned %+v, want op-observed-chat/big-pickle", got[0])
	}
}

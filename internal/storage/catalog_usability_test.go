package storage

import (
	"context"
	"testing"
)

// TestCatalogRepo_ListChatOfferingsToVerify_ProbingChatOpsOnly proves the
// usability-verification lister returns exactly the account's chat
// offering-operations whose certification is in `probing` (where the existing
// drainer strands them and where this sweep must execute) — never a non-chat
// operation, never an `observed` chat op (that is the drainer's job), never a
// terminal-state chat op, and never another account's rows.
func TestCatalogRepo_ListChatOfferingsToVerify_ProbingChatOpsOnly(t *testing.T) {
	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-1")
	insertAccount(t, db, "acct-1", "prov-1")
	insertAccount(t, db, "acct-2", "prov-1")
	insertModelFull(t, db, "model-free", "ck-free", "Free Model", nil, nil, nil)
	insertModelFull(t, db, "model-two", "ck-two", "Second Model", nil, nil, nil)
	insertModelFull(t, db, "model-obs", "ck-obs", "Observed Model", nil, nil, nil)

	insertOfferingFull(t, db, "acct-1", "prov-1", "big-pickle", "model-free", nil, nil, nil, nil, nil, 0, 0)
	insertOfferingFull(t, db, "acct-1", "prov-1", "gpt-5.5-pro", "model-two", nil, nil, nil, nil, nil, 0, 0)
	insertOfferingFull(t, db, "acct-1", "prov-1", "deepseek-free", "model-obs", nil, nil, nil, nil, nil, 0, 0)
	insertOfferingFull(t, db, "acct-2", "prov-1", "big-pickle", "model-free", nil, nil, nil, nil, nil, 0, 0)

	// The one that MUST be returned: a chat op stranded in probing.
	insertOfferingOperationFull(t, db, "op-probing-chat", "acct-1", "prov-1", "big-pickle", "chat", "probing", "unknown", 0, nil, "")
	// Excluded: an observed chat op — the drainer moves it to probing first.
	insertOfferingOperationFull(t, db, "op-observed-chat", "acct-1", "prov-1", "deepseek-free", "chat", "observed", "unknown", 0, nil, "")
	// Excluded: a chat op already certified.
	insertOfferingOperationFull(t, db, "op-certified-chat", "acct-1", "prov-1", "gpt-5.5-pro", "chat", "certified", "supported", 1, nil, "")
	// Excluded: a probing op for a non-chat operation.
	insertOfferingOperationFull(t, db, "op-probing-tools", "acct-1", "prov-1", "big-pickle", "tools", "probing", "unknown", 0, nil, "")
	// Excluded: another account's probing chat op.
	insertOfferingOperationFull(t, db, "op-other-account", "acct-2", "prov-1", "big-pickle", "chat", "probing", "unknown", 0, nil, "")

	repo := NewCatalogRepo(db)
	got, err := repo.ListChatOfferingsToVerify(context.Background(), "acct-1")
	if err != nil {
		t.Fatalf("ListChatOfferingsToVerify() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("returned %d offerings, want 1; got %+v", len(got), got)
	}
	if got[0].OfferingOperationID != "op-probing-chat" || got[0].ProviderModelID != "big-pickle" {
		t.Fatalf("returned %+v, want op-probing-chat/big-pickle", got[0])
	}
}

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

// TestCatalogRepo_ListObservedChatOfferings_ObservedChatOpsOnly proves the
// FAST-LANE lister returns exactly the account's chat offering-operations whose
// certification is still `observed` — the rows discovery just seeded, which the
// fast lane must drive across the observed -> probing edge itself before it can
// probe anything. It is the exact complement of
// ListChatOfferingsToVerify: never a non-chat operation, never a chat op
// already in `probing` (that lister owns those), never a terminal-state row,
// and never another account's rows.
func TestCatalogRepo_ListObservedChatOfferings_ObservedChatOpsOnly(t *testing.T) {
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

	// The one that MUST be returned: a freshly discovered chat op at `observed`.
	insertOfferingOperationFull(t, db, "op-observed-chat", "acct-1", "prov-1", "deepseek-free", "chat", "observed", "unknown", 0, nil, "")
	// Excluded: a chat op ALREADY in probing — ListChatOfferingsToVerify's row.
	insertOfferingOperationFull(t, db, "op-probing-chat", "acct-1", "prov-1", "big-pickle", "chat", "probing", "unknown", 0, nil, "")
	// Excluded: a chat op already certified.
	insertOfferingOperationFull(t, db, "op-certified-chat", "acct-1", "prov-1", "gpt-5.5-pro", "chat", "certified", "supported", 1, nil, "")
	// Excluded: an observed op for a non-chat operation.
	insertOfferingOperationFull(t, db, "op-observed-tools", "acct-1", "prov-1", "big-pickle", "tools", "observed", "unknown", 0, nil, "")
	// Excluded: another account's observed chat op.
	insertOfferingOperationFull(t, db, "op-other-account", "acct-2", "prov-1", "big-pickle", "chat", "observed", "unknown", 0, nil, "")

	repo := NewCatalogRepo(db)
	got, err := repo.ListObservedChatOfferings(context.Background(), "acct-1")
	if err != nil {
		t.Fatalf("ListObservedChatOfferings() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("returned %d offerings, want 1; got %+v", len(got), got)
	}
	if got[0].OfferingOperationID != "op-observed-chat" || got[0].ProviderModelID != "deepseek-free" {
		t.Fatalf("returned %+v, want op-observed-chat/deepseek-free", got[0])
	}
}

// TestCatalogRepo_ListNonChatOperationsToCertify_ProbingNonChatOpsOnly proves
// the declaration-certification lister returns exactly the account's NON-chat
// offering-operations stranded in `probing` — never chat (that has its own
// runtime prober), never an `observed` op (drainer's job), never a terminal
// row, and never another account's rows.
func TestCatalogRepo_ListNonChatOperationsToCertify_ProbingNonChatOpsOnly(t *testing.T) {
	db := migratedCatalogRepoDB(t)
	insertProvider(t, db, "prov-1")
	insertAccount(t, db, "acct-1", "prov-1")
	insertAccount(t, db, "acct-2", "prov-1")
	insertModelFull(t, db, "model-free", "ck-free", "Free Model", nil, nil, nil)
	insertModelFull(t, db, "model-two", "ck-two", "Second Model", nil, nil, nil)

	insertOfferingFull(t, db, "acct-1", "prov-1", "big-pickle", "model-free", nil, nil, nil, nil, nil, 0, 0)
	insertOfferingFull(t, db, "acct-1", "prov-1", "mimo-free", "model-two", nil, nil, nil, nil, nil, 0, 0)
	insertOfferingFull(t, db, "acct-2", "prov-1", "big-pickle", "model-free", nil, nil, nil, nil, nil, 0, 0)

	// MUST be returned: non-chat ops stranded in probing.
	insertOfferingOperationFull(t, db, "op-tools", "acct-1", "prov-1", "big-pickle", "tools", "probing", "unknown", 0, nil, "")
	insertOfferingOperationFull(t, db, "op-vision", "acct-1", "prov-1", "mimo-free", "vision", "probing", "unknown", 0, nil, "")
	// Excluded: a chat op in probing (that is the runtime prober's job, not declaration).
	insertOfferingOperationFull(t, db, "op-chat", "acct-1", "prov-1", "big-pickle", "chat", "probing", "unknown", 0, nil, "")
	// Excluded: an observed non-chat op — the drainer moves it to probing first.
	insertOfferingOperationFull(t, db, "op-observed-tools", "acct-1", "prov-1", "mimo-free", "tools", "observed", "unknown", 0, nil, "")
	// Excluded: a non-chat op already certified.
	insertOfferingOperationFull(t, db, "op-certified-vision", "acct-1", "prov-1", "big-pickle", "vision", "certified", "supported", 1, nil, "")
	// Excluded: another account's probing non-chat op.
	insertOfferingOperationFull(t, db, "op-other-account", "acct-2", "prov-1", "big-pickle", "tools", "probing", "unknown", 0, nil, "")

	repo := NewCatalogRepo(db)
	got, err := repo.ListNonChatOperationsToCertify(context.Background(), "acct-1")
	if err != nil {
		t.Fatalf("ListNonChatOperationsToCertify() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("returned %d ops, want 2; got %+v", len(got), got)
	}
	seen := map[string]string{}
	for _, v := range got {
		seen[v.OfferingOperationID] = v.Operation
	}
	if seen["op-tools"] != "tools" || seen["op-vision"] != "vision" {
		t.Fatalf("returned %+v, want op-tools/tools + op-vision/vision", got)
	}
}

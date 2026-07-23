package secrets

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// testCiphertextStore is a REAL-SQLite-backed secrets.CiphertextStore
// implementation, used only by this package's tests. The production,
// credential-table-backed implementation lands in P2b once M2's columns
// exist; this test proves the rotation mechanism (atomic re-wrap, crash
// recovery) against genuine SQLite transactions in the meantime.
type testCiphertextStore struct {
	db *storage.DB

	mu          sync.Mutex
	failRowID   string // if set, RewrapAll fails when it reaches this row id
	rewrapCalls int    // counts RewrapAll invocations, for idempotency assertions
}

func newTestCiphertextStore(t *testing.T, db *storage.DB) *testCiphertextStore {
	t.Helper()
	_, err := db.Conn().Exec(`
		CREATE TABLE IF NOT EXISTS test_ciphertext_rows (
			id         TEXT PRIMARY KEY,
			purpose    TEXT NOT NULL,
			provider   TEXT NOT NULL,
			account    TEXT NOT NULL,
			record     TEXT NOT NULL,
			kind       TEXT NOT NULL,
			key_id     TEXT NOT NULL,
			nonce      BLOB NOT NULL,
			ciphertext BLOB NOT NULL
		)
	`)
	if err != nil {
		t.Fatalf("create test_ciphertext_rows: %v", err)
	}
	return &testCiphertextStore{db: db}
}

func (s *testCiphertextStore) seed(t *testing.T, id string, identity RecordIdentity, env Envelope) {
	t.Helper()
	_, err := s.db.Conn().Exec(`
		INSERT INTO test_ciphertext_rows (id, purpose, provider, account, record, kind, key_id, nonce, ciphertext)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, identity.Purpose, identity.Provider, identity.Account, identity.Record, identity.Kind,
		env.KeyID, env.Nonce, env.Ciphertext)
	if err != nil {
		t.Fatalf("seed row %q: %v", id, err)
	}
}

func (s *testCiphertextStore) rowsUnderKey(t *testing.T, keyID string) int {
	t.Helper()
	var n int
	if err := s.db.Conn().QueryRow(`SELECT COUNT(*) FROM test_ciphertext_rows WHERE key_id = ?`, keyID).Scan(&n); err != nil {
		t.Fatalf("count rows under %q: %v", keyID, err)
	}
	return n
}

func (s *testCiphertextStore) load(t *testing.T, id string) (RecordIdentity, Envelope) {
	t.Helper()
	var identity RecordIdentity
	var env Envelope
	err := s.db.Conn().QueryRow(`
		SELECT purpose, provider, account, record, kind, key_id, nonce, ciphertext
		FROM test_ciphertext_rows WHERE id = ?
	`, id).Scan(&identity.Purpose, &identity.Provider, &identity.Account, &identity.Record, &identity.Kind,
		&env.KeyID, &env.Nonce, &env.Ciphertext)
	if err != nil {
		t.Fatalf("load row %q: %v", id, err)
	}
	return identity, env
}

// RewrapAll implements secrets.CiphertextStore over the real, single
// database/sql handle storage.DB exposes: it runs the entire
// select-then-update batch inside one transaction, so a failure at any
// point (including the injected failRowID fault) rolls every row in the
// batch back to exactly its pre-call state — proving the "single SQL
// transaction" atomicity the card requires against real SQLite, not a
// pure in-memory fake.
func (s *testCiphertextStore) RewrapAll(ctx context.Context, fromKeyID string, rewrap func(RewrapRow) (Envelope, error)) error {
	s.mu.Lock()
	s.rewrapCalls++
	s.mu.Unlock()

	tx, err := s.db.Conn().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit succeeds

	rows, err := tx.QueryContext(ctx, `
		SELECT id, purpose, provider, account, record, kind, nonce, ciphertext
		FROM test_ciphertext_rows WHERE key_id = ?
	`, fromKeyID)
	if err != nil {
		return err
	}

	type batchRow struct {
		id       string
		identity RecordIdentity
		nonce    []byte
		cipher   []byte
	}
	var batch []batchRow
	for rows.Next() {
		var r batchRow
		if err := rows.Scan(&r.id, &r.identity.Purpose, &r.identity.Provider, &r.identity.Account,
			&r.identity.Record, &r.identity.Kind, &r.nonce, &r.cipher); err != nil {
			_ = rows.Close()
			return err
		}
		batch = append(batch, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()

	for _, r := range batch {
		if s.failRowID != "" && r.id == s.failRowID {
			return errors.New("secrets_test: simulated store failure on row " + r.id)
		}

		newEnv, err := rewrap(RewrapRow{
			ID:       r.id,
			Identity: r.identity,
			Envelope: Envelope{KeyID: fromKeyID, Nonce: r.nonce, Ciphertext: r.cipher},
		})
		if err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE test_ciphertext_rows SET key_id = ?, nonce = ?, ciphertext = ? WHERE id = ?
		`, newEnv.KeyID, newEnv.Nonce, newEnv.Ciphertext, r.id); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func openTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// seedRows creates n rows, each with a distinct RecordIdentity and
// plaintext, encrypted under kr's current active key. It returns the
// row ids and the original plaintexts, keyed by row id, for later
// decrypt-and-compare assertions.
func seedRows(t *testing.T, store *testCiphertextStore, kr *Keyring, n int) (ids []string, plaintexts map[string][]byte, identities map[string]RecordIdentity) {
	t.Helper()
	plaintexts = make(map[string][]byte, n)
	identities = make(map[string]RecordIdentity, n)
	for i := 0; i < n; i++ {
		id := "row-" + string(rune('a'+i))
		identity := RecordIdentity{
			Purpose:  "test",
			Provider: "provider",
			Account:  "account",
			Record:   id,
			Kind:     "kind",
		}
		plaintext := []byte("secret-plaintext-" + string(rune('a'+i)))

		env, err := Encrypt(kr, identity, plaintext)
		if err != nil {
			t.Fatalf("seed Encrypt() error = %v", err)
		}
		store.seed(t, id, identity, env)

		ids = append(ids, id)
		plaintexts[id] = plaintext
		identities[id] = identity
	}
	return ids, plaintexts, identities
}

func TestRotate_AtomicRewrap_AllRowsMoveToNewKey(t *testing.T) {
	dir := t.TempDir()
	secretsDir := dir + "/secrets"

	kr, err := Load(dir, "", false)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	oldKeyID := kr.ActiveKeyID

	db := openTestDB(t)
	store := newTestCiphertextStore(t, db)
	ids, plaintexts, identities := seedRows(t, store, kr, 5)

	holder := NewKeyringHolder(kr)
	if err := holder.Rotate(context.Background(), secretsDir, store); err != nil {
		t.Fatalf("Rotate() error = %v", err)
	}

	newKr := holder.Get()
	if newKr.ActiveKeyID == oldKeyID {
		t.Fatalf("ActiveKeyID did not change from %q", oldKeyID)
	}
	if newKr.PendingRotation != nil {
		t.Fatalf("PendingRotation = %+v, want nil after a successful Rotate", newKr.PendingRotation)
	}
	if _, ok := newKr.Keys[oldKeyID]; !ok {
		t.Fatalf("old key %q was removed from the keyring, want retained", oldKeyID)
	}

	if n := store.rowsUnderKey(t, oldKeyID); n != 0 {
		t.Fatalf("%d rows remain under old key %q, want 0", n, oldKeyID)
	}
	if n := store.rowsUnderKey(t, newKr.ActiveKeyID); n != len(ids) {
		t.Fatalf("%d rows under new key, want %d", n, len(ids))
	}

	for _, id := range ids {
		identity, env := store.load(t, id)
		if identity != identities[id] {
			t.Fatalf("row %q identity changed: got %+v, want %+v", id, identity, identities[id])
		}
		got, err := Decrypt(newKr, identity, env)
		if err != nil {
			t.Fatalf("Decrypt(row %q) error = %v", id, err)
		}
		if string(got) != string(plaintexts[id]) {
			t.Fatalf("row %q decrypted plaintext = %q, want %q", id, got, plaintexts[id])
		}
	}

	reloaded, err := Load(dir, "", false)
	if err != nil {
		t.Fatalf("reload Load() error = %v", err)
	}
	if reloaded.ActiveKeyID != newKr.ActiveKeyID {
		t.Fatalf("reloaded ActiveKeyID = %q, want %q", reloaded.ActiveKeyID, newKr.ActiveKeyID)
	}
	if _, ok := reloaded.Keys[oldKeyID]; !ok {
		t.Fatalf("reloaded keyring lost old key %q", oldKeyID)
	}
	if _, ok := reloaded.Keys[newKr.ActiveKeyID]; !ok {
		t.Fatalf("reloaded keyring lost new key %q", newKr.ActiveKeyID)
	}
	if reloaded.PendingRotation != nil {
		t.Fatalf("reloaded PendingRotation = %+v, want nil", reloaded.PendingRotation)
	}
}

func TestRotate_BatchFailurePartway_RollsBackEntirely(t *testing.T) {
	dir := t.TempDir()
	secretsDir := dir + "/secrets"

	kr, err := Load(dir, "", false)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	oldKeyID := kr.ActiveKeyID

	db := openTestDB(t)
	store := newTestCiphertextStore(t, db)
	ids, plaintexts, identities := seedRows(t, store, kr, 4)
	store.failRowID = ids[2] // fail after rows 0,1 would have been staged

	holder := NewKeyringHolder(kr)
	err = holder.Rotate(context.Background(), secretsDir, store)
	if err == nil {
		t.Fatalf("Rotate() error = nil, want a failure from the injected store fault")
	}

	if n := store.rowsUnderKey(t, oldKeyID); n != len(ids) {
		t.Fatalf("%d rows remain under old key %q after rollback, want all %d", n, oldKeyID, len(ids))
	}

	for _, id := range ids {
		identity, env := store.load(t, id)
		if identity != identities[id] {
			t.Fatalf("row %q identity changed after rollback", id)
		}
		if env.KeyID != oldKeyID {
			t.Fatalf("row %q key_id = %q after rollback, want unchanged %q", id, env.KeyID, oldKeyID)
		}
		got, err := Decrypt(kr, identity, env)
		if err != nil {
			t.Fatalf("Decrypt(row %q) after rollback error = %v", id, err)
		}
		if string(got) != string(plaintexts[id]) {
			t.Fatalf("row %q plaintext after rollback = %q, want %q", id, got, plaintexts[id])
		}
	}

	pending := holder.Get().PendingRotation
	if pending == nil {
		t.Fatalf("PendingRotation is nil after a failed Rotate, want set (resumable)")
	}
	if pending.FromKeyID != oldKeyID {
		t.Fatalf("PendingRotation.FromKeyID = %q, want %q", pending.FromKeyID, oldKeyID)
	}
	if _, ok := holder.Get().Keys[pending.ToKeyID]; !ok {
		t.Fatalf("PendingRotation.ToKeyID %q not present in keyring", pending.ToKeyID)
	}
}

func TestRotate_CrashMidRotation_ResumeCompletesIdempotently(t *testing.T) {
	dir := t.TempDir()
	secretsDir := dir + "/secrets"

	kr, err := Load(dir, "", false)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	oldKeyID := kr.ActiveKeyID

	db := openTestDB(t)
	store := newTestCiphertextStore(t, db)
	ids, plaintexts, identities := seedRows(t, store, kr, 3)
	store.failRowID = ids[1] // force Rotate to fail, leaving a pending marker on disk

	holder := NewKeyringHolder(kr)
	if err := holder.Rotate(context.Background(), secretsDir, store); err == nil {
		t.Fatalf("Rotate() error = nil, want the injected failure")
	}
	newKeyID := holder.Get().ActiveKeyID

	// Every row must still be decryptable at this point — the DB
	// transaction rolled back, so nothing is under the new key yet, and
	// the old key remains in the keyring and usable.
	for _, id := range ids {
		identity, env := store.load(t, id)
		if _, err := Decrypt(kr, identity, env); err != nil {
			t.Fatalf("row %q not decryptable immediately after the failed Rotate: %v", id, err)
		}
	}

	// Simulate a crash: drop the in-memory holder/keyring entirely and
	// reload from disk, exactly as a restarted process would.
	reloaded, err := Load(dir, "", false)
	if err != nil {
		t.Fatalf("post-crash reload Load() error = %v", err)
	}
	if reloaded.PendingRotation == nil {
		t.Fatalf("reloaded keyring has no PendingRotation, want one referencing the interrupted rotation")
	}
	if reloaded.PendingRotation.FromKeyID != oldKeyID || reloaded.PendingRotation.ToKeyID != newKeyID {
		t.Fatalf("reloaded PendingRotation = %+v, want {%s %s}", reloaded.PendingRotation, oldKeyID, newKeyID)
	}

	// Retry, this time without the injected fault.
	store.failRowID = ""
	resumeHolder := NewKeyringHolder(reloaded)
	if err := resumeHolder.Resume(context.Background(), secretsDir, store); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}

	finalKr := resumeHolder.Get()
	if finalKr.PendingRotation != nil {
		t.Fatalf("PendingRotation = %+v after Resume, want nil", finalKr.PendingRotation)
	}
	if finalKr.ActiveKeyID != newKeyID {
		t.Fatalf("ActiveKeyID = %q after Resume, want %q", finalKr.ActiveKeyID, newKeyID)
	}
	if n := store.rowsUnderKey(t, oldKeyID); n != 0 {
		t.Fatalf("%d rows remain under old key after Resume, want 0", n)
	}
	if n := store.rowsUnderKey(t, newKeyID); n != len(ids) {
		t.Fatalf("%d rows under new key after Resume, want %d", n, len(ids))
	}
	for _, id := range ids {
		identity, env := store.load(t, id)
		if identity != identities[id] {
			t.Fatalf("row %q identity changed across resume", id)
		}
		got, err := Decrypt(finalKr, identity, env)
		if err != nil {
			t.Fatalf("Decrypt(row %q) after Resume error = %v", id, err)
		}
		if string(got) != string(plaintexts[id]) {
			t.Fatalf("row %q plaintext after Resume = %q, want %q", id, got, plaintexts[id])
		}
	}

	callsBeforeSecondResume := store.rewrapCalls
	if err := resumeHolder.Resume(context.Background(), secretsDir, store); err != nil {
		t.Fatalf("second Resume() error = %v, want idempotent no-op success", err)
	}
	if store.rewrapCalls != callsBeforeSecondResume {
		t.Fatalf("second Resume() invoked the store %d more time(s), want a pure no-op (0 extra calls)",
			store.rewrapCalls-callsBeforeSecondResume)
	}
	if resumeHolder.Get().PendingRotation != nil {
		t.Fatalf("PendingRotation set after idempotent second Resume, want nil")
	}
}

func TestRotate_RefusesWhileAPriorRotationIsPending(t *testing.T) {
	dir := t.TempDir()
	secretsDir := dir + "/secrets"

	kr, err := Load(dir, "", false)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	db := openTestDB(t)
	store := newTestCiphertextStore(t, db)
	ids, _, _ := seedRows(t, store, kr, 2)
	store.failRowID = ids[1]

	holder := NewKeyringHolder(kr)
	if err := holder.Rotate(context.Background(), secretsDir, store); err == nil {
		t.Fatalf("Rotate() error = nil, want the injected failure to leave a pending rotation")
	}

	err = holder.Rotate(context.Background(), secretsDir, store)
	if !errors.Is(err, ErrRotationInProgress) {
		t.Fatalf("second Rotate() error = %v, want ErrRotationInProgress", err)
	}
}

func TestKeyringHolder_ConcurrentEncryptDecryptDuringRotate(t *testing.T) {
	dir := t.TempDir()
	secretsDir := dir + "/secrets"

	kr, err := Load(dir, "", false)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	db := openTestDB(t)
	store := newTestCiphertextStore(t, db)
	_, _, _ = seedRows(t, store, kr, 3)

	holder := NewKeyringHolder(kr)
	identity := RecordIdentity{Purpose: "p", Provider: "pr", Account: "a", Record: "r", Kind: "k"}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	errs := make(chan error, 64)

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := holder.Get()
				env, err := Encrypt(snap, identity, []byte("hello"))
				if err != nil {
					errs <- err
					return
				}
				if _, err := Decrypt(snap, identity, env); err != nil {
					errs <- err
					return
				}
			}
		}()
	}

	if err := holder.Rotate(context.Background(), secretsDir, store); err != nil {
		close(stop)
		wg.Wait()
		t.Fatalf("Rotate() error = %v", err)
	}
	close(stop)
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent Encrypt/Decrypt during Rotate failed: %v", err)
	}
}

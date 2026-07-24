package application_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// fakeOAuthAdapter is a deterministic, in-memory providers.OAuthAdapter —
// no real network call, no real provider — used to prove
// OAuthEnrollmentService's own framework behavior (PKCE, replay-safety,
// expiry, provider-mismatch) independent of any concrete provider
// integration (there is no real OAuth adapter yet this phase; antigravity
// OAuth is a separate future unit).
type fakeOAuthAdapter struct {
	authorizeURLPrefix string
	identity           providers.IdentityResult
	creds              providers.StoredCredentials
	beginErr           error
	completeErr        error

	beginCalls    int32
	completeCalls int32

	lastRedirectURI, lastState, lastChallenge       string
	lastCode, lastVerifier, lastCompleteRedirectURI string
}

func newFakeOAuthAdapter() *fakeOAuthAdapter {
	return &fakeOAuthAdapter{
		authorizeURLPrefix: "https://fake-provider.example/authorize?state=",
		identity:           providers.IdentityResult{ExternalID: "ext-1", Email: "owner@example.com", Plan: "pro", Funding: "free"},
		creds:              providers.StoredCredentials{Value: "fake-access-token-0123456789"},
	}
}

func (f *fakeOAuthAdapter) BeginOAuth(_ context.Context, redirectURI, state, pkceChallenge string) (string, error) {
	atomic.AddInt32(&f.beginCalls, 1)
	f.lastRedirectURI, f.lastState, f.lastChallenge = redirectURI, state, pkceChallenge
	if f.beginErr != nil {
		return "", f.beginErr
	}
	return f.authorizeURLPrefix + state, nil
}

func (f *fakeOAuthAdapter) CompleteOAuth(_ context.Context, code, pkceVerifier, redirectURI string) (providers.IdentityResult, providers.StoredCredentials, error) {
	atomic.AddInt32(&f.completeCalls, 1)
	f.lastCode, f.lastVerifier, f.lastCompleteRedirectURI = code, pkceVerifier, redirectURI
	if f.completeErr != nil {
		return providers.IdentityResult{}, providers.StoredCredentials{}, f.completeErr
	}
	return f.identity, f.creds, nil
}

func (f *fakeOAuthAdapter) RefreshCredentials(_ context.Context, creds providers.StoredCredentials) (providers.StoredCredentials, error) {
	return creds, nil
}

func (f *fakeOAuthAdapter) getCompleteCalls() int32 { return atomic.LoadInt32(&f.completeCalls) }

func newOAuthTestService(t *testing.T, db *storage.DB, now func() time.Time) (*application.OAuthEnrollmentService, application.IDGenerator) {
	t.Helper()
	newID := sequentialIDGenerator("oauth-id")
	svc := application.NewOAuthEnrollmentService(
		storage.NewOAuthTransactionRepo(db),
		storage.NewEnrollmentRepo(db),
		storage.NewAccountRepo(db),
		newTestKeyring(t),
		newID,
		now,
	)
	return svc, newID
}

func rawOAuthTxRow(t *testing.T, db *storage.DB, stateHash string) (providerID, transactionID, keyID string, nonce, ciphertext []byte, expiresAt, createdAt int64, consumed int, ok bool) {
	t.Helper()
	err := db.Conn().QueryRow(
		`SELECT provider_id, transaction_id, key_id, nonce, ciphertext, expires_at, created_at, consumed FROM oauth_transactions WHERE state_sha256 = ?`,
		stateHash,
	).Scan(&providerID, &transactionID, &keyID, &nonce, &ciphertext, &expiresAt, &createdAt, &consumed)
	if err != nil {
		return "", "", "", nil, nil, 0, 0, 0, false
	}
	return providerID, transactionID, keyID, nonce, ciphertext, expiresAt, createdAt, consumed, true
}

func stateHashOf(state string) string {
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:])
}

// TestOAuthService_BeginThenComplete_CreatesExactlyOneAccountCredentialFunding
// is the happy-path proof: Begin persists a pending transaction and calls
// the adapter's BeginOAuth with a fresh state/PKCE challenge; Complete
// consumes it, calls CompleteOAuth with the correct verifier, and
// atomically creates exactly one account/credential/funding row.
func TestOAuthService_BeginThenComplete_CreatesExactlyOneAccountCredentialFunding(t *testing.T) {
	db := migratedDB(t)
	seedProvider(t, db, "fake-oauth")

	adapter := newFakeOAuthAdapter()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc, _ := newOAuthTestService(t, db, func() time.Time { return now })

	beginResult, err := svc.Begin(context.Background(), application.BeginOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RedirectURI: "http://127.0.0.1:8081/api/control/v1/oauth/fake-oauth/callback",
	})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if adapter.beginCalls != 1 {
		t.Fatalf("adapter.BeginOAuth called %d times, want 1", adapter.beginCalls)
	}
	if adapter.lastState == "" || adapter.lastChallenge == "" {
		t.Fatalf("adapter received empty state/challenge: state=%q challenge=%q", adapter.lastState, adapter.lastChallenge)
	}
	assertCount(t, db, "oauth_transactions", 1)

	txID, account, err := svc.Complete(context.Background(), application.CompleteOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RawState: adapter.lastState, Code: "fake-auth-code",
		RedirectURI: "http://127.0.0.1:8081/api/control/v1/oauth/fake-oauth/callback",
		FundingMode: domain.FundingModeOwnerPolicy,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if txID != beginResult.TransactionID {
		t.Fatalf("Complete returned transactionID %q, want %q (from Begin)", txID, beginResult.TransactionID)
	}
	if adapter.getCompleteCalls() != 1 {
		t.Fatalf("adapter.CompleteOAuth called %d times, want 1", adapter.getCompleteCalls())
	}
	if adapter.lastVerifier == "" {
		t.Fatalf("adapter.CompleteOAuth received an empty verifier")
	}
	if account.ConnectionState != domain.ConnectionConnected || account.AuthType != "oauth2" {
		t.Fatalf("account = %+v, want connected/oauth2", account)
	}

	assertCount(t, db, "accounts", 1)
	assertCount(t, db, "account_credentials", 1)
	assertCount(t, db, "account_funding_evidence", 1)

	var kind string
	if err := db.Conn().QueryRow(`SELECT kind FROM account_credentials WHERE account_id = ?`, account.ID).Scan(&kind); err != nil {
		t.Fatalf("query credential kind: %v", err)
	}
	if kind != string(domain.CredentialKindOAuth2) {
		t.Fatalf("credential kind = %q, want oauth2", kind)
	}
}

// TestOAuthService_UnknownState_RejectedNoAdapterExchange is the
// state-nonce-verification invariant: a callback whose state hashes to
// no stored transaction is rejected, and the adapter's CompleteOAuth is
// never invoked for it.
func TestOAuthService_UnknownState_RejectedNoAdapterExchange(t *testing.T) {
	db := migratedDB(t)
	seedProvider(t, db, "fake-oauth")

	adapter := newFakeOAuthAdapter()
	svc, _ := newOAuthTestService(t, db, nil)

	_, _, err := svc.Complete(context.Background(), application.CompleteOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RawState: "never-began-this-state", Code: "any-code",
		RedirectURI: "http://127.0.0.1:8081/callback", FundingMode: domain.FundingModeOwnerPolicy,
	})
	if !errors.Is(err, application.ErrOAuthTransactionInvalid) {
		t.Fatalf("error = %v, want ErrOAuthTransactionInvalid", err)
	}
	if adapter.getCompleteCalls() != 0 {
		t.Fatalf("adapter.CompleteOAuth called %d times, want 0 (state never matched any transaction)", adapter.getCompleteCalls())
	}
}

// TestOAuthService_RawStateNeverStored is the "raw state never appears in
// any DB column" canary: only its sha256 (the primary key itself) may
// ever match — the raw value must not leak into any other column either.
func TestOAuthService_RawStateNeverStored(t *testing.T) {
	db := migratedDB(t)
	seedProvider(t, db, "fake-oauth")

	adapter := newFakeOAuthAdapter()
	svc, _ := newOAuthTestService(t, db, nil)

	if _, err := svc.Begin(context.Background(), application.BeginOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RedirectURI: "http://127.0.0.1:8081/callback",
	}); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	rawState := adapter.lastState
	if rawState == "" {
		t.Fatalf("fake adapter never captured a state value")
	}
	wantHash := stateHashOf(rawState)

	rows, err := db.Conn().Query(`SELECT state_sha256, provider_id, transaction_id, key_id, nonce, ciphertext FROM oauth_transactions`)
	if err != nil {
		t.Fatalf("query oauth_transactions: %v", err)
	}
	defer func() { _ = rows.Close() }()

	found := false
	for rows.Next() {
		var stateSHA256, providerID, transactionID, keyID string
		var nonce, ciphertext []byte
		if err := rows.Scan(&stateSHA256, &providerID, &transactionID, &keyID, &nonce, &ciphertext); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if stateSHA256 == wantHash {
			found = true
		}
		// The raw state must never appear verbatim in ANY column,
		// including the primary key itself (which correctly holds only
		// the hash, never the raw value).
		assertNoFragment(t, stateSHA256, rawState, "state_sha256 column")
		assertNoFragment(t, providerID, rawState, "provider_id column")
		assertNoFragment(t, transactionID, rawState, "transaction_id column")
		assertNoFragment(t, keyID, rawState, "key_id column")
		assertNoFragment(t, string(nonce), rawState, "nonce column")
		assertNoFragment(t, string(ciphertext), rawState, "ciphertext column")
	}
	if !found {
		t.Fatalf("no oauth_transactions row has state_sha256 = hex(sha256(rawState)) — the hash itself was not stored correctly")
	}
}

// TestOAuthService_ReplaySafety_ExactlyOneWinnerNoSecondAdapterExchange is
// the core anti-replay invariant: two Complete calls with the identical
// state — issued concurrently — must yield exactly one success; the
// loser is rejected and the adapter's CompleteOAuth is called exactly
// once in total (never twice for the same transaction).
func TestOAuthService_ReplaySafety_ExactlyOneWinnerNoSecondAdapterExchange(t *testing.T) {
	db := migratedDB(t)
	seedProvider(t, db, "fake-oauth")

	adapter := newFakeOAuthAdapter()
	svc, _ := newOAuthTestService(t, db, nil)

	beginResult, err := svc.Begin(context.Background(), application.BeginOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RedirectURI: "http://127.0.0.1:8081/callback",
	})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	rawState := adapter.lastState
	stateHash := stateHashOf(rawState)

	var wg sync.WaitGroup
	var successes int32
	var failures int32
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := svc.Complete(context.Background(), application.CompleteOAuthParams{
				ProviderID: "fake-oauth", Adapter: adapter, RawState: rawState, Code: "race-code",
				RedirectURI: "http://127.0.0.1:8081/callback", FundingMode: domain.FundingModeOwnerPolicy,
			})
			if err == nil {
				atomic.AddInt32(&successes, 1)
			} else if errors.Is(err, application.ErrOAuthTransactionInvalid) {
				atomic.AddInt32(&failures, 1)
			}
		}()
	}
	wg.Wait()

	if successes != 1 {
		t.Fatalf("successes = %d, want exactly 1", successes)
	}
	if failures != 1 {
		t.Fatalf("failures (ErrOAuthTransactionInvalid) = %d, want exactly 1", failures)
	}
	if adapter.getCompleteCalls() != 1 {
		t.Fatalf("adapter.CompleteOAuth called %d times, want exactly 1 (the replay must never reach the adapter)", adapter.getCompleteCalls())
	}

	_, _, keyID, nonce, ciphertext, _, _, consumed, ok := rawOAuthTxRow(t, db, stateHash)
	if !ok {
		t.Fatalf("oauth_transactions row for state hash %q no longer exists", stateHash)
	}
	if consumed != 1 {
		t.Fatalf("consumed = %d, want 1 after a successful consume", consumed)
	}
	if keyID != "" || len(nonce) != 0 || len(ciphertext) != 0 {
		t.Fatalf("verifier envelope columns not cleared after consume: key_id=%q nonce=%v ciphertext=%v", keyID, nonce, ciphertext)
	}
	_ = beginResult
}

// TestOAuthService_CodeNeverStored_Canary pushes a distinctive canary
// code through the full begin->callback flow and asserts it appears in
// no DB column across oauth_transactions, accounts, or
// account_credentials — the `code` parameter must never be persisted
// anywhere.
func TestOAuthService_CodeNeverStored_Canary(t *testing.T) {
	const canaryCode = "CANARY-9f3Kx2Qw8pLm0Zt7Vb4Nr1Hy6Dc5Ea-oauth-code"

	db := migratedDB(t)
	seedProvider(t, db, "fake-oauth")

	adapter := newFakeOAuthAdapter()
	svc, _ := newOAuthTestService(t, db, nil)

	if _, err := svc.Begin(context.Background(), application.BeginOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RedirectURI: "http://127.0.0.1:8081/callback",
	}); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	_, account, err := svc.Complete(context.Background(), application.CompleteOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RawState: adapter.lastState, Code: canaryCode,
		RedirectURI: "http://127.0.0.1:8081/callback", FundingMode: domain.FundingModeOwnerPolicy,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if adapter.lastCode != canaryCode {
		t.Fatalf("adapter did not receive the code at all — test is not exercising the right path")
	}

	assertNoFragmentAnywhere(t, db, "oauth_transactions", canaryCode)
	assertNoFragmentAnywhere(t, db, "accounts", canaryCode)
	assertNoFragmentAnywhere(t, db, "account_credentials", canaryCode)
	_ = account
}

// assertNoFragmentAnywhere dumps every row of every TEXT/BLOB-shaped
// column in table (via a generic SELECT *) and asserts none of them
// contain any 8+ character substring of secret.
func assertNoFragmentAnywhere(t *testing.T, db *storage.DB, table, secret string) {
	t.Helper()
	rows, err := db.Conn().Query(`SELECT * FROM ` + table)
	if err != nil {
		t.Fatalf("query %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns %s: %v", table, err)
	}

	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan %s: %v", table, err)
		}
		for i, v := range vals {
			var s string
			switch val := v.(type) {
			case []byte:
				s = string(val)
			case string:
				s = val
			default:
				continue
			}
			assertNoFragment(t, s, secret, table+"."+cols[i])
		}
	}
}

// TestOAuthService_ExpiredTransaction_RejectedRowUnchanged is the expiry
// invariant: a transaction whose expires_at is already in the past is
// rejected by Complete, the row is left completely unchanged (still
// consumed = 0, verifier envelope intact), and the adapter's
// CompleteOAuth is never called.
func TestOAuthService_ExpiredTransaction_RejectedRowUnchanged(t *testing.T) {
	db := migratedDB(t)
	seedProvider(t, db, "fake-oauth")

	adapter := newFakeOAuthAdapter()
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc, _ := newOAuthTestService(t, db, func() time.Time { return clock })

	if _, err := svc.Begin(context.Background(), application.BeginOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RedirectURI: "http://127.0.0.1:8081/callback",
	}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	rawState := adapter.lastState
	stateHash := stateHashOf(rawState)

	_, _, beforeKeyID, beforeNonce, beforeCiphertext, _, _, beforeConsumed, ok := rawOAuthTxRow(t, db, stateHash)
	if !ok {
		t.Fatalf("transaction row missing before expiry test")
	}

	clock = clock.Add(11 * time.Minute) // past the 10-minute TTL

	_, _, err := svc.Complete(context.Background(), application.CompleteOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RawState: rawState, Code: "any-code",
		RedirectURI: "http://127.0.0.1:8081/callback", FundingMode: domain.FundingModeOwnerPolicy,
	})
	if !errors.Is(err, application.ErrOAuthTransactionInvalid) {
		t.Fatalf("error = %v, want ErrOAuthTransactionInvalid for an expired transaction", err)
	}
	if adapter.getCompleteCalls() != 0 {
		t.Fatalf("adapter.CompleteOAuth called %d times, want 0 for an expired transaction", adapter.getCompleteCalls())
	}

	_, _, afterKeyID, afterNonce, afterCiphertext, _, _, afterConsumed, ok := rawOAuthTxRow(t, db, stateHash)
	if !ok {
		t.Fatalf("transaction row disappeared after a rejected expired Complete")
	}
	if afterConsumed != beforeConsumed || afterKeyID != beforeKeyID || string(afterNonce) != string(beforeNonce) || string(afterCiphertext) != string(beforeCiphertext) {
		t.Fatalf("expired transaction row was modified by a rejected Complete: before consumed=%d after consumed=%d", beforeConsumed, afterConsumed)
	}
}

// TestOAuthService_ProviderMismatch_Rejected is the provider-mismatch
// invariant: a callback claiming a DIFFERENT providerID than the one the
// transaction was begun under is rejected, and the adapter's
// CompleteOAuth is never called (the state is already irreversibly
// consumed by the time the mismatch is detected, so there is nothing to
// "undo" — the transaction is simply spent).
func TestOAuthService_ProviderMismatch_Rejected(t *testing.T) {
	db := migratedDB(t)
	seedProvider(t, db, "fake-oauth")
	seedProvider(t, db, "other-oauth")

	adapter := newFakeOAuthAdapter()
	svc, _ := newOAuthTestService(t, db, nil)

	if _, err := svc.Begin(context.Background(), application.BeginOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RedirectURI: "http://127.0.0.1:8081/callback",
	}); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	rawState := adapter.lastState

	_, _, err := svc.Complete(context.Background(), application.CompleteOAuthParams{
		ProviderID: "other-oauth", Adapter: adapter, RawState: rawState, Code: "any-code",
		RedirectURI: "http://127.0.0.1:8081/callback", FundingMode: domain.FundingModeOwnerPolicy,
	})
	if !errors.Is(err, application.ErrOAuthTransactionInvalid) {
		t.Fatalf("error = %v, want ErrOAuthTransactionInvalid for a provider mismatch", err)
	}
	if adapter.getCompleteCalls() != 0 {
		t.Fatalf("adapter.CompleteOAuth called %d times, want 0 for a provider mismatch", adapter.getCompleteCalls())
	}
}

// TestOAuthService_AccountAlreadyConnected_CreatesNothing proves
// reauth/re-linking is out of scope for this unit: a provider identity
// that already resolves to an existing account is rejected with
// ErrOAuthAccountAlreadyConnected and creates no new account/credential/
// funding rows.
func TestOAuthService_AccountAlreadyConnected_CreatesNothing(t *testing.T) {
	db := migratedDB(t)
	seedProvider(t, db, "fake-oauth")
	seedAccount(t, db, "fake-oauth", "existing-acct")
	if _, err := db.Conn().Exec(
		`UPDATE accounts SET external_id = ? WHERE id = ?`, "ext-1", "existing-acct",
	); err != nil {
		t.Fatalf("seed external_id: %v", err)
	}

	adapter := newFakeOAuthAdapter() // identity.ExternalID == "ext-1"
	svc, _ := newOAuthTestService(t, db, nil)

	if _, err := svc.Begin(context.Background(), application.BeginOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RedirectURI: "http://127.0.0.1:8081/callback",
	}); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	_, _, err := svc.Complete(context.Background(), application.CompleteOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RawState: adapter.lastState, Code: "any-code",
		RedirectURI: "http://127.0.0.1:8081/callback", FundingMode: domain.FundingModeOwnerPolicy,
	})
	if !errors.Is(err, application.ErrOAuthAccountAlreadyConnected) {
		t.Fatalf("error = %v, want ErrOAuthAccountAlreadyConnected", err)
	}
	assertCount(t, db, "accounts", 1)
	assertCount(t, db, "account_credentials", 0)
	assertCount(t, db, "account_funding_evidence", 0)
}

// TestOAuthService_PKCE_ChallengeMatchesVerifierS256 proves the PKCE
// wiring itself: the challenge Begin hands the adapter is the RFC 7636
// S256 transform of the verifier Complete later decrypts and hands back
// to the adapter.
func TestOAuthService_PKCE_ChallengeMatchesVerifierS256(t *testing.T) {
	db := migratedDB(t)
	seedProvider(t, db, "fake-oauth")

	adapter := newFakeOAuthAdapter()
	svc, _ := newOAuthTestService(t, db, nil)

	if _, err := svc.Begin(context.Background(), application.BeginOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RedirectURI: "http://127.0.0.1:8081/callback",
	}); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	if _, _, err := svc.Complete(context.Background(), application.CompleteOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RawState: adapter.lastState, Code: "any-code",
		RedirectURI: "http://127.0.0.1:8081/callback", FundingMode: domain.FundingModeOwnerPolicy,
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	sum := sha256.Sum256([]byte(adapter.lastVerifier))
	wantChallenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if adapter.lastChallenge != wantChallenge {
		t.Fatalf("challenge = %q, want S256(verifier) = %q", adapter.lastChallenge, wantChallenge)
	}
}

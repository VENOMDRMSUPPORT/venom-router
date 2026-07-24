package application_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// seedActiveCredential inserts an 'active' account_credentials row
// directly (bypassing any service), for tests that need a pre-existing
// credential to prove it survives or is correctly retired.
func seedActiveCredential(t *testing.T, db *storage.DB, kr *secrets.Keyring, id, accountID, providerID string, kind domain.CredentialKind, plaintext string) {
	t.Helper()
	env, err := secrets.Encrypt(kr, secrets.RecordIdentity{
		Purpose: "credential", Provider: providerID, Account: accountID, Record: id, Kind: string(kind),
	}, []byte(plaintext))
	if err != nil {
		t.Fatalf("seed active credential: encrypt: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO account_credentials
			(id, account_id, provider_id, kind, state, fingerprint_sha256, key_id, nonce, ciphertext, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 'active', ?, ?, ?, ?, 0, 0)`,
		id, accountID, providerID, string(kind), id+"-fingerprint", env.KeyID, env.Nonce, env.Ciphertext,
	); err != nil {
		t.Fatalf("seed active credential: insert: %v", err)
	}
}

// setAccountExternalID overrides a seeded account's external_id column
// directly, so a fakeOAuthAdapter's fixed identity.ExternalID can be
// made to match (or deliberately NOT match) it.
func setAccountExternalID(t *testing.T, db *storage.DB, accountID, externalID string) {
	t.Helper()
	if _, err := db.Conn().Exec(`UPDATE accounts SET external_id = ? WHERE id = ?`, externalID, accountID); err != nil {
		t.Fatalf("set account external_id: %v", err)
	}
}

// TestOAuthService_Reauth_StagingSwapHappyPath is the core P2b-PROV-008
// proof: a completion whose identity resolves to an existing account
// with an active oauth2 credential stages the new one, validates it
// (via the adapter's own successful exchange), and atomically swaps it
// in — end state: exactly one active credential (the NEW one), the old
// one retired, reauth_in_progress cleared, health_state healthy, and
// the account's own identity (external_id) unchanged.
func TestOAuthService_Reauth_StagingSwapHappyPath(t *testing.T) {
	db := migratedDB(t)
	seedProvider(t, db, "fake-oauth")
	seedAccount(t, db, "fake-oauth", "acct-1")
	setAccountExternalID(t, db, "acct-1", "ext-1")

	kr := newTestKeyring(t)
	seedActiveCredential(t, db, kr, "old-cred", "acct-1", "fake-oauth", domain.CredentialKindOAuth2, "old-token-value")

	adapter := newFakeOAuthAdapter() // identity.ExternalID == "ext-1"
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc, _ := newOAuthTestService(t, db, func() time.Time { return now })

	if _, err := svc.Begin(context.Background(), application.BeginOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RedirectURI: "http://127.0.0.1:8081/callback",
	}); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	_, account, err := svc.Complete(context.Background(), application.CompleteOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RawState: adapter.lastState, Code: "any-code",
		RedirectURI: "http://127.0.0.1:8081/callback", FundingMode: domain.FundingModeOwnerPolicy,
	})
	if err != nil {
		t.Fatalf("Complete: %v, want a successful reauthentication swap", err)
	}
	if account.ID != "acct-1" {
		t.Fatalf("account.ID = %q, want the existing acct-1 (no new account)", account.ID)
	}
	if account.ExternalID != "ext-1" {
		t.Fatalf("account.ExternalID = %q, want unchanged ext-1", account.ExternalID)
	}

	// Exactly one active oauth2 credential for this account, and it is
	// NOT the old one.
	var activeID string
	if err := db.Conn().QueryRow(
		`SELECT id FROM account_credentials WHERE account_id = ? AND kind = 'oauth2' AND state = 'active'`, "acct-1",
	).Scan(&activeID); err != nil {
		t.Fatalf("query active credential: %v", err)
	}
	if activeID == "old-cred" {
		t.Fatalf("active credential is still old-cred, want a freshly promoted one")
	}
	var activeCount int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM account_credentials WHERE account_id = ? AND kind = 'oauth2' AND state = 'active'`, "acct-1",
	).Scan(&activeCount); err != nil {
		t.Fatalf("count active credentials: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("active oauth2 credential count = %d, want exactly 1", activeCount)
	}

	var oldState string
	var retiredAt sql.NullInt64
	if err := db.Conn().QueryRow(`SELECT state, retired_at FROM account_credentials WHERE id = 'old-cred'`).Scan(&oldState, &retiredAt); err != nil {
		t.Fatalf("query old credential: %v", err)
	}
	if oldState != "retired" || !retiredAt.Valid {
		t.Fatalf("old credential state/retired_at = %q/%v, want retired/non-null", oldState, retiredAt)
	}

	var reauthInProgress int
	var healthState string
	if err := db.Conn().QueryRow(`SELECT reauth_in_progress, health_state FROM accounts WHERE id = 'acct-1'`).Scan(&reauthInProgress, &healthState); err != nil {
		t.Fatalf("query account: %v", err)
	}
	if reauthInProgress != 0 {
		t.Fatalf("reauth_in_progress = %d, want 0 after a successful swap", reauthInProgress)
	}
	if healthState != "healthy" {
		t.Fatalf("health_state = %q, want healthy after a successful swap", healthState)
	}
}

// TestOAuthService_Reauth_CrashRecoverySweepDiscardsStaleStagedOnly
// proves P2b-PROV-008 §5's crash-recovery sweep: a staged credential
// row older than the cutoff is discarded and reauth_in_progress is
// cleared, while a DIFFERENT account's active credential — and this
// same account's own active credential, if any — are never touched.
func TestOAuthService_Reauth_CrashRecoverySweepDiscardsStaleStagedOnly(t *testing.T) {
	db := migratedDB(t)
	seedProvider(t, db, "fake-oauth")
	seedAccount(t, db, "fake-oauth", "acct-1")

	kr := newTestKeyring(t)
	seedActiveCredential(t, db, kr, "active-cred", "acct-1", "fake-oauth", domain.CredentialKindOAuth2, "active-token-value")

	reauthRepo := storage.NewReauthRepo(db)
	staleEnv, err := secrets.Encrypt(kr, secrets.RecordIdentity{
		Purpose: "credential", Provider: "fake-oauth", Account: "acct-1", Record: "stale-staged", Kind: "oauth2",
	}, []byte("stale-in-flight-token"))
	if err != nil {
		t.Fatalf("encrypt stale staged credential: %v", err)
	}
	staleCreatedAt := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := reauthRepo.StageCredential(
		context.Background(), "acct-1", "fake-oauth",
		domain.Credential{ID: "stale-staged", AccountID: "acct-1", Kind: domain.CredentialKindOAuth2, State: domain.CredentialStaged, Fingerprint: "stale-fp"},
		staleEnv, staleCreatedAt,
	); err != nil {
		t.Fatalf("StageCredential: %v", err)
	}

	var reauthBefore int
	if err := db.Conn().QueryRow(`SELECT reauth_in_progress FROM accounts WHERE id = 'acct-1'`).Scan(&reauthBefore); err != nil {
		t.Fatalf("query reauth_in_progress before sweep: %v", err)
	}
	if reauthBefore != 1 {
		t.Fatalf("reauth_in_progress before sweep = %d, want 1 (staged)", reauthBefore)
	}

	svc, _ := newOAuthTestService(t, db, nil)
	cutoff := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) // well after staleCreatedAt
	n, err := svc.SweepStaleStagedCredentials(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("SweepStaleStagedCredentials: %v", err)
	}
	if n != 1 {
		t.Fatalf("swept %d rows, want exactly 1", n)
	}

	assertCount(t, db, "account_credentials", 1) // only active-cred remains

	var reauthAfter int
	if err := db.Conn().QueryRow(`SELECT reauth_in_progress FROM accounts WHERE id = 'acct-1'`).Scan(&reauthAfter); err != nil {
		t.Fatalf("query reauth_in_progress after sweep: %v", err)
	}
	if reauthAfter != 0 {
		t.Fatalf("reauth_in_progress after sweep = %d, want 0", reauthAfter)
	}

	var activeState string
	if err := db.Conn().QueryRow(`SELECT state FROM account_credentials WHERE id = 'active-cred'`).Scan(&activeState); err != nil {
		t.Fatalf("query active-cred: %v", err)
	}
	if activeState != "active" {
		t.Fatalf("active-cred state = %q, want still active (untouched by the sweep)", activeState)
	}
}

// TestOAuthService_Reauth_SecondStagedRejected proves the concurrency
// guard: a second reauthentication attempt while one credential is
// already staged (simulating an in-flight/interrupted reauth) is
// rejected with domain.ErrReauthenticationInProgress, and no second
// staged row is ever created.
func TestOAuthService_Reauth_SecondStagedRejected(t *testing.T) {
	db := migratedDB(t)
	seedProvider(t, db, "fake-oauth")
	seedAccount(t, db, "fake-oauth", "acct-1")
	setAccountExternalID(t, db, "acct-1", "ext-1")

	kr := newTestKeyring(t)
	reauthRepo := storage.NewReauthRepo(db)
	inflightEnv, err := secrets.Encrypt(kr, secrets.RecordIdentity{
		Purpose: "credential", Provider: "fake-oauth", Account: "acct-1", Record: "inflight-staged", Kind: "oauth2",
	}, []byte("in-flight-token"))
	if err != nil {
		t.Fatalf("encrypt in-flight staged credential: %v", err)
	}
	if err := reauthRepo.StageCredential(
		context.Background(), "acct-1", "fake-oauth",
		domain.Credential{ID: "inflight-staged", AccountID: "acct-1", Kind: domain.CredentialKindOAuth2, State: domain.CredentialStaged, Fingerprint: "fp1"},
		inflightEnv, time.Now(),
	); err != nil {
		t.Fatalf("StageCredential: %v", err)
	}

	adapter := newFakeOAuthAdapter() // identity.ExternalID == "ext-1"
	svc, _ := newOAuthTestService(t, db, nil)

	if _, err := svc.Begin(context.Background(), application.BeginOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RedirectURI: "http://127.0.0.1:8081/callback",
	}); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	_, _, err = svc.Complete(context.Background(), application.CompleteOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RawState: adapter.lastState, Code: "any-code",
		RedirectURI: "http://127.0.0.1:8081/callback", FundingMode: domain.FundingModeOwnerPolicy,
	})
	if !errors.Is(err, domain.ErrReauthenticationInProgress) {
		t.Fatalf("error = %v, want domain.ErrReauthenticationInProgress", err)
	}
	assertCount(t, db, "account_credentials", 1) // only the pre-existing staged row
}

// TestOAuthService_Reauth_IdentityMismatch_DifferentExistingAccount
// proves the account_identity_mismatch guard: a targeted reauth for
// account X (ReauthAccountID) whose exchanged identity resolves to a
// DIFFERENT existing account is rejected, and X's own credential is
// left completely untouched — nothing is staged or swapped anywhere.
func TestOAuthService_Reauth_IdentityMismatch_DifferentExistingAccount(t *testing.T) {
	db := migratedDB(t)
	seedProvider(t, db, "fake-oauth")
	seedAccount(t, db, "fake-oauth", "acct-X")
	setAccountExternalID(t, db, "acct-X", "ext-X")

	kr := newTestKeyring(t)
	seedActiveCredential(t, db, kr, "cred-X", "acct-X", "fake-oauth", domain.CredentialKindOAuth2, "original-token-value")

	adapter := newFakeOAuthAdapter() // identity.ExternalID == "ext-1" != "ext-X"
	svc, _ := newOAuthTestService(t, db, nil)

	if _, err := svc.Begin(context.Background(), application.BeginOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RedirectURI: "http://127.0.0.1:8081/callback",
	}); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	_, _, err := svc.Complete(context.Background(), application.CompleteOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RawState: adapter.lastState, Code: "any-code",
		RedirectURI: "http://127.0.0.1:8081/callback", FundingMode: domain.FundingModeOwnerPolicy,
		ReauthAccountID: "acct-X",
	})
	if !errors.Is(err, application.ErrOAuthAccountIdentityMismatch) {
		t.Fatalf("error = %v, want ErrOAuthAccountIdentityMismatch", err)
	}

	assertCount(t, db, "account_credentials", 1) // still just cred-X; nothing staged
	var state string
	if err := db.Conn().QueryRow(`SELECT state FROM account_credentials WHERE id = 'cred-X'`).Scan(&state); err != nil {
		t.Fatalf("query cred-X: %v", err)
	}
	if state != "active" {
		t.Fatalf("cred-X state = %q, want still active (untouched)", state)
	}
}

// TestOAuthService_Reauth_IdentityMismatch_NoExistingAccountAtAll proves
// the other mismatch sub-case: a targeted reauth for account X whose
// exchanged identity resolves to NO existing account at all is also
// rejected with ErrOAuthAccountIdentityMismatch, and — critically — no
// new account is created either (a targeted reauth must never silently
// fall back to first-time enrollment).
func TestOAuthService_Reauth_IdentityMismatch_NoExistingAccountAtAll(t *testing.T) {
	db := migratedDB(t)
	seedProvider(t, db, "fake-oauth")
	seedAccount(t, db, "fake-oauth", "acct-X") // external_id defaults to "acct-X" itself (seedAccount)

	adapter := newFakeOAuthAdapter() // identity.ExternalID == "ext-1", matches no account
	svc, _ := newOAuthTestService(t, db, nil)

	if _, err := svc.Begin(context.Background(), application.BeginOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RedirectURI: "http://127.0.0.1:8081/callback",
	}); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	_, _, err := svc.Complete(context.Background(), application.CompleteOAuthParams{
		ProviderID: "fake-oauth", Adapter: adapter, RawState: adapter.lastState, Code: "any-code",
		RedirectURI: "http://127.0.0.1:8081/callback", FundingMode: domain.FundingModeOwnerPolicy,
		ReauthAccountID: "acct-X",
	})
	if !errors.Is(err, application.ErrOAuthAccountIdentityMismatch) {
		t.Fatalf("error = %v, want ErrOAuthAccountIdentityMismatch", err)
	}
	assertCount(t, db, "accounts", 1) // no new account created
	assertCount(t, db, "account_credentials", 0)
}

// TestOAuthService_Reauth_MultiKindCoexistence proves staging/swapping
// an oauth2 credential never disturbs a DIFFERENT active-kind
// credential (e.g. api_key) on the same account — the two kinds coexist
// independently, exactly as domain.CanStageCredential/
// CanAddActiveCredential's per-(account,kind) scoping documents.
func TestOAuthService_Reauth_MultiKindCoexistence(t *testing.T) {
	db := migratedDB(t)
	seedProvider(t, db, "fake-oauth")
	seedAccount(t, db, "fake-oauth", "acct-1")
	setAccountExternalID(t, db, "acct-1", "ext-1")

	kr := newTestKeyring(t)
	seedActiveCredential(t, db, kr, "api-cred", "acct-1", "fake-oauth", domain.CredentialKindAPIKey, "api-key-value")

	adapter := newFakeOAuthAdapter() // identity.ExternalID == "ext-1"
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

	var apiState string
	if err := db.Conn().QueryRow(`SELECT state FROM account_credentials WHERE id = 'api-cred'`).Scan(&apiState); err != nil {
		t.Fatalf("query api-cred: %v", err)
	}
	if apiState != "active" {
		t.Fatalf("api-cred (a different kind) state = %q, want still active (untouched by the oauth2 reauth)", apiState)
	}

	var oauthActiveCount int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM account_credentials WHERE account_id = 'acct-1' AND kind = 'oauth2' AND state = 'active'`,
	).Scan(&oauthActiveCount); err != nil {
		t.Fatalf("count active oauth2 credentials: %v", err)
	}
	if oauthActiveCount != 1 {
		t.Fatalf("active oauth2 credential count = %d, want exactly 1", oauthActiveCount)
	}
}

// TestOAuthService_Reauth_AtomicSwap_NeverTwoActiveCredentialsOfSameKind
// is the atomic-swap invariant test: after a reauth swap, exactly one
// active oauth2 credential exists for the account — never two. This
// invariant is enforced by TWO independent layers: internal/storage's
// SwapStagedToActive retires the old active credential BEFORE promoting
// the staged one (ordering), and the M2 idx_cred_active_per_kind
// partial-unique index would reject the promoting UPDATE outright if
// that ordering were ever violated while an active row still existed
// (the structural backstop). Both were manually verified to matter via
// a RED->restore exercise: temporarily reordering
// internal/storage/reauth.go's SwapStagedToActive to promote the staged
// row BEFORE retiring the old active one causes this test to fail
// closed (Complete returns an error from the unique-index violation,
// so the account's active credential stays the OLD one rather than the
// new one) rather than silently leaving two active rows — proving the
// index backstop holds even when the code's ordering does not; the
// ordering was then restored byte-for-byte and this test re-confirmed
// green. See this unit's final report for the exact commands run.
func TestOAuthService_Reauth_AtomicSwap_NeverTwoActiveCredentialsOfSameKind(t *testing.T) {
	db := migratedDB(t)
	seedProvider(t, db, "fake-oauth")
	seedAccount(t, db, "fake-oauth", "acct-1")
	setAccountExternalID(t, db, "acct-1", "ext-1")

	kr := newTestKeyring(t)
	seedActiveCredential(t, db, kr, "old-cred", "acct-1", "fake-oauth", domain.CredentialKindOAuth2, "old-token-value")

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
		t.Fatalf("Complete: %v, want a successful swap", err)
	}

	var activeCount int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM account_credentials WHERE account_id = 'acct-1' AND kind = 'oauth2' AND state = 'active'`,
	).Scan(&activeCount); err != nil {
		t.Fatalf("count active oauth2 credentials: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("active oauth2 credentials for acct-1 = %d, want exactly 1 (never two simultaneously-active rows of the same kind)", activeCount)
	}
}

// TestOAuthService_Reauth_Canary_StagedTokenNeverPlaintextInDB pushes a
// distinctive canary token through a full reauth swap and asserts it
// never appears in ANY column of accounts or account_credentials — only
// the encrypted envelope (nonce/ciphertext) may exist, never the
// plaintext.
func TestOAuthService_Reauth_Canary_StagedTokenNeverPlaintextInDB(t *testing.T) {
	const canaryToken = "CANARY-9f3Kx2Qw8pLm0Zt7Vb4Nr1Hy6Dc5Ea-Ws3Rq8Ln-reauth"

	db := migratedDB(t)
	seedProvider(t, db, "fake-oauth")
	seedAccount(t, db, "fake-oauth", "acct-1")
	setAccountExternalID(t, db, "acct-1", "ext-1")

	adapter := newFakeOAuthAdapter()
	adapter.creds = providers.StoredCredentials{Value: canaryToken}
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

	assertNoFragmentAnywhere(t, db, "account_credentials", canaryToken)
	assertNoFragmentAnywhere(t, db, "accounts", canaryToken)
}

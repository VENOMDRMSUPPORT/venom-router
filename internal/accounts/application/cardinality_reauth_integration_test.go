package application_test

// cardinality_reauth_integration_test.go is P2b-TEST-002: it proves the
// M2 partial-unique indexes (idx_cred_active_per_kind,
// idx_cred_staged_per_kind) BITE at the real SQLite layer, that two
// DIFFERENT credential kinds (github_oauth + copilot_service, 02
// §3/03 §2e/10 §2) genuinely coexist per-kind rather than per-account,
// and it fills the two staging-swap-interruption gaps
// reauth_test.go does not already cover.
//
// reauth_test.go (same package) already proves, and is NOT duplicated
// here (cited by exact test name at each relevant point below):
//   - TestOAuthService_Reauth_StagingSwapHappyPath — the atomic swap's
//     happy path (old retired, new active, reauth_in_progress cleared).
//   - TestOAuthService_Reauth_CrashRecoverySweepDiscardsStaleStagedOnly —
//     a single stale staged row is swept, the SAME account's own active
//     credential is untouched, reauth_in_progress clears.
//   - TestOAuthService_Reauth_SecondStagedRejected — a second
//     reauthentication attempt while one is already staged is rejected
//     with domain.ErrReauthenticationInProgress and no second staged row
//     is created (this IS "second-staged rejected" — not re-implemented
//     here).
//   - TestOAuthService_Reauth_IdentityMismatch_* — account_identity_mismatch
//     guards.
//   - TestOAuthService_Reauth_MultiKindCoexistence — proves an oauth2
//     reauth swap never disturbs a DIFFERENT active-kind (api_key)
//     credential on the same account, via the OAuthEnrollmentService
//     flow. This file's own multi-kind test below is a DIFFERENT,
//     additional proof: it persists TWO specific kinds the card names
//     (github_oauth + copilot_service) directly through the storage
//     repos (no OAuth service involved), reading both back to confirm
//     the partial indexes are genuinely per-(account,kind).
//   - TestOAuthService_Reauth_AtomicSwap_NeverTwoActiveCredentialsOfSameKind —
//     the atomic-swap invariant, plus its RED->restore manual exercise
//     against internal/storage/reauth.go's ordering (documented in that
//     test's own comment).
//   - TestOAuthService_Reauth_Canary_StagedTokenNeverPlaintextInDB — the
//     secret-canary proof for a reauth swap.
//
// What is NOT already covered, and IS added here:
//  1. The active-per-kind index's raw DB-layer bite: a second ACTIVE
//     credential of the SAME kind is rejected by AccountCredentialRepo.
//     Create itself (not by any domain-level pre-check — this hits the
//     real idx_cred_active_per_kind constraint), while a different kind
//     succeeds. This is distinct from domain.CanAddActiveCredential's own
//     unit test (a pure in-memory function, no DB involved at all).
//  2. The staged-per-kind index's raw DB-layer bite: the analogous proof
//     for idx_cred_staged_per_kind.
//  3. Multi-kind coexistence via the exact kind names 02 §3/10 §2 use as
//     the canonical multi-credential example: github_oauth AND
//     copilot_service active simultaneously on one account.
//  4. The two staging-interruption sub-cases reauth_test.go's crash-
//     recovery test does not itself assert:
//     (a) immediately after StageCredential (an interrupted swap,
//         pre-sweep), the staged row count is exactly 1 and the prior
//         active credential is untouched — proven BEFORE any sweep runs
//         (reauth_test.go's crash-recovery test only asserts state
//         AFTER the sweep);
//     (b) SweepStaleStagedCredentials, given a stale staged row on one
//         account and a FRESH staged row on a DIFFERENT account, discards
//         ONLY the stale one — reauth_test.go's own sweep test uses a
//         single account/single staged row, so it cannot show a fresh
//         row (same or different account) surviving alongside a stale
//         one.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// cardinalityEnvelope encrypts plaintext under a RecordIdentity for
// (providerID, accountID, credID, kind) — the same shape
// CredentialService.Store uses internally — so this file's direct
// repo.Create calls (bypassing CredentialService entirely, to reach the
// raw DB constraint rather than any application-layer pre-check) still
// persist a structurally valid envelope.
func cardinalityEnvelope(t *testing.T, kr *secrets.Keyring, providerID, accountID, credID string, kind domain.CredentialKind, plaintext string) secrets.Envelope {
	t.Helper()
	env, err := secrets.Encrypt(kr, secrets.RecordIdentity{
		Purpose: "credential", Provider: providerID, Account: accountID, Record: credID, Kind: string(kind),
	}, []byte(plaintext))
	if err != nil {
		t.Fatalf("cardinalityEnvelope: encrypt: %v", err)
	}
	return env
}

// isUniqueConstraintErr reports whether err looks like a SQLite UNIQUE
// constraint violation — a driver-agnostic-enough substring check (both
// mattn/go-sqlite3 and this module's modernc.org/sqlite report the
// standard SQLite message text "UNIQUE constraint failed") used only to
// confirm a rejection came from the partial index itself rather than
// some unrelated failure, on top of the primary assertion (err != nil).
func isUniqueConstraintErr(err error) bool {
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "UNIQUE CONSTRAINT")
}

// --- 1. idx_cred_active_per_kind: raw DB-layer bite ---

// TestCardinality_ActivePerKindIndex_RejectsSecondActiveSameKind_AllowsDifferentKind
// is the two-direction, non-vacuous proof for idx_cred_active_per_kind,
// exercised directly through AccountCredentialRepo.Create (the real
// storage repo — no CredentialService/domain pre-check involved, so a
// failure here can ONLY be the SQLite constraint itself): a second
// ACTIVE api_key credential on the same account is rejected, while an
// active credential of a DIFFERENT kind on that same account succeeds.
func TestCardinality_ActivePerKindIndex_RejectsSecondActiveSameKind_AllowsDifferentKind(t *testing.T) {
	db := migratedDB(t)
	seedProviderAndAccount(t, db, "prov-active-idx", "acct-active-idx")
	kr := newTestKeyring(t)
	repo := storage.NewAccountCredentialRepo(db)
	now := time.Unix(0, 0)

	// First active api_key credential: succeeds.
	firstEnv := cardinalityEnvelope(t, kr, "prov-active-idx", "acct-active-idx", "active-1", domain.CredentialKindAPIKey, "plaintext-1")
	if err := repo.Create(context.Background(), "prov-active-idx",
		domain.Credential{ID: "active-1", AccountID: "acct-active-idx", Kind: domain.CredentialKindAPIKey, State: domain.CredentialActive, Fingerprint: "fp-active-1"},
		firstEnv, now,
	); err != nil {
		t.Fatalf("first active api_key credential: Create failed, want success: %v", err)
	}

	// Second active api_key credential (same kind, same account):
	// REJECTED by idx_cred_active_per_kind.
	secondEnv := cardinalityEnvelope(t, kr, "prov-active-idx", "acct-active-idx", "active-2", domain.CredentialKindAPIKey, "plaintext-2")
	err := repo.Create(context.Background(), "prov-active-idx",
		domain.Credential{ID: "active-2", AccountID: "acct-active-idx", Kind: domain.CredentialKindAPIKey, State: domain.CredentialActive, Fingerprint: "fp-active-2"},
		secondEnv, now,
	)
	if err == nil {
		t.Fatalf("second active api_key credential of the same kind: Create succeeded, want rejection by idx_cred_active_per_kind")
	}
	if !isUniqueConstraintErr(err) {
		t.Fatalf("second active api_key credential: error = %v, want a UNIQUE constraint violation (idx_cred_active_per_kind)", err)
	}

	// An active credential of a DIFFERENT kind on the SAME account:
	// SUCCEEDS — the index is per-(account,kind), not per-account.
	oauthEnv := cardinalityEnvelope(t, kr, "prov-active-idx", "acct-active-idx", "active-oauth", domain.CredentialKindOAuth2, "plaintext-oauth")
	if err := repo.Create(context.Background(), "prov-active-idx",
		domain.Credential{ID: "active-oauth", AccountID: "acct-active-idx", Kind: domain.CredentialKindOAuth2, State: domain.CredentialActive, Fingerprint: "fp-active-oauth"},
		oauthEnv, now,
	); err != nil {
		t.Fatalf("active oauth2 credential (different kind, same account): Create failed, want success: %v", err)
	}

	// Exactly two rows persisted: active-1 and active-oauth. active-2
	// never landed.
	assertCount(t, db, "account_credentials", 2)
	creds, err := repo.ListForAccount(context.Background(), "acct-active-idx")
	if err != nil {
		t.Fatalf("ListForAccount: %v", err)
	}
	var sawAPIKey, sawOAuth bool
	for _, c := range creds {
		if c.ID == "active-2" {
			t.Fatalf("active-2 (the rejected insert) is present in ListForAccount, want it absent")
		}
		if c.Kind == domain.CredentialKindAPIKey && c.State == domain.CredentialActive {
			sawAPIKey = true
		}
		if c.Kind == domain.CredentialKindOAuth2 && c.State == domain.CredentialActive {
			sawOAuth = true
		}
	}
	if !sawAPIKey || !sawOAuth {
		t.Fatalf("ListForAccount = %+v, want one active api_key row and one active oauth2 row", creds)
	}
}

// --- 2. idx_cred_staged_per_kind: raw DB-layer bite ---

// TestCardinality_StagedPerKindIndex_RejectsSecondStagedSameKind_AllowsFirst
// is the two-direction proof for idx_cred_staged_per_kind: a first
// STAGED api_key credential on an account succeeds, and a second staged
// api_key credential on the SAME account is rejected by the index —
// exercised directly through AccountCredentialRepo.Create, same as the
// active-index test above.
func TestCardinality_StagedPerKindIndex_RejectsSecondStagedSameKind_AllowsFirst(t *testing.T) {
	db := migratedDB(t)
	seedProviderAndAccount(t, db, "prov-staged-idx", "acct-staged-idx")
	kr := newTestKeyring(t)
	repo := storage.NewAccountCredentialRepo(db)
	now := time.Unix(0, 0)

	// First staged api_key credential: succeeds.
	firstEnv := cardinalityEnvelope(t, kr, "prov-staged-idx", "acct-staged-idx", "staged-1", domain.CredentialKindAPIKey, "plaintext-1")
	if err := repo.Create(context.Background(), "prov-staged-idx",
		domain.Credential{ID: "staged-1", AccountID: "acct-staged-idx", Kind: domain.CredentialKindAPIKey, State: domain.CredentialStaged, Fingerprint: "fp-staged-1"},
		firstEnv, now,
	); err != nil {
		t.Fatalf("first staged api_key credential: Create failed, want success: %v", err)
	}

	// Second staged api_key credential (same kind, same account):
	// REJECTED by idx_cred_staged_per_kind.
	secondEnv := cardinalityEnvelope(t, kr, "prov-staged-idx", "acct-staged-idx", "staged-2", domain.CredentialKindAPIKey, "plaintext-2")
	err := repo.Create(context.Background(), "prov-staged-idx",
		domain.Credential{ID: "staged-2", AccountID: "acct-staged-idx", Kind: domain.CredentialKindAPIKey, State: domain.CredentialStaged, Fingerprint: "fp-staged-2"},
		secondEnv, now,
	)
	if err == nil {
		t.Fatalf("second staged api_key credential of the same kind: Create succeeded, want rejection by idx_cred_staged_per_kind")
	}
	if !isUniqueConstraintErr(err) {
		t.Fatalf("second staged api_key credential: error = %v, want a UNIQUE constraint violation (idx_cred_staged_per_kind)", err)
	}

	assertCount(t, db, "account_credentials", 1)
	var state string
	if err := db.Conn().QueryRow(`SELECT state FROM account_credentials WHERE id = 'staged-1'`).Scan(&state); err != nil {
		t.Fatalf("query staged-1: %v", err)
	}
	if state != "staged" {
		t.Fatalf("staged-1 state = %q, want staged (untouched by the rejected second insert)", state)
	}
}

// --- 3. Multi-kind coexistence: github_oauth + copilot_service ---

// TestCardinality_MultiKindCoexistence_GithubOAuthAndCopilotService proves
// one account can hold an ACTIVE github_oauth credential AND an ACTIVE
// copilot_service credential SIMULTANEOUSLY — 02 §3's own doc comment
// names this exact pair as the canonical multi-credential example
// ("e.g. a github_oauth AND a copilot_service credential on one
// account"). Both are persisted through the real storage repos and read
// back, confirming idx_cred_active_per_kind is genuinely scoped to
// (account, kind), not (account) alone.
func TestCardinality_MultiKindCoexistence_GithubOAuthAndCopilotService(t *testing.T) {
	db := migratedDB(t)
	seedProviderAndAccount(t, db, "prov-multikind", "acct-multikind")
	kr := newTestKeyring(t)
	repo := storage.NewAccountCredentialRepo(db)
	now := time.Unix(0, 0)

	githubEnv := cardinalityEnvelope(t, kr, "prov-multikind", "acct-multikind", "cred-github", domain.CredentialKindGitHubOAuth, "github-token-value")
	if err := repo.Create(context.Background(), "prov-multikind",
		domain.Credential{ID: "cred-github", AccountID: "acct-multikind", Kind: domain.CredentialKindGitHubOAuth, State: domain.CredentialActive, Fingerprint: "fp-github"},
		githubEnv, now,
	); err != nil {
		t.Fatalf("active github_oauth credential: Create failed, want success: %v", err)
	}

	copilotEnv := cardinalityEnvelope(t, kr, "prov-multikind", "acct-multikind", "cred-copilot", domain.CredentialKindCopilotService, "copilot-token-value")
	if err := repo.Create(context.Background(), "prov-multikind",
		domain.Credential{ID: "cred-copilot", AccountID: "acct-multikind", Kind: domain.CredentialKindCopilotService, State: domain.CredentialActive, Fingerprint: "fp-copilot"},
		copilotEnv, now,
	); err != nil {
		t.Fatalf("active copilot_service credential (coexisting kind): Create failed, want success: %v", err)
	}

	creds, err := repo.ListForAccount(context.Background(), "acct-multikind")
	if err != nil {
		t.Fatalf("ListForAccount: %v", err)
	}
	if len(creds) != 2 {
		t.Fatalf("ListForAccount returned %d rows, want exactly 2 (github_oauth + copilot_service, both active)", len(creds))
	}
	var sawGitHub, sawCopilot bool
	for _, c := range creds {
		if c.State != domain.CredentialActive {
			t.Fatalf("credential %q state = %q, want active for both coexisting kinds", c.ID, c.State)
		}
		switch c.Kind {
		case domain.CredentialKindGitHubOAuth:
			sawGitHub = true
		case domain.CredentialKindCopilotService:
			sawCopilot = true
		}
	}
	if !sawGitHub || !sawCopilot {
		t.Fatalf("ListForAccount = %+v, want one active github_oauth row and one active copilot_service row", creds)
	}
}

// --- 4. Staging-interruption sub-cases NOT already in reauth_test.go ---

// TestReauthInterruption_StagedRowPreSweep_LeavesSingleStagedAndActiveIntact
// proves the interrupted-swap STATE ITSELF, before any sweep ever runs:
// staging a new credential (simulating a swap that crashed between
// staging and the atomic promote) leaves exactly one staged row and
// the prior active credential completely untouched.
// reauth_test.go's TestOAuthService_Reauth_CrashRecoverySweepDiscardsStaleStagedOnly
// only asserts this AFTER calling SweepStaleStagedCredentials; this test
// asserts it BEFORE any sweep is invoked at all.
func TestReauthInterruption_StagedRowPreSweep_LeavesSingleStagedAndActiveIntact(t *testing.T) {
	db := migratedDB(t)
	seedProvider(t, db, "prov-interrupt")
	seedAccount(t, db, "prov-interrupt", "acct-interrupt")
	kr := newTestKeyring(t)

	seedActiveCredential(t, db, kr, "active-cred", "acct-interrupt", "prov-interrupt", domain.CredentialKindOAuth2, "active-token-value")

	reauthRepo := storage.NewReauthRepo(db)
	stagedEnv := cardinalityEnvelope(t, kr, "prov-interrupt", "acct-interrupt", "interrupted-staged", domain.CredentialKindOAuth2, "in-flight-token-value")
	stagedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) // "recent" — this swap was just interrupted, not stale
	if err := reauthRepo.StageCredential(
		context.Background(), "acct-interrupt", "prov-interrupt",
		domain.Credential{ID: "interrupted-staged", AccountID: "acct-interrupt", Kind: domain.CredentialKindOAuth2, State: domain.CredentialStaged, Fingerprint: "fp-interrupted"},
		stagedEnv, stagedAt,
	); err != nil {
		t.Fatalf("StageCredential: %v", err)
	}

	// Pre-sweep: exactly one staged row (never two — CanStageCredential/
	// idx_cred_staged_per_kind would reject a second one, but here we
	// only ever staged one, and are confirming it landed as exactly one).
	var stagedCount int
	if err := db.Conn().QueryRow(
		`SELECT COUNT(*) FROM account_credentials WHERE account_id = 'acct-interrupt' AND state = 'staged'`,
	).Scan(&stagedCount); err != nil {
		t.Fatalf("count staged rows: %v", err)
	}
	if stagedCount != 1 {
		t.Fatalf("staged row count pre-sweep = %d, want exactly 1", stagedCount)
	}

	// The prior active credential is completely untouched: still active,
	// same id, not retired.
	var activeState string
	var retiredAt *int64
	if err := db.Conn().QueryRow(`SELECT state, retired_at FROM account_credentials WHERE id = 'active-cred'`).Scan(&activeState, &retiredAt); err != nil {
		t.Fatalf("query active-cred: %v", err)
	}
	if activeState != "active" {
		t.Fatalf("active-cred state pre-sweep = %q, want still active (interruption must not retire it)", activeState)
	}
	if retiredAt != nil {
		t.Fatalf("active-cred retired_at pre-sweep = %v, want NULL", retiredAt)
	}

	// reauth_in_progress is set (this account genuinely has an in-flight
	// reauthentication attempt right now).
	var reauthInProgress int
	if err := db.Conn().QueryRow(`SELECT reauth_in_progress FROM accounts WHERE id = 'acct-interrupt'`).Scan(&reauthInProgress); err != nil {
		t.Fatalf("query reauth_in_progress: %v", err)
	}
	if reauthInProgress != 1 {
		t.Fatalf("reauth_in_progress pre-sweep = %d, want 1", reauthInProgress)
	}

	assertCount(t, db, "account_credentials", 2) // active-cred + interrupted-staged, nothing else
}

// TestReauthInterruption_SweepDiscardsOnlyStaleRow_FreshRowOnOtherAccountUntouched
// proves SweepStaleStagedCredentials discards ONLY a genuinely stale
// staged row: with a STALE staged row on one account and a FRESH staged
// row (created after the cutoff) on a DIFFERENT account, the sweep
// removes exactly the stale one and leaves the fresh one — and that
// other account's own reauth_in_progress flag — completely untouched.
// reauth_test.go's sweep test uses a single account/single staged row,
// so it cannot show a fresh row surviving a sweep at all; this fills
// that gap.
func TestReauthInterruption_SweepDiscardsOnlyStaleRow_FreshRowOnOtherAccountUntouched(t *testing.T) {
	db := migratedDB(t)
	seedProvider(t, db, "prov-sweep")
	seedAccount(t, db, "prov-sweep", "acct-stale")
	seedAccount(t, db, "prov-sweep", "acct-fresh")
	kr := newTestKeyring(t)

	reauthRepo := storage.NewReauthRepo(db)

	staleEnv := cardinalityEnvelope(t, kr, "prov-sweep", "acct-stale", "stale-staged", domain.CredentialKindOAuth2, "stale-token-value")
	staleCreatedAt := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := reauthRepo.StageCredential(
		context.Background(), "acct-stale", "prov-sweep",
		domain.Credential{ID: "stale-staged", AccountID: "acct-stale", Kind: domain.CredentialKindOAuth2, State: domain.CredentialStaged, Fingerprint: "fp-stale"},
		staleEnv, staleCreatedAt,
	); err != nil {
		t.Fatalf("StageCredential (stale): %v", err)
	}

	cutoff := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	freshEnv := cardinalityEnvelope(t, kr, "prov-sweep", "acct-fresh", "fresh-staged", domain.CredentialKindOAuth2, "fresh-token-value")
	freshCreatedAt := cutoff.Add(24 * time.Hour) // strictly after the cutoff — never stale
	if err := reauthRepo.StageCredential(
		context.Background(), "acct-fresh", "prov-sweep",
		domain.Credential{ID: "fresh-staged", AccountID: "acct-fresh", Kind: domain.CredentialKindOAuth2, State: domain.CredentialStaged, Fingerprint: "fp-fresh"},
		freshEnv, freshCreatedAt,
	); err != nil {
		t.Fatalf("StageCredential (fresh): %v", err)
	}

	svc, _ := newOAuthTestService(t, db, nil)
	n, err := svc.SweepStaleStagedCredentials(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("SweepStaleStagedCredentials: %v", err)
	}
	if n != 1 {
		t.Fatalf("swept %d rows, want exactly 1 (only the stale one)", n)
	}

	// The stale row is gone.
	assertCount(t, db, "account_credentials", 1)
	var staleGone int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM account_credentials WHERE id = 'stale-staged'`).Scan(&staleGone); err != nil {
		t.Fatalf("count stale-staged: %v", err)
	}
	if staleGone != 0 {
		t.Fatalf("stale-staged row still present after sweep, want discarded")
	}

	// The fresh row on the OTHER account survives, still staged.
	var freshState string
	if err := db.Conn().QueryRow(`SELECT state FROM account_credentials WHERE id = 'fresh-staged'`).Scan(&freshState); err != nil {
		t.Fatalf("query fresh-staged: %v", err)
	}
	if freshState != "staged" {
		t.Fatalf("fresh-staged state after sweep = %q, want still staged (untouched)", freshState)
	}

	// The stale account's reauth_in_progress cleared (DiscardStaged's job)...
	var staleReauth int
	if err := db.Conn().QueryRow(`SELECT reauth_in_progress FROM accounts WHERE id = 'acct-stale'`).Scan(&staleReauth); err != nil {
		t.Fatalf("query acct-stale reauth_in_progress: %v", err)
	}
	if staleReauth != 0 {
		t.Fatalf("acct-stale reauth_in_progress after sweep = %d, want 0", staleReauth)
	}
	// ...while the FRESH account's own reauth_in_progress (still
	// genuinely mid-reauthentication) is completely untouched by a sweep
	// that had nothing stale to discard for it.
	var freshReauth int
	if err := db.Conn().QueryRow(`SELECT reauth_in_progress FROM accounts WHERE id = 'acct-fresh'`).Scan(&freshReauth); err != nil {
		t.Fatalf("query acct-fresh reauth_in_progress: %v", err)
	}
	if freshReauth != 1 {
		t.Fatalf("acct-fresh reauth_in_progress after sweep = %d, want still 1 (untouched)", freshReauth)
	}
}

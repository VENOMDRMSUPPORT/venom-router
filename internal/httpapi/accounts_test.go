package httpapi

// accounts_test.go exercises the P2b-CAPI-004 account-lifecycle surface
// (internal/httpapi/accounts.go): the GET list/detail projections, the
// credential reveal (the reverify-freshness gate, no-store, decrypt-once,
// the audit row's no-secret invariant, and the rate limit), the funding
// owner override (supersession, locked, version), the connection lifecycle
// (stop/resume/soft-disconnect), the health transition, and the provider
// sync endpoint. Every test seeds a real migrated SQLite DB with a
// provider, an account, and (where the test needs it) an active credential
// stored through the real CredentialService so reveal can decrypt it — the
// same real-SQLite + real-keyring posture the enrollment and credential
// tests use. Canary assertions (assertNoFragment) prove no credential
// material ever appears in a projection, an audit row, or an error.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// fixedAccountTestClock is the injectable clock every accounts test uses;
// its value is captured by pointer so a test can step it forward (e.g.
// past the 5-minute reverify window) between requests.
func fixedAccountTestClock() *time.Time {
	t := time.Date(2026, 3, 15, 9, 0, 0, 0, time.UTC)
	return &t
}

// newTestAccountsHandlerV2 builds an AccountsHandler over a fresh migrated
// DB with the given clock, and seeds one provider + one connected account
// (owner_policy funding) + one ACTIVE api_key credential whose stored
// plaintext is canaryKey (stored via the real CredentialService so reveal
// can decrypt it). It returns the handler, the DB, the seeded account id,
// and the active credential id.
//
// The credential is stored through CredentialService.Store (the canonical
// encrypt+persist path) rather than EnrollmentRepo.CreateConnectedAccount,
// so the account + funding rows are seeded directly — CreateConnectedAccount
// would double-insert the credential row CredentialService already wrote.
func newTestAccountsHandlerV2(t *testing.T, clock *time.Time, canaryKey string) (*AccountsHandler, *storage.DB, string, string) {
	t.Helper()
	db := testControlDB(t)

	if _, err := db.Conn().Exec(
		`INSERT INTO providers (id, display_name, auth_mode, funding_mode, funding_locked, created_at, updated_at) VALUES (?, ?, 'api_key', 'owner_policy', 0, 0, 0)`,
		"prov-a", "prov-a",
	); err != nil {
		t.Fatalf("seed provider: %v", err)
	}

	accountRepo := storage.NewAccountRepo(db)
	credRepo := storage.NewAccountCredentialRepo(db)
	fundingRepo := storage.NewFundingEvidenceRepo(db)
	kr := testKeyring(t)
	now := *clock
	credSvc := application.NewCredentialService(credRepo, kr, func() time.Time { return now })

	const accountID = "acct-seed-1"
	const credID = "cred-seed-1"
	// Insert account row directly (connection_state connected, health healthy).
	if _, err := db.Conn().Exec(
		`INSERT INTO accounts (id, provider_id, external_id, auth_type, connection_state, health_state, identity_email, identity_plan, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		accountID, "prov-a", "ext-1", "api_key", string(domain.ConnectionConnected), string(domain.HealthHealthy),
		"owner@example.com", "free", now.Unix(), now.Unix(),
	); err != nil {
		t.Fatalf("seed account row: %v", err)
	}
	// Store the active credential through CredentialService (the canonical
	// encrypt+persist path reveal will later decrypt through).
	if _, err := credSvc.Store(context.Background(), application.StoreCredentialParams{
		ID:           credID,
		AccountID:    accountID,
		ProviderID:   "prov-a",
		Kind:         domain.CredentialKindAPIKey,
		Active:       true,
		PlaintextKey: canaryKey,
	}); err != nil {
		t.Fatalf("store seed credential: %v", err)
	}
	// Insert the first funding row directly (owner_policy, free, not locked).
	if _, err := db.Conn().Exec(
		`INSERT INTO account_funding_evidence (id, account_id, funding, source, locked, confidence, observed_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"fund-seed-1", accountID, string(domain.FundingFree), string(domain.FundingSourceOwnerPolicy), 0, 1.0, now.Unix(),
	); err != nil {
		t.Fatalf("seed funding row: %v", err)
	}

	audit := newAuditEmitter(db, nil)
	quotaWindowRepo := storage.NewQuotaWindowRepo(db, nil, func() time.Time { return *clock })
	h := NewAccountsHandler(accountRepo, credRepo, fundingRepo, quotaWindowRepo, credSvc, newOperationalSettings(storage.NewSettingsRepo(db)), audit, func() time.Time { return *clock }, fundingIDCounter())
	return h, db, accountID, credID
}

// fundingIDCounter returns a deterministic funding-id minter for tests
// (fund-1, fund-2, ...), so a test can assert on a specific supersession
// outcome.
func fundingIDCounter() func() string {
	n := 0
	return func() string {
		n++
		return "fund-new-" + itoaAccountTest(n)
	}
}

func itoaAccountTest(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// withSession injects a validSession into r's context, as ownerSessionGate
// would. reverifyFreshUntil controls whether IsReverifyFresh reports fresh
// (non-nil and in the future) or stale (nil).
func withSession(r *http.Request, reverifyFreshUntil *time.Time) *http.Request {
	session := validSession{Row: storage.OwnerSessionRow{ReverifyFreshUntil: reverifyFreshUntil}, TokenHash: []byte("test-hash")}
	return r.WithContext(context.WithValue(r.Context(), sessionContextKey{}, session))
}

// newAccountsRequest builds a loopback, allowed-Host request to path with
// the given method and optional body. id is the path value the handlers
// read via r.PathValue("id"); httptest.NewRequest does not itself parse
// {id} patterns, so the path value is set explicitly (the path string is
// still used verbatim, matching how the real ServeMux would route it).
func newAccountsRequest(method, path, id string, body []byte) *http.Request {
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	var req *http.Request
	if reader != nil {
		req = httptest.NewRequest(method, path, reader)
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost
	if id != "" {
		req.SetPathValue("id", id)
	}
	return req
}

// freshReverifyUntil returns a reverifyFreshUntil that is fresh at the
// given now (5 minutes in the future).
func freshReverifyUntil(now time.Time) *time.Time {
	t := now.Add(5 * time.Minute)
	return &t
}

// staleReverifyUntil returns a reverifyFreshUntil already in the past.
func staleReverifyUntil(now time.Time) *time.Time {
	t := now.Add(-1 * time.Minute)
	return &t
}

// ============================================================================
// Reveal — the crown jewel
// ============================================================================

// TestReveal_StaleReverify_RejectedBeforeDecrypt proves SEC-005's
// consumption point: a session whose reverify freshness has expired is
// rejected with reverification_required (401) BEFORE any credential lookup
// or decrypt. RED->restore evidence: temporarily commenting out the
// IsReverifyFresh gate in ServeReveal makes this test fail (the reveal
// would proceed and return 200 with the plaintext instead of 401).
func TestReveal_StaleReverify_RejectedBeforeDecrypt(t *testing.T) {
	clock := fixedAccountTestClock()
	const canary = "CANARY-REVEAL-KEY-9zQ8xK2mVb4Nr7"
	h, db, accountID, _ := newTestAccountsHandlerV2(t, clock, canary)

	req := newAccountsRequest(http.MethodPost, "/api/control/v1/accounts/"+accountID+"/reveal", accountID, nil)
	req = withSession(req, staleReverifyUntil(*clock))
	rec := httptest.NewRecorder()
	h.ServeReveal(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("stale reverify status = %d, want 401 reverification_required; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "reverification_required" {
		t.Fatalf("error code = %q, want reverification_required", code)
	}
	// Canary: the secret must NOT appear in the rejection response.
	assertNoFragment(t, rec.Body.String(), canary, "stale-reverify reveal response body")

	// No reveal audit-success row should exist; exactly one failure row.
	var failCount int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = ? AND result = ?`, AuditActionAccountReveal, AuditResultFailure).Scan(&failCount); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if failCount != 1 {
		t.Fatalf("reveal failure audit rows = %d, want 1", failCount)
	}
}

// TestReveal_FreshReverify_ReturnsPlaintextOnceNoStore proves that on a
// fresh reverify, reveal returns the plaintext ONCE with
// Cache-Control: no-store, and the audit row records only the action +
// account id — never the secret.
func TestReveal_FreshReverify_ReturnsPlaintextOnceNoStore(t *testing.T) {
	clock := fixedAccountTestClock()
	const canary = "CANARY-REVEAL-KEY-9zQ8xK2mVb4Nr7"
	h, db, accountID, _ := newTestAccountsHandlerV2(t, clock, canary)

	req := newAccountsRequest(http.MethodPost, "/api/control/v1/accounts/"+accountID+"/reveal", accountID, nil)
	req = withSession(req, freshReverifyUntil(*clock))
	rec := httptest.NewRecorder()
	h.ServeReveal(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("fresh reverify reveal status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
	if rec.Body.String() != canary {
		t.Fatalf("reveal body = %q, want the plaintext %q (revealed exactly once, verbatim)", rec.Body.String(), canary)
	}

	// The audit row must NOT contain the secret (canary over every column).
	rows, err := db.Conn().Query(`SELECT action, result, entity_id, reason_code FROM audit_events WHERE action = ?`, AuditActionAccountReveal)
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var sawSuccess bool
	for rows.Next() {
		var action, result, entityID string
		var reasonCode sql.NullString
		if err := rows.Scan(&action, &result, &entityID, &reasonCode); err != nil {
			t.Fatalf("scan audit: %v", err)
		}
		assertNoFragment(t, entityID, canary, "audit entity_id")
		if reasonCode.Valid {
			assertNoFragment(t, reasonCode.String, canary, "audit reason_code")
		}
		if result == AuditResultSuccess {
			sawSuccess = true
		}
	}
	if !sawSuccess {
		t.Fatalf("no reveal success audit row recorded")
	}
}

// TestReveal_SecondRevealPastWindowRejected proves that a second reveal
// past the 5-minute reverify window is rejected again (reverify is a
// point-in-time stamp, not a lasting grant).
func TestReveal_SecondRevealPastWindowRejected(t *testing.T) {
	clock := fixedAccountTestClock()
	const canary = "CANARY-REVEAL-KEY-9zQ8xK2mVb4Nr7"
	h, _, accountID, _ := newTestAccountsHandlerV2(t, clock, canary)

	// First reveal: fresh (reverify stamp 5 min in the future of the
	// current clock).
	fresh := freshReverifyUntil(*clock)
	req1 := newAccountsRequest(http.MethodPost, "/api/control/v1/accounts/"+accountID+"/reveal", accountID, nil)
	req1 = withSession(req1, fresh)
	rec1 := httptest.NewRecorder()
	h.ServeReveal(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first reveal status = %d, want 200", rec1.Code)
	}

	// Step the clock past the SAME reverify stamp. The session still
	// carries the original fresh stamp (now in the past relative to the
	// stepped clock), so IsReverifyFresh now reports stale.
	*clock = clock.Add(6 * time.Minute)
	req2 := newAccountsRequest(http.MethodPost, "/api/control/v1/accounts/"+accountID+"/reveal", accountID, nil)
	req2 = withSession(req2, fresh) // same OLD stamp, now stale vs the stepped clock
	rec2 := httptest.NewRecorder()
	h.ServeReveal(rec2, req2)

	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("second reveal (past window) status = %d, want 401 reverification_required; body = %q", rec2.Code, rec2.Body.String())
	}
	if code := decodeErrorCode(t, rec2.Body.Bytes()); code != "reverification_required" {
		t.Fatalf("error code = %q, want reverification_required", code)
	}
}

// TestReveal_RateLimited proves repeated fresh reveals are eventually
// throttled (the defense-in-depth cap on top of the reverify gate).
func TestReveal_RateLimited(t *testing.T) {
	clock := fixedAccountTestClock()
	const canary = "CANARY-REVEAL-KEY-9zQ8xK2mVb4Nr7"
	h, _, accountID, _ := newTestAccountsHandlerV2(t, clock, canary)

	fresh := freshReverifyUntil(*clock)
	var sawLimited bool
	for i := 0; i < revealLimiterMaxPerWindow+5; i++ {
		req := newAccountsRequest(http.MethodPost, "/api/control/v1/accounts/"+accountID+"/reveal", accountID, nil)
		req = withSession(req, fresh)
		rec := httptest.NewRecorder()
		h.ServeReveal(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			if code := decodeErrorCode(t, rec.Body.Bytes()); code != "rate_limited" {
				t.Fatalf("rate-limit code = %q, want rate_limited", code)
			}
			sawLimited = true
			break
		}
	}
	if !sawLimited {
		t.Fatalf("revealed %d times without ever being rate-limited (cap = %d)", revealLimiterMaxPerWindow, revealLimiterMaxPerWindow)
	}
}

// ============================================================================
// Funding override
// ============================================================================

// TestFunding_OwnerOverrideSupersedesAndBecomesCurrent proves an
// owner_override candidate supersedes the current owner_policy row and
// becomes the new current row.
func TestFunding_OwnerOverrideSupersedesAndBecomesCurrent(t *testing.T) {
	clock := fixedAccountTestClock()
	h, db, accountID, _ := newTestAccountsHandlerV2(t, clock, "irrelevant-key")

	body, _ := json.Marshal(map[string]any{"funding": "paid"})
	req := newAccountsRequest(http.MethodPut, "/api/control/v1/accounts/"+accountID+"/funding", accountID, body)
	rec := httptest.NewRecorder()
	h.ServeFunding(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("funding override status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}

	// The new current row must be the owner_override paid row.
	current, ok, err := h.funding.CurrentForAccount(req.Context(), accountID)
	if err != nil || !ok {
		t.Fatalf("read current funding: ok=%v err=%v", ok, err)
	}
	if current.Funding != domain.FundingPaid || current.Source != domain.FundingSourceOwnerOverride {
		t.Fatalf("new current = %s/%s, want paid/owner_override", current.Funding, current.Source)
	}
	// The prior current row must now be superseded.
	var supersededCount int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM account_funding_evidence WHERE account_id = ? AND superseded_at IS NOT NULL`, accountID).Scan(&supersededCount); err != nil {
		t.Fatalf("count superseded: %v", err)
	}
	if supersededCount != 1 {
		t.Fatalf("superseded funding rows = %d, want 1", supersededCount)
	}
}

// TestFunding_LockedProviderPolicy_Returns409 proves a locked
// provider_policy current row rejects the override with funding_locked 409.
func TestFunding_LockedProviderPolicy_Returns409(t *testing.T) {
	clock := fixedAccountTestClock()
	h, db, accountID, _ := newTestAccountsHandlerV2(t, clock, "irrelevant-key")

	// Replace the seeded funding row with a LOCKED provider_policy row.
	if _, err := db.Conn().Exec(
		`UPDATE account_funding_evidence SET source = ?, locked = 1, funding = 'paid' WHERE account_id = ? AND superseded_at IS NULL`,
		string(domain.FundingSourceProviderPolicy), accountID,
	); err != nil {
		t.Fatalf("lock funding row: %v", err)
	}

	body, _ := json.Marshal(map[string]any{"funding": "free"})
	req := newAccountsRequest(http.MethodPut, "/api/control/v1/accounts/"+accountID+"/funding", accountID, body)
	rec := httptest.NewRecorder()
	h.ServeFunding(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("locked funding status = %d, want 409; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "funding_locked" {
		t.Fatalf("error code = %q, want funding_locked", code)
	}
	// The locked row must be unchanged (still the single current row).
	var currentCount int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM account_funding_evidence WHERE account_id = ? AND superseded_at IS NULL`, accountID).Scan(&currentCount); err != nil {
		t.Fatalf("count current: %v", err)
	}
	if currentCount != 1 {
		t.Fatalf("current funding rows after rejected override = %d, want 1 (unchanged)", currentCount)
	}
}

// TestFunding_ExpectedVersionMismatch_Returns412 proves optimistic
// concurrency: a wrong expected_version is rejected with 412.
func TestFunding_ExpectedVersionMismatch_Returns412(t *testing.T) {
	clock := fixedAccountTestClock()
	h, _, accountID, _ := newTestAccountsHandlerV2(t, clock, "irrelevant-key")

	// Supply a deliberately wrong expected_version.
	body, _ := json.Marshal(map[string]any{"funding": "paid", "expected_version": "totally-wrong-token"})
	req := newAccountsRequest(http.MethodPut, "/api/control/v1/accounts/"+accountID+"/funding", accountID, body)
	rec := httptest.NewRecorder()
	h.ServeFunding(rec, req)

	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("version mismatch status = %d, want 412; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "precondition_failed" {
		t.Fatalf("error code = %q, want precondition_failed", code)
	}
}

// TestFunding_ExpectedVersionMatch_Succeeds proves a CORRECT
// expected_version (the token GET /accounts/{id} returns) lets the
// override through.
func TestFunding_ExpectedVersionMatch_Succeeds(t *testing.T) {
	clock := fixedAccountTestClock()
	h, _, accountID, _ := newTestAccountsHandlerV2(t, clock, "irrelevant-key")

	// Read the current row's version token the same way the projection does.
	current, ok, err := h.funding.CurrentForAccount(context.Background(), accountID)
	if err != nil || !ok {
		t.Fatalf("read current funding: ok=%v err=%v", ok, err)
	}
	correctVersion := fundingEvidenceVersionToken(current)

	body, _ := json.Marshal(map[string]any{"funding": "paid", "expected_version": correctVersion})
	req := newAccountsRequest(http.MethodPut, "/api/control/v1/accounts/"+accountID+"/funding", accountID, body)
	rec := httptest.NewRecorder()
	h.ServeFunding(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("version match status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}
}

// ============================================================================
// Soft-disconnect (DELETE)
// ============================================================================

// TestDisconnect_HardDeletesAccountAndCascades proves DELETE removes the
// account entirely: the accounts row is gone and its credential is gone via
// the ON DELETE CASCADE FK — so the provider returns to a pristine
// available/awaiting-connection state (owner decision 2026-08-03, superseding
// the prior soft-disconnect). Audit history (append-only, account-FK-free) is
// retained separately and is not asserted here.
func TestDisconnect_HardDeletesAccountAndCascades(t *testing.T) {
	clock := fixedAccountTestClock()
	const canary = "CANARY-DISC-KEY-7tR3nP9x"
	h, db, accountID, credID := newTestAccountsHandlerV2(t, clock, canary)

	req := newAccountsRequest(http.MethodDelete, "/api/control/v1/accounts/"+accountID, accountID, nil)
	rec := httptest.NewRecorder()
	h.ServeDisconnect(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("disconnect status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}

	// Account row is GONE (hard delete).
	var accountCount int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM accounts WHERE id = ?`, accountID).Scan(&accountCount); err != nil {
		t.Fatalf("count accounts: %v", err)
	}
	if accountCount != 0 {
		t.Fatalf("accounts rows after disconnect = %d, want 0 (hard-deleted)", accountCount)
	}
	// The credential row is GONE too (FK cascade), not merely retired.
	var credCount int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM account_credentials WHERE id = ?`, credID).Scan(&credCount); err != nil {
		t.Fatalf("count credentials: %v", err)
	}
	if credCount != 0 {
		t.Fatalf("credential rows after disconnect = %d, want 0 (cascaded)", credCount)
	}
}

// TestDisconnect_RemovedAccountCannotBeResumed proves that once an account is
// disconnected (removed), it is simply gone — a resume finds no such account
// (404). Returning requires a fresh enrollment, not a resume.
func TestDisconnect_RemovedAccountCannotBeResumed(t *testing.T) {
	clock := fixedAccountTestClock()
	h, _, accountID, _ := newTestAccountsHandlerV2(t, clock, "irrelevant-key")

	// Remove the account.
	delReq := newAccountsRequest(http.MethodDelete, "/api/control/v1/accounts/"+accountID, accountID, nil)
	delRec := httptest.NewRecorder()
	h.ServeDisconnect(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("disconnect status = %d, want 200", delRec.Code)
	}

	// Attempt resume: the account no longer exists.
	resumeReq := newAccountsRequest(http.MethodPost, "/api/control/v1/accounts/"+accountID+"/resume", accountID, nil)
	resumeRec := httptest.NewRecorder()
	h.ServeResume(resumeRec, resumeReq)
	if resumeRec.Code != http.StatusNotFound {
		t.Fatalf("resume-after-remove status = %d, want 404; body = %q", resumeRec.Code, resumeRec.Body.String())
	}
	if code := decodeErrorCode(t, resumeRec.Body.Bytes()); code != "not_found" {
		t.Fatalf("error code = %q, want not_found", code)
	}
}

// ============================================================================
// Stop / resume
// ============================================================================

// TestStopResume_LegalTransitionsSucceed proves connected -> stopped ->
// connected round-trips.
func TestStopResume_LegalTransitionsSucceed(t *testing.T) {
	clock := fixedAccountTestClock()
	h, db, accountID, _ := newTestAccountsHandlerV2(t, clock, "irrelevant-key")

	// connected -> stopped.
	stopReq := newAccountsRequest(http.MethodPost, "/api/control/v1/accounts/"+accountID+"/stop", accountID, nil)
	stopRec := httptest.NewRecorder()
	h.ServeStop(stopRec, stopReq)
	if stopRec.Code != http.StatusOK {
		t.Fatalf("stop status = %d, want 200; body = %q", stopRec.Code, stopRec.Body.String())
	}
	var connState string
	if err := db.Conn().QueryRow(`SELECT connection_state FROM accounts WHERE id = ?`, accountID).Scan(&connState); err != nil {
		t.Fatalf("read state after stop: %v", err)
	}
	if connState != string(domain.ConnectionStopped) {
		t.Fatalf("state after stop = %q, want stopped", connState)
	}

	// stopped -> connected (resume).
	resumeReq := newAccountsRequest(http.MethodPost, "/api/control/v1/accounts/"+accountID+"/resume", accountID, nil)
	resumeRec := httptest.NewRecorder()
	h.ServeResume(resumeRec, resumeReq)
	if resumeRec.Code != http.StatusOK {
		t.Fatalf("resume status = %d, want 200; body = %q", resumeRec.Code, resumeRec.Body.String())
	}
	if err := db.Conn().QueryRow(`SELECT connection_state FROM accounts WHERE id = ?`, accountID).Scan(&connState); err != nil {
		t.Fatalf("read state after resume: %v", err)
	}
	if connState != string(domain.ConnectionConnected) {
		t.Fatalf("state after resume = %q, want connected", connState)
	}
}

// TestResume_AlreadyConnected_IsIllegalTransition proves resuming an
// already-connected account is rejected (and leaves state unchanged).
func TestResume_AlreadyConnected_IsIllegalTransition(t *testing.T) {
	clock := fixedAccountTestClock()
	h, db, accountID, _ := newTestAccountsHandlerV2(t, clock, "irrelevant-key")

	// Account is connected; resume -> connected is illegal.
	req := newAccountsRequest(http.MethodPost, "/api/control/v1/accounts/"+accountID+"/resume", accountID, nil)
	rec := httptest.NewRecorder()
	h.ServeResume(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("resume-connected status = %d, want 409; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "invalid_state" {
		t.Fatalf("error code = %q, want invalid_state", code)
	}
	// State unchanged.
	var connState string
	if err := db.Conn().QueryRow(`SELECT connection_state FROM accounts WHERE id = ?`, accountID).Scan(&connState); err != nil {
		t.Fatalf("read state: %v", err)
	}
	if connState != string(domain.ConnectionConnected) {
		t.Fatalf("state after rejected resume = %q, want connected (unchanged)", connState)
	}
}

// ============================================================================
// Projection (GET list / GET one) — no-secret canary
// ============================================================================

// TestProjection_NeverIncludesCredentialMaterial proves GET /accounts and
// GET /accounts/{id} report display_status + eligibility + funding and
// NEVER any credential material: not the plaintext key, not its sha256
// fingerprint, not the stored ciphertext/nonce/key_id envelope. RED->restore
// evidence: leaking the active credential's fingerprint into any projection
// field makes this test fail (verified during development).
func TestProjection_NeverIncludesCredentialMaterial(t *testing.T) {
	clock := fixedAccountTestClock()
	const canary = "CANARY-PROJ-KEY-5kW2jH8nQ4"
	h, db, accountID, _ := newTestAccountsHandlerV2(t, clock, canary)

	// Read the seeded credential's stored material so the canary covers it
	// too: the plaintext (canary), its fingerprint, and the envelope columns.
	var fp, keyID string
	var nonce, ciphertext []byte
	if err := db.Conn().QueryRow(
		`SELECT fingerprint_sha256, key_id, nonce, ciphertext FROM account_credentials WHERE account_id = ? AND state = 'active'`,
		accountID,
	).Scan(&fp, &keyID, &nonce, &ciphertext); err != nil {
		t.Fatalf("read seeded credential material: %v", err)
	}

	// GET /accounts/{id}
	getReq := newAccountsRequest(http.MethodGet, "/api/control/v1/accounts/"+accountID, accountID, nil)
	getRec := httptest.NewRecorder()
	h.ServeGet(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET one status = %d, want 200; body = %q", getRec.Code, getRec.Body.String())
	}
	getBody := getRec.Body.String()
	assertNoFragment(t, getBody, canary, "GET /accounts/{id} response body")
	assertNoFragment(t, getBody, fp, "GET /accounts/{id} leaked fingerprint")
	assertNoFragment(t, getBody, keyID, "GET /accounts/{id} leaked key_id")
	assertNoFragment(t, getBody, string(ciphertext), "GET /accounts/{id} leaked ciphertext")
	assertNoFragment(t, getBody, string(nonce), "GET /accounts/{id} leaked nonce")

	// Validate the projection carries the expected derived fields.
	var body struct {
		Data accountProjectionJSON `json:"data"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode projection: %v; body = %q", err, getRec.Body.String())
	}
	if body.Data.DisplayStatus != string(domain.DisplayHealthy) {
		t.Fatalf("display_status = %q, want healthy", body.Data.DisplayStatus)
	}
	if body.Data.Funding == nil || body.Data.Funding.Funding != string(domain.FundingFree) {
		t.Fatalf("funding projection = %+v, want free", body.Data.Funding)
	}
	if body.Data.Funding.Version == "" {
		t.Fatalf("funding.version missing on the single-account projection (needed for optimistic concurrency)")
	}
	if !body.Data.Eligibility.Eligible {
		t.Fatalf("eligibility = %+v, want eligible", body.Data.Eligibility)
	}

	// GET /accounts (list).
	listReq := newAccountsRequest(http.MethodGet, "/api/control/v1/accounts", "", nil)
	listRec := httptest.NewRecorder()
	h.ServeList(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET list status = %d, want 200; body = %q", listRec.Code, listRec.Body.String())
	}
	listBody := listRec.Body.String()
	assertNoFragment(t, listBody, canary, "GET /accounts response body")
	assertNoFragment(t, listBody, fp, "GET /accounts leaked fingerprint")
	assertNoFragment(t, listBody, string(ciphertext), "GET /accounts leaked ciphertext")
	assertNoFragment(t, listBody, string(nonce), "GET /accounts leaked nonce")
}

// TestProjection_UnknownAccount_404 proves GET /accounts/{id} for an
// unknown id returns not_found.
func TestProjection_UnknownAccount_404(t *testing.T) {
	clock := fixedAccountTestClock()
	h, _, _, _ := newTestAccountsHandlerV2(t, clock, "irrelevant-key")

	req := newAccountsRequest(http.MethodGet, "/api/control/v1/accounts/does-not-exist", "does-not-exist", nil)
	rec := httptest.NewRecorder()
	h.ServeGet(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown account status = %d, want 404", rec.Code)
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "not_found" {
		t.Fatalf("error code = %q, want not_found", code)
	}
}

// ============================================================================
// Health
// ============================================================================

// TestHealth_TransitionSucceeds proves POST /accounts/{id}/health applies
// the domain health transition and audits it.
func TestHealth_TransitionSucceeds(t *testing.T) {
	clock := fixedAccountTestClock()
	h, db, accountID, _ := newTestAccountsHandlerV2(t, clock, "irrelevant-key")

	body, _ := json.Marshal(map[string]any{"health_state": "degraded"})
	req := newAccountsRequest(http.MethodPost, "/api/control/v1/accounts/"+accountID+"/health", accountID, body)
	rec := httptest.NewRecorder()
	h.ServeHealth(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}
	var hs string
	if err := db.Conn().QueryRow(`SELECT health_state FROM accounts WHERE id = ?`, accountID).Scan(&hs); err != nil {
		t.Fatalf("read health_state: %v", err)
	}
	if hs != string(domain.HealthDegraded) {
		t.Fatalf("health_state = %q, want degraded", hs)
	}

	// Audit row recorded.
	var n int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = ? AND result = ?`, AuditActionAccountHealth, AuditResultSuccess).Scan(&n); err != nil {
		t.Fatalf("count health audit: %v", err)
	}
	if n != 1 {
		t.Fatalf("health success audit rows = %d, want 1", n)
	}
}

// ============================================================================
// Provider sync
// ============================================================================

// TestProviderSync_EmitsAuditAndCountsAccounts proves POST /providers/{id}/sync
// iterates the provider's accounts, audits the action, and returns a count.
func TestProviderSync_EmitsAuditAndCountsAccounts(t *testing.T) {
	clock := fixedAccountTestClock()
	h, db, _, _ := newTestAccountsHandlerV2(t, clock, "irrelevant-key")

	req := newAccountsRequest(http.MethodPost, "/api/control/v1/providers/prov-a/sync", "prov-a", nil)
	rec := httptest.NewRecorder()
	h.ServeProviderSync(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}
	var body struct {
		Data struct {
			Provider string `json:"provider"`
			Synced   int    `json:"synced"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode sync response: %v; body = %q", err, rec.Body.String())
	}
	if body.Data.Provider != "prov-a" || body.Data.Synced != 1 {
		t.Fatalf("sync response = %+v, want provider=prov-a synced=1", body.Data)
	}
	// Audit row recorded against the provider resource.
	var n int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = ? AND result = ?`, AuditActionProviderSync, AuditResultSuccess).Scan(&n); err != nil {
		t.Fatalf("count sync audit: %v", err)
	}
	if n != 1 {
		t.Fatalf("provider_sync success audit rows = %d, want 1", n)
	}
}

// ============================================================================
// Gating — every route is owner-session + CSRF gated via ControlMux
// ============================================================================

// TestControlMux_AccountsList_UnauthenticatedRejected proves the real
// ControlMux composition rejects an unauthenticated GET /accounts with 401.
func TestControlMux_AccountsList_UnauthenticatedRejected(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))

	req := newAuthRequest(t, http.MethodGet, "/api/control/v1/accounts", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /accounts unauthenticated status = %d, want 401", rec.Code)
	}
}

// TestControlMux_AccountReveal_SessionWithoutCSRFRejected proves a
// mutating reveal call with a valid session but no CSRF is rejected 403.
func TestControlMux_AccountReveal_SessionWithoutCSRFRejected(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))
	cookie, _ := setupOwnerWithCSRF(t, mux)

	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/accounts/some-id/reveal", nil)
	req.AddCookie(cookie) // no X-CSRF-Token
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("reveal without CSRF status = %d, want 403", rec.Code)
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "csrf_failed" {
		t.Fatalf("error code = %q, want csrf_failed", code)
	}
}

// ============================================================================
// Quota windows in the accounts projection (P3b-CAPI-QUOTAREAD, enables
// P3b-UI-001)
// ============================================================================

// quotaWindowSeed is the column set a test needs to control when seeding a
// quota_windows row directly for the accounts-projection tests below.
type quotaWindowSeed struct {
	id, accountID, source, unit, windowType, windowKey string
	used, remaining, total, limitValue                 *float64
	reserved                                           float64
	resetAt                                            *int64
	freshness                                          string
	observedAt                                         int64
}

func seedQuotaWindow(t *testing.T, db *storage.DB, s quotaWindowSeed) {
	t.Helper()
	freshness := s.freshness
	if freshness == "" {
		freshness = "fresh"
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO quota_windows
		    (id, account_id, source, unit, window_type, window_key, duration_seconds,
		     used, remaining, total, reserved, limit_value, reset_at, version, confidence,
		     freshness_state, observed_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?, ?, 1, 1.0, ?, ?, ?, ?)`,
		s.id, s.accountID, s.source, s.unit, s.windowType, s.windowKey,
		s.used, s.remaining, s.total, s.reserved, s.limitValue, s.resetAt,
		freshness, s.observedAt, s.observedAt, s.observedAt,
	); err != nil {
		t.Fatalf("seed quota window %s: %v", s.id, err)
	}
}

func f64(v float64) *float64 { return &v }

// TestAccountsList_IncludesQuotaWindows proves GET /accounts, exercised
// through the REAL ControlMux, serializes an account's provider-evidence
// window and its two local-safety windows, each with the server-derived
// state and the exact vocabularies (byte-identical to the frozen Design
// System's unions).
func TestAccountsList_IncludesQuotaWindows(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
	cookie, _ := setupOwnerWithCSRF(t, mux)

	if _, err := db.Conn().Exec(
		`INSERT INTO providers (id, display_name, auth_mode, funding_mode, funding_locked, created_at, updated_at) VALUES (?, ?, 'api_key', 'owner_policy', 0, 0, 0)`,
		"prov-q1", "prov-q1",
	); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	now := time.Now().Unix()
	if _, err := db.Conn().Exec(
		`INSERT INTO accounts (id, provider_id, external_id, auth_type, connection_state, health_state, created_at, updated_at)
		 VALUES (?, ?, ?, 'api_key', 'connected', 'healthy', ?, ?)`,
		"acct-q1", "prov-q1", "ext-q1", now, now,
	); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	seedQuotaWindow(t, db, quotaWindowSeed{
		id: "w-q1-provider", accountID: "acct-q1", source: "provider_evidence", unit: "requests",
		windowType: "rolling", windowKey: "provider:daily",
		used: f64(10), remaining: f64(90), total: f64(100), observedAt: now,
	})
	seedQuotaWindow(t, db, quotaWindowSeed{
		id: "w-q1-ls-concurrency", accountID: "acct-q1", source: "local_safety", unit: "concurrency",
		windowType: "concurrency", windowKey: "local:concurrency",
		limitValue: f64(5), observedAt: now,
	})
	seedQuotaWindow(t, db, quotaWindowSeed{
		id: "w-q1-ls-consumption", accountID: "acct-q1", source: "local_safety", unit: "requests",
		windowType: "estimated_consumption", windowKey: "local:requests",
		limitValue: f64(1000), observedAt: now,
	})

	req := newAuthRequest(t, http.MethodGet, "/api/control/v1/accounts", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /accounts status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}

	var body struct {
		Data struct {
			Accounts []struct {
				ID    string            `json:"id"`
				Quota []quotaWindowJSON `json:"quota"`
			} `json:"accounts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body = %q", err, rec.Body.String())
	}

	var acct *struct {
		ID    string            `json:"id"`
		Quota []quotaWindowJSON `json:"quota"`
	}
	for i := range body.Data.Accounts {
		if body.Data.Accounts[i].ID == "acct-q1" {
			acct = &body.Data.Accounts[i]
		}
	}
	if acct == nil {
		t.Fatalf("acct-q1 not found in accounts list: %+v", body.Data.Accounts)
	}
	if len(acct.Quota) != 3 {
		t.Fatalf("len(quota) = %d, want 3; got %+v", len(acct.Quota), acct.Quota)
	}

	bySource := map[string]quotaWindowJSON{}
	for _, w := range acct.Quota {
		bySource[w.Source] = w
	}
	pe, ok := bySource["provider_evidence"]
	if !ok {
		t.Fatalf("no provider_evidence window in %+v", acct.Quota)
	}
	if pe.State != "available" || pe.Freshness != "fresh" || pe.Unit != "requests" {
		t.Fatalf("provider_evidence window = %+v, want state=available freshness=fresh unit=requests", pe)
	}
	if pe.Used == nil || *pe.Used != 10 || pe.Total == nil || *pe.Total != 100 {
		t.Fatalf("provider_evidence window numerics = %+v, want used=10 total=100", pe)
	}
	if _, ok := bySource["local_safety"]; !ok {
		t.Fatalf("no local_safety window in %+v", acct.Quota)
	}
}

// TestAccountsList_UnknownNumericsSerializeAsNull proves a window with
// unknown used/remaining/total emits JSON null for each — the "never a
// fabricated number" contract at the wire level. Asserted on the raw JSON,
// not just the decoded struct, so a stray 0-default cannot hide behind
// Go's own zero-value decoding.
func TestAccountsList_UnknownNumericsSerializeAsNull(t *testing.T) {
	clock := fixedAccountTestClock()
	h, db, accountID, _ := newTestAccountsHandlerV2(t, clock, "irrelevant-key")

	seedQuotaWindow(t, db, quotaWindowSeed{
		id: "w-unknown", accountID: accountID, source: "provider_evidence", unit: "requests",
		windowType: "rolling", windowKey: "provider:daily",
		used: nil, remaining: nil, total: nil, freshness: "unknown", observedAt: clock.Unix(),
	})

	req := newAccountsRequest(http.MethodGet, "/api/control/v1/accounts", "", nil)
	rec := httptest.NewRecorder()
	h.ServeList(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /accounts status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}
	raw := rec.Body.String()
	if strings.Contains(raw, `"used":0`) || strings.Contains(raw, `"remaining":0`) || strings.Contains(raw, `"total":0`) {
		t.Fatalf("raw body fabricated a 0 for an unknown numeric: %q", raw)
	}

	var body struct {
		Data struct {
			Accounts []struct {
				Quota []quotaWindowJSON `json:"quota"`
			} `json:"accounts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body = %q", err, raw)
	}
	if len(body.Data.Accounts) != 1 || len(body.Data.Accounts[0].Quota) != 1 {
		t.Fatalf("unexpected shape: %+v", body.Data)
	}
	w := body.Data.Accounts[0].Quota[0]
	if w.Used != nil || w.Remaining != nil || w.Total != nil {
		t.Fatalf("window numerics = %+v, want all nil (unknown)", w)
	}
}

// TestAccountsList_StaleWindowIsStateStale proves a window observed 20
// minutes ago serializes state:stale even though its numbers look healthy,
// while a fresh window with the SAME numbers serializes state:available —
// state is derived from server-side staleness, never inferred from the
// numbers alone. Both directions are asserted.
func TestAccountsList_StaleWindowIsStateStale(t *testing.T) {
	clock := fixedAccountTestClock()
	h, db, accountID, _ := newTestAccountsHandlerV2(t, clock, "irrelevant-key")

	seedQuotaWindow(t, db, quotaWindowSeed{
		id: "w-fresh", accountID: accountID, source: "provider_evidence", unit: "requests",
		windowType: "rolling", windowKey: "provider:fresh-window",
		used: f64(10), remaining: f64(90), total: f64(100),
		freshness: "fresh", observedAt: clock.Unix(),
	})
	seedQuotaWindow(t, db, quotaWindowSeed{
		id: "w-stale", accountID: accountID, source: "provider_evidence", unit: "requests",
		windowType: "rolling", windowKey: "provider:stale-window",
		used: f64(10), remaining: f64(90), total: f64(100),
		freshness: "fresh", observedAt: clock.Add(-20 * time.Minute).Unix(),
	})

	req := newAccountsRequest(http.MethodGet, "/api/control/v1/accounts", "", nil)
	rec := httptest.NewRecorder()
	h.ServeList(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /accounts status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}

	var body struct {
		Data struct {
			Accounts []struct {
				Quota []quotaWindowJSON `json:"quota"`
			} `json:"accounts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body = %q", err, rec.Body.String())
	}
	if len(body.Data.Accounts) != 1 {
		t.Fatalf("unexpected account count: %+v", body.Data)
	}
	byKey := map[string]quotaWindowJSON{}
	for _, w := range body.Data.Accounts[0].Quota {
		byKey[w.WindowKey] = w
	}
	fresh, ok := byKey["provider:fresh-window"]
	if !ok || fresh.State != "available" {
		t.Fatalf("fresh window = %+v, want state=available", fresh)
	}
	stale, ok := byKey["provider:stale-window"]
	if !ok || stale.State != "stale" {
		t.Fatalf("stale window (observed 20m ago) = %+v, want state=stale even though numbers look healthy", stale)
	}
}

// TestAccountsList_QuotaIsEmptyArrayNotNull proves an account with no quota
// windows serializes "quota":[], never null.
func TestAccountsList_QuotaIsEmptyArrayNotNull(t *testing.T) {
	clock := fixedAccountTestClock()
	h, _, _, _ := newTestAccountsHandlerV2(t, clock, "irrelevant-key")

	req := newAccountsRequest(http.MethodGet, "/api/control/v1/accounts", "", nil)
	rec := httptest.NewRecorder()
	h.ServeList(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /accounts status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"quota":null`) {
		t.Fatalf("quota serialized as null: %q", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"quota":[]`) {
		t.Fatalf("quota did not serialize as an empty array: %q", rec.Body.String())
	}
}

// TestAccountsList_DoesNotDeadlockWithManyAccounts is the N+1/deadlock
// regression test (constraint: a per-account query issued while the
// accounts cursor is still open deadlocks under SetMaxOpenConns(1)): 25
// accounts, each with a quota window, listed in ONE call under a 10s
// context timeout.
func TestAccountsList_DoesNotDeadlockWithManyAccounts(t *testing.T) {
	db := testControlDB(t)
	if _, err := db.Conn().Exec(
		`INSERT INTO providers (id, display_name, auth_mode, funding_mode, funding_locked, created_at, updated_at) VALUES (?, ?, 'api_key', 'owner_policy', 0, 0, 0)`,
		"prov-many", "prov-many",
	); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	now := time.Now().Unix()
	for i := 0; i < 25; i++ {
		id := "acct-many-" + itoaAccountTest(i)
		if _, err := db.Conn().Exec(
			`INSERT INTO accounts (id, provider_id, external_id, auth_type, connection_state, health_state, created_at, updated_at)
			 VALUES (?, ?, ?, 'api_key', 'connected', 'healthy', ?, ?)`,
			id, "prov-many", id, now, now,
		); err != nil {
			t.Fatalf("seed account %d: %v", i, err)
		}
		seedQuotaWindow(t, db, quotaWindowSeed{
			id: "w-many-" + itoaAccountTest(i), accountID: id, source: "provider_evidence", unit: "requests",
			windowType: "rolling", windowKey: "provider:daily",
			used: f64(1), remaining: f64(99), total: f64(100), observedAt: now,
		})
	}

	accountRepo := storage.NewAccountRepo(db)
	credRepo := storage.NewAccountCredentialRepo(db)
	fundingRepo := storage.NewFundingEvidenceRepo(db)
	quotaWindowRepo := storage.NewQuotaWindowRepo(db, nil, nil)
	kr := testKeyring(t)
	credSvc := application.NewCredentialService(credRepo, kr, nil)
	audit := newAuditEmitter(db, nil)
	h := NewAccountsHandler(accountRepo, credRepo, fundingRepo, quotaWindowRepo, credSvc, newOperationalSettings(storage.NewSettingsRepo(db)), audit, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req := newAccountsRequest(http.MethodGet, "/api/control/v1/accounts", "", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ServeList(rec, req)

	if err := ctx.Err(); err != nil {
		t.Fatalf("context error after ServeList = %v, want nil (no deadlock/timeout)", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /accounts status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}
	var body struct {
		Data struct {
			Accounts []struct {
				Quota []quotaWindowJSON `json:"quota"`
			} `json:"accounts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body = %q", err, rec.Body.String())
	}
	if len(body.Data.Accounts) != 25 {
		t.Fatalf("account count = %d, want 25", len(body.Data.Accounts))
	}
	for _, a := range body.Data.Accounts {
		if len(a.Quota) != 1 {
			t.Fatalf("account quota count = %d, want 1: %+v", len(a.Quota), a)
		}
	}
}

// TestControlMux_AccountFunding_EmitsAuditOnFailure proves the funding
// PUT emits exactly one audit row even on a failure path (unknown account).
func TestControlMux_AccountFunding_EmitsAuditOnFailure(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
	cookie, csrfToken := setupOwnerWithCSRF(t, mux)

	body, _ := json.Marshal(map[string]any{"funding": "paid"})
	req := newAuthRequest(t, http.MethodPut, "/api/control/v1/accounts/does-not-exist/funding", bytes.NewBuffer(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrfToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("funding unknown account status = %d, want 404; body = %q", rec.Code, rec.Body.String())
	}
	var n int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = ?`, AuditActionAccountFunding).Scan(&n); err != nil {
		t.Fatalf("count funding audit: %v", err)
	}
	if n != 1 {
		t.Fatalf("funding failure audit rows = %d, want 1", n)
	}
}

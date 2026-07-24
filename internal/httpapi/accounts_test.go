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
	h := NewAccountsHandler(accountRepo, credRepo, fundingRepo, credSvc, audit, func() time.Time { return *clock }, fundingIDCounter())
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

// TestSoftDisconnect_RetiresCredentialAndDisconnectsNoHardDelete proves
// DELETE soft-disconnects: connection_state = disconnected, every usable
// credential retired, reauth_in_progress cleared, and the account ROW +
// history retained (no hard delete). RED->restore evidence: temporarily
// changing SoftDisconnect to a hard DELETE would make this test fail (the
// account row count would drop to 0).
func TestSoftDisconnect_RetiresCredentialAndDisconnectsNoHardDelete(t *testing.T) {
	clock := fixedAccountTestClock()
	const canary = "CANARY-SOFTDISC-KEY-7tR3nP9x"
	h, db, accountID, credID := newTestAccountsHandlerV2(t, clock, canary)

	req := newAccountsRequest(http.MethodDelete, "/api/control/v1/accounts/"+accountID, accountID, nil)
	rec := httptest.NewRecorder()
	h.ServeDisconnect(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("soft-disconnect status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}

	// Account row retained (NO hard delete).
	var accountCount int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM accounts WHERE id = ?`, accountID).Scan(&accountCount); err != nil {
		t.Fatalf("count accounts: %v", err)
	}
	if accountCount != 1 {
		t.Fatalf("accounts rows after soft-disconnect = %d, want 1 (retained, never hard-deleted)", accountCount)
	}
	// connection_state = disconnected.
	var connState string
	if err := db.Conn().QueryRow(`SELECT connection_state FROM accounts WHERE id = ?`, accountID).Scan(&connState); err != nil {
		t.Fatalf("read connection_state: %v", err)
	}
	if connState != string(domain.ConnectionDisconnected) {
		t.Fatalf("connection_state = %q, want disconnected", connState)
	}
	// The active credential is now retired (retired_at stamped).
	var credState string
	var retiredAt *int64
	if err := db.Conn().QueryRow(`SELECT state, retired_at FROM account_credentials WHERE id = ?`, credID).Scan(&credState, &retiredAt); err != nil {
		t.Fatalf("read credential state: %v", err)
	}
	if credState != string(domain.CredentialRetired) {
		t.Fatalf("credential state = %q, want retired", credState)
	}
	if retiredAt == nil {
		t.Fatalf("retired_at = nil, want a timestamp")
	}
	// The credential row is retained (history preserved).
	var credCount int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM account_credentials WHERE id = ?`, credID).Scan(&credCount); err != nil {
		t.Fatalf("count credentials: %v", err)
	}
	if credCount != 1 {
		t.Fatalf("credential rows after soft-disconnect = %d, want 1 (retained)", credCount)
	}
}

// TestSoftDisconnect_DisconnectedCannotBeResume proves a disconnected
// account cannot be resumed (disconnected -> connected is illegal); it can
// only return via re-enrollment.
func TestSoftDisconnect_DisconnectedCannotBeResume(t *testing.T) {
	clock := fixedAccountTestClock()
	h, _, accountID, _ := newTestAccountsHandlerV2(t, clock, "irrelevant-key")

	// Soft-disconnect first.
	delReq := newAccountsRequest(http.MethodDelete, "/api/control/v1/accounts/"+accountID, accountID, nil)
	delRec := httptest.NewRecorder()
	h.ServeDisconnect(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("soft-disconnect status = %d, want 200", delRec.Code)
	}

	// Attempt resume: must be rejected as an illegal transition.
	resumeReq := newAccountsRequest(http.MethodPost, "/api/control/v1/accounts/"+accountID+"/resume", accountID, nil)
	resumeRec := httptest.NewRecorder()
	h.ServeResume(resumeRec, resumeReq)
	if resumeRec.Code != http.StatusConflict {
		t.Fatalf("resume-after-disconnect status = %d, want 409; body = %q", resumeRec.Code, resumeRec.Body.String())
	}
	if code := decodeErrorCode(t, resumeRec.Body.Bytes()); code != "invalid_state" {
		t.Fatalf("error code = %q, want invalid_state", code)
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

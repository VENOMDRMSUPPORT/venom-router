package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// --- P2b-OBS-001: the audit emitter itself ---

// TestAuditEmitter_Emit_InsertsOneRowWithCorrectCodes proves a single
// Emit call appends exactly one audit_events row carrying the exact
// action/result/resource codes it was given.
func TestAuditEmitter_Emit_InsertsOneRowWithCorrectCodes(t *testing.T) {
	db := testControlDB(t)
	fixedNow := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	emitter := newAuditEmitter(db, observability.Default())
	emitter.now = func() time.Time { return fixedNow }

	emitter.Emit(context.Background(), AuditActionAccountConnect, AuditResultSuccess, AuditResourceAccount, "acct-42", "")

	var count int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&count); err != nil {
		t.Fatalf("count audit_events: %v", err)
	}
	if count != 1 {
		t.Fatalf("audit_events row count = %d, want exactly 1", count)
	}

	var action, entityType, entityID, result string
	var atEpoch int64
	if err := db.Conn().QueryRow(
		`SELECT action, entity_type, entity_id, result, at FROM audit_events`,
	).Scan(&action, &entityType, &entityID, &result, &atEpoch); err != nil {
		t.Fatalf("scan audit_events row: %v", err)
	}
	if action != AuditActionAccountConnect || entityType != AuditResourceAccount || entityID != "acct-42" || result != AuditResultSuccess {
		t.Fatalf("row = (%q, %q, %q, %q), want (%q, %q, acct-42, %q)",
			action, entityType, entityID, result, AuditActionAccountConnect, AuditResourceAccount, AuditResultSuccess)
	}
	if atEpoch != fixedNow.Unix() {
		t.Fatalf("at = %d, want %d", atEpoch, fixedNow.Unix())
	}
}

// TestAuditEmitter_Emit_WriteFailureDoesNotPanicOrBlockCaller proves the
// log-and-continue contract: even given a scenario where the underlying
// repo write cannot succeed (here, a DB that was never migrated so
// audit_events does not exist), Emit does not panic and returns
// normally — the primary operation it is auditing is never disrupted by
// an audit-log failure.
func TestAuditEmitter_Emit_WriteFailureDoesNotPanicOrBlockCaller(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(dir) // deliberately NOT migrated — audit_events does not exist
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	emitter := newAuditEmitter(db, observability.Default())

	// Must not panic.
	emitter.Emit(context.Background(), AuditActionAccountConnect, AuditResultFailure, AuditResourceProvider, "opencode-zen", "internal_error")
}

// --- Canary: a secret-shaped string must never land in an audit_events column ---

// TestAuditEmitter_Emit_CanarySecretNeverPersisted plants a
// secret-shaped string (an api_key=... parameter, the shape
// sanitize.Text specifically recognizes) in the resourceID/reasonCode
// arguments — a hypothetical caller mistake, since every real call site
// only ever passes ids/fixed codes — and proves it never survives into
// the persisted audit_events row.
func TestAuditEmitter_Emit_CanarySecretNeverPersisted(t *testing.T) {
	const canarySecret = "CANARY-7fQ2mZ0kR9xVb3Nc6Ea1Hy8Dc5-audit"
	db := testControlDB(t)
	emitter := newAuditEmitter(db, observability.Default())

	leakedResourceID := "api_key=" + canarySecret
	leakedReasonCode := "token=" + canarySecret

	emitter.Emit(context.Background(), AuditActionAccountConnect, AuditResultFailure, AuditResourceAccount, leakedResourceID, leakedReasonCode)

	rows, err := db.Conn().Query(`SELECT action, entity_type, entity_id, result, reason_code FROM audit_events`)
	if err != nil {
		t.Fatalf("query audit_events: %v", err)
	}
	defer func() { _ = rows.Close() }()

	found := false
	for rows.Next() {
		found = true
		var action, entityType, entityID, result string
		var reasonCode *string
		if err := rows.Scan(&action, &entityType, &entityID, &result, &reasonCode); err != nil {
			t.Fatalf("scan audit_events row: %v", err)
		}
		assertNoFragment(t, entityID, canarySecret, "audit_events.entity_id")
		if reasonCode != nil {
			assertNoFragment(t, *reasonCode, canarySecret, "audit_events.reason_code")
		}
	}
	if !found {
		t.Fatalf("expected at least one audit_events row")
	}
}

// --- Wiring: POST .../oauth/begin and POST .../accounts/{id}/reauth/begin
// each emit exactly one audit_event through ControlMux's shared emitter ---

// TestControlMux_OAuthBegin_EmitsOneAuditEventOnFailure proves
// ServeBegin's not_found branch (empty registry, no OAuth adapter for
// opencode-zen) records exactly one audit_events row with the expected
// action/result/reason codes.
func TestControlMux_OAuthBegin_EmitsOneAuditEventOnFailure(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
	cookie, csrfToken := setupOwnerWithCSRF(t, mux)

	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/providers/opencode-zen/oauth/begin", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrfToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %q", rec.Code, rec.Body.String())
	}

	var count int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = ?`, AuditActionOAuthBegin).Scan(&count); err != nil {
		t.Fatalf("count audit_events: %v", err)
	}
	if count != 1 {
		t.Fatalf("audit_events rows for action %q = %d, want exactly 1", AuditActionOAuthBegin, count)
	}

	var result, reasonCode string
	if err := db.Conn().QueryRow(
		`SELECT result, reason_code FROM audit_events WHERE action = ?`, AuditActionOAuthBegin,
	).Scan(&result, &reasonCode); err != nil {
		t.Fatalf("scan audit_events row: %v", err)
	}
	if result != AuditResultFailure || reasonCode != "not_found" {
		t.Fatalf("result/reason_code = %q/%q, want %q/not_found", result, reasonCode, AuditResultFailure)
	}
}

// TestControlMux_ReauthBegin_EmitsOneAuditEventOnFailure mirrors the
// above for POST .../accounts/{id}/reauth/begin's not_found branch (an
// unknown account id).
func TestControlMux_ReauthBegin_EmitsOneAuditEventOnFailure(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
	cookie, csrfToken := setupOwnerWithCSRF(t, mux)

	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/accounts/does-not-exist/reauth/begin", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrfToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %q", rec.Code, rec.Body.String())
	}

	var count int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = ?`, AuditActionReauthBegin).Scan(&count); err != nil {
		t.Fatalf("count audit_events: %v", err)
	}
	if count != 1 {
		t.Fatalf("audit_events rows for action %q = %d, want exactly 1", AuditActionReauthBegin, count)
	}
}

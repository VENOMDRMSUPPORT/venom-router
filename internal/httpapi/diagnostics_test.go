package httpapi

// diagnostics_test.go exercises the P3b-CAPI-002 reconciliation
// diagnostics surface (internal/httpapi/diagnostics.go). Functional
// tests build a DiagnosticsHandler directly over a fresh migrated DB —
// mirroring newDiscoveryFixture/newQuotaFixture's posture. Owner-gating
// is proved separately through the real ControlMux composition.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

func fixedDiagnosticsClock() time.Time {
	return time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
}

type diagnosticsFixture struct {
	handler        *DiagnosticsHandler
	db             *storage.DB
	reconciliation *storage.ReconciliationRepo
	lifecycle      *storage.QuotaLifecycleRepo
	accountID      string
	windowID       string
}

// newDiagnosticsHTTPFixture seeds a provider + connected account + one
// provider-evidence quota window over a fresh migrated DB, and wires a
// DiagnosticsHandler over it. clock defaults to time.Now (via the
// storage repos' own nil-defaulting) when nil.
func newDiagnosticsHTTPFixture(t *testing.T, clock func() time.Time) *diagnosticsFixture {
	t.Helper()
	db := testControlDB(t)

	const providerID = "prov-diag-http"
	const accountID = "acct-diag-http"
	const windowID = "win-diag-http"

	if _, err := db.Conn().Exec(
		`INSERT INTO providers (id, display_name, auth_mode, funding_mode, funding_locked, created_at, updated_at) VALUES (?, ?, 'api_key', 'owner_policy', 0, 0, 0)`,
		providerID, providerID,
	); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO accounts (id, provider_id, external_id, auth_type, connection_state, health_state, created_at, updated_at)
		 VALUES (?, ?, ?, 'api_key', 'connected', 'healthy', 0, 0)`,
		accountID, providerID, accountID,
	); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO quota_windows (id, account_id, source, unit, window_type, window_key, reserved, version, confidence, freshness_state, observed_at, created_at, updated_at)
		 VALUES (?, ?, 'provider_evidence', 'requests', 'rolling_5h', '5h', 5, 1, 0.9, 'fresh', 0, 0, 0)`,
		windowID, accountID,
	); err != nil {
		t.Fatalf("seed window: %v", err)
	}

	lifecycle := storage.NewQuotaLifecycleRepo(db, clock, nil)
	reconciliation := storage.NewReconciliationRepo(db, clock, quota.DefaultReconciliationPolicy(), lifecycle, nil)
	audit := newAuditEmitter(db, nil)
	handler := NewDiagnosticsHandler(reconciliation, lifecycle, audit)

	return &diagnosticsFixture{
		handler:        handler,
		db:             db,
		reconciliation: reconciliation,
		lifecycle:      lifecycle,
		accountID:      accountID,
		windowID:       windowID,
	}
}

// seedReservation inserts a quota_reservations row plus its single
// allocation (estimated_cost=5, matching the fixture window's
// reserved=5) directly, giving each test full control over
// state/attempts/lease.
func (f *diagnosticsFixture) seedReservation(t *testing.T, id, state string, attempts int, leaseOwner *string, leaseExpiresAt *int64) {
	t.Helper()
	if _, err := f.db.Conn().Exec(
		`INSERT INTO quota_reservations (id, account_id, request_id, attempt_id, state, dispatched_at, expires_at, created_at, reconcile_attempts, lease_owner, lease_expires_at)
		 VALUES (?, ?, ?, ?, ?, 900, 1900, 900, ?, ?, ?)`,
		id, f.accountID, id+"-req", id+"-attempt", state, attempts, leaseOwner, leaseExpiresAt,
	); err != nil {
		t.Fatalf("seed reservation %s: %v", id, err)
	}
	if _, err := f.db.Conn().Exec(
		`INSERT INTO quota_reservation_allocations (reservation_id, window_id, unit, estimated_cost, estimate_source, actual_cost, state)
		 VALUES (?, ?, 'requests', 5, 'from_request', NULL, 'reserved')`,
		id, f.windowID,
	); err != nil {
		t.Fatalf("seed allocation %s: %v", id, err)
	}
}

func (f *diagnosticsFixture) readReservation(t *testing.T, id string) (state string, attempts int64, leaseOwner sql.NullString, leaseExpiresAt sql.NullInt64) {
	t.Helper()
	if err := f.db.Conn().QueryRow(
		`SELECT state, reconcile_attempts, lease_owner, lease_expires_at FROM quota_reservations WHERE id = ?`, id,
	).Scan(&state, &attempts, &leaseOwner, &leaseExpiresAt); err != nil {
		t.Fatalf("read reservation %s: %v", id, err)
	}
	return
}

func (f *diagnosticsFixture) readAllocation(t *testing.T, id string) (state string, actualCost sql.NullFloat64, actualConfidence sql.NullString) {
	t.Helper()
	if err := f.db.Conn().QueryRow(
		`SELECT state, actual_cost, actual_confidence FROM quota_reservation_allocations WHERE reservation_id = ? AND window_id = ?`, id, f.windowID,
	).Scan(&state, &actualCost, &actualConfidence); err != nil {
		t.Fatalf("read allocation %s: %v", id, err)
	}
	return
}

func (f *diagnosticsFixture) readWindow(t *testing.T) (reserved float64, version int64) {
	t.Helper()
	if err := f.db.Conn().QueryRow(`SELECT reserved, version FROM quota_windows WHERE id = ?`, f.windowID).Scan(&reserved, &version); err != nil {
		t.Fatalf("read window: %v", err)
	}
	return
}

func newTestDiagnosticsMux(h *DiagnosticsHandler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/control/v1/diagnostics/reconciliation", h.ServeList)
	mux.HandleFunc("/api/control/v1/diagnostics/reconciliation/{reservation_id}", h.ServeAction)
	return mux
}

func diagnosticsGetRequest(path string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost
	return req
}

func diagnosticsActionRequest(t *testing.T, path, action string) *http.Request {
	t.Helper()
	b, err := json.Marshal(reconciliationActionRequest{Action: action})
	if err != nil {
		t.Fatalf("marshal action request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost
	req.Header.Set("Content-Type", "application/json")
	return req
}

func strPtrHTTP(s string) *string { return &s }
func i64PtrHTTP(v int64) *int64   { return &v }

// --- GET /diagnostics/reconciliation ---

func TestDiagnosticsReconciliation_ListsPendingAndUnknown_OwnerGated(t *testing.T) {
	clock := fixedDiagnosticsClock()
	f := newDiagnosticsHTTPFixture(t, func() time.Time { return clock })
	f.seedReservation(t, "res-pending-1", "reconciliation_pending", 1, nil, nil)
	f.seedReservation(t, "res-unknown-1", "unknown_consumption", 5, nil, nil)
	f.seedReservation(t, "res-reserved-1", "reserved", 0, nil, nil)

	mux := newTestDiagnosticsMux(f.handler)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, diagnosticsGetRequest("/api/control/v1/diagnostics/reconciliation"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}

	var env struct {
		Data []reconciliationItemJSON `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v; body = %q", err, rec.Body.String())
	}
	ids := make(map[string]bool, len(env.Data))
	for _, it := range env.Data {
		ids[it.ReservationID] = true
	}
	if !ids["res-pending-1"] || !ids["res-unknown-1"] {
		t.Fatalf("data = %+v, want both res-pending-1 and res-unknown-1", env.Data)
	}
	if ids["res-reserved-1"] {
		t.Fatalf("data = %+v, want res-reserved-1 absent", env.Data)
	}

	// Owner-gating, through the real ControlMux.
	db2 := testControlDB(t)
	realMux := ControlMux(testAllowedHost, fakeSPA(), db2, testKeyring(t))
	rec2 := httptest.NewRecorder()
	realMux.ServeHTTP(rec2, newAuthRequest(t, http.MethodGet, "/api/control/v1/diagnostics/reconciliation", nil))
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", rec2.Code)
	}
}

// --- POST /diagnostics/reconciliation/{id} ---

func TestDiagnosticsResync_ClearsLeaseAndResetsAttempts(t *testing.T) {
	clock := fixedDiagnosticsClock()
	f := newDiagnosticsHTTPFixture(t, func() time.Time { return clock })
	f.seedReservation(t, "res-resync", "reconciliation_pending", 2, strPtrHTTP("worker-1"), i64PtrHTTP(clock.Unix()+300))
	mux := newTestDiagnosticsMux(f.handler)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, diagnosticsActionRequest(t, "/api/control/v1/diagnostics/reconciliation/res-resync", "resync"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}

	state, attempts, leaseOwner, leaseExpiresAt := f.readReservation(t, "res-resync")
	if state != "reconciliation_pending" {
		t.Fatalf("state = %q, want reconciliation_pending (resync does not change state)", state)
	}
	if attempts != 0 {
		t.Fatalf("reconcile_attempts = %d, want 0 (resync grants a fresh retry budget)", attempts)
	}
	if leaseOwner.Valid || leaseExpiresAt.Valid {
		t.Fatalf("lease = (%v,%v), want both cleared", leaseOwner, leaseExpiresAt)
	}

	allocState, actualCost, actualConfidence := f.readAllocation(t, "res-resync")
	if allocState != "reserved" || actualCost.Valid || actualConfidence.Valid {
		t.Fatalf("allocation = (%q,%v,%v), want unchanged (reserved, NULL, NULL)", allocState, actualCost, actualConfidence)
	}
	reserved, version := f.readWindow(t)
	if reserved != 5 || version != 1 {
		t.Fatalf("window = (reserved=%v,version=%v), want unchanged (5,1)", reserved, version)
	}

	// The re-enqueue is real, not cosmetic: a subsequent ClaimPending
	// (whose selection requires expires_at < now, satisfied by this
	// fixture's expires_at=1900 well before the clock's real epoch) DOES
	// return it now that the lease is clear.
	claimed, err := f.reconciliation.ClaimPending(context.Background(), "worker-2", quota.DefaultLeaseTTL)
	if err != nil {
		t.Fatalf("ClaimPending: %v", err)
	}
	found := false
	for _, c := range claimed {
		if c.ReservationID == "res-resync" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ClaimPending did not re-pick res-resync after resync; claimed = %+v", claimed)
	}
}

func TestDiagnosticsAcceptEstimate_SettlesLowConfidence(t *testing.T) {
	clock := fixedDiagnosticsClock()
	f := newDiagnosticsHTTPFixture(t, func() time.Time { return clock })
	f.seedReservation(t, "res-accept", "reconciliation_pending", 1, nil, nil)
	mux := newTestDiagnosticsMux(f.handler)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, diagnosticsActionRequest(t, "/api/control/v1/diagnostics/reconciliation/res-accept", "accept_estimate"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}

	state, _, _, _ := f.readReservation(t, "res-accept")
	if state != "settled" {
		t.Fatalf("state = %q, want settled", state)
	}
	allocState, actualCost, actualConfidence := f.readAllocation(t, "res-accept")
	if allocState != "settled" {
		t.Fatalf("allocation state = %q, want settled", allocState)
	}
	if !actualConfidence.Valid || actualConfidence.String != "low" {
		t.Fatalf("actual_confidence = %v, want \"low\"", actualConfidence)
	}
	if !actualCost.Valid || actualCost.Float64 != 5 {
		t.Fatalf("actual_cost = %v, want 5 (the estimate)", actualCost)
	}
	reserved, _ := f.readWindow(t)
	if reserved != 0 {
		t.Fatalf("window reserved = %v, want 0 (5 - the settled estimate of 5)", reserved)
	}
}

func TestDiagnosticsActions_TerminalReservationIsRejected(t *testing.T) {
	for _, action := range []string{"resync", "accept_estimate"} {
		t.Run(action, func(t *testing.T) {
			clock := fixedDiagnosticsClock()
			f := newDiagnosticsHTTPFixture(t, func() time.Time { return clock })
			f.seedReservation(t, "res-terminal", "unknown_consumption", 5, nil, nil)
			mux := newTestDiagnosticsMux(f.handler)

			beforeState, beforeAttempts, _, _ := f.readReservation(t, "res-terminal")
			beforeAllocState, beforeActualCost, beforeActualConfidence := f.readAllocation(t, "res-terminal")

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, diagnosticsActionRequest(t, "/api/control/v1/diagnostics/reconciliation/res-terminal", action))
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409; body = %q", rec.Code, rec.Body.String())
			}
			if code := decodeErrorCode(t, rec.Body.Bytes()); code != "reservation_terminal" {
				t.Fatalf("error code = %q, want reservation_terminal", code)
			}

			afterState, afterAttempts, _, _ := f.readReservation(t, "res-terminal")
			afterAllocState, afterActualCost, afterActualConfidence := f.readAllocation(t, "res-terminal")
			if afterState != beforeState || afterAttempts != beforeAttempts {
				t.Fatalf("reservation changed: before=(%q,%d) after=(%q,%d)", beforeState, beforeAttempts, afterState, afterAttempts)
			}
			if afterAllocState != beforeAllocState || afterActualCost != beforeActualCost || afterActualConfidence != beforeActualConfidence {
				t.Fatalf("allocation changed: before=(%q,%v,%v) after=(%q,%v,%v)",
					beforeAllocState, beforeActualCost, beforeActualConfidence, afterAllocState, afterActualCost, afterActualConfidence)
			}
			reserved, version := f.readWindow(t)
			if reserved != 5 || version != 1 {
				t.Fatalf("window changed: reserved=%v version=%v, want unchanged (5,1)", reserved, version)
			}
		})
	}
}

// TestDiagnosticsResponse_ContainsNoContentFields is a canary: the
// serialized JSON must contain no key outside the documented projection
// (ids/units/costs/confidence/states/counts) — no prompt/response
// content field could ever sneak in.
func TestDiagnosticsResponse_ContainsNoContentFields(t *testing.T) {
	clock := fixedDiagnosticsClock()
	f := newDiagnosticsHTTPFixture(t, func() time.Time { return clock })
	f.seedReservation(t, "res-canary", "reconciliation_pending", 1, strPtrHTTP("worker-1"), i64PtrHTTP(clock.Unix()+300))
	mux := newTestDiagnosticsMux(f.handler)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, diagnosticsGetRequest("/api/control/v1/diagnostics/reconciliation"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}

	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data, ok := raw["data"].([]any)
	if !ok || len(data) == 0 {
		t.Fatalf("data = %+v, want a non-empty list", raw["data"])
	}

	allowedItemKeys := map[string]bool{
		"reservation_id": true, "account_id": true, "request_id": true, "attempt_id": true,
		"state": true, "attempts": true, "leased": true, "dispatched_at": true,
		"expires_at": true, "rebaseline_flagged": true, "allocations": true,
	}
	allowedAllocKeys := map[string]bool{
		"window_id": true, "unit": true, "estimated_cost": true,
		"actual_cost": true, "actual_confidence": true, "state": true,
	}

	for _, item := range data {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("item = %+v, not an object", item)
		}
		for k := range m {
			if !allowedItemKeys[k] {
				t.Fatalf("item has undocumented key %q: %+v", k, m)
			}
		}
		allocs, _ := m["allocations"].([]any)
		for _, a := range allocs {
			am, ok := a.(map[string]any)
			if !ok {
				t.Fatalf("allocation = %+v, not an object", a)
			}
			for k := range am {
				if !allowedAllocKeys[k] {
					t.Fatalf("allocation has undocumented key %q: %+v", k, am)
				}
			}
		}
	}
}

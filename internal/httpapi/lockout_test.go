package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func doLogin(t *testing.T, h *AuthHandlers, password string) *httptest.ResponseRecorder {
	t.Helper()
	req := newAuthRequest(t, http.MethodPost, "/api/control/v1/auth/login", setupRequestBody(password))
	rec := httptest.NewRecorder()
	h.ServeLogin(rec, req)
	return rec
}

func TestLockout_Login_LocksAfterFiveConsecutiveFailures(t *testing.T) {
	clock := fixedClockAt2026()
	h, _ := newTestAuthHandlersWithDB(t, &clock)
	setupOwnerDirect(t, h, testSetupPassword)

	for i := 0; i < lockoutThreshold; i++ {
		clock = clock.Add(time.Second)
		rec := doLogin(t, h, "the-wrong-password")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure #%d: status = %d, want 401", i+1, rec.Code)
		}
	}

	// The 6th attempt, even with the CORRECT password, must be locked out.
	clock = clock.Add(time.Second)
	rec := doLogin(t, h, testSetupPassword)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th attempt: status = %d, want 429; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "locked_out" {
		t.Fatalf("error code = %q, want locked_out", code)
	}

	var body struct {
		Error struct {
			RetryAfter int64 `json:"retry_after"`
		} `json:"error"`
	}
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Error.RetryAfter <= 0 {
		t.Fatalf("retry_after = %d, want positive", body.Error.RetryAfter)
	}
}

func TestLockout_Login_ReplayDuringLockoutRejected(t *testing.T) {
	clock := fixedClockAt2026()
	h, _ := newTestAuthHandlersWithDB(t, &clock)
	setupOwnerDirect(t, h, testSetupPassword)

	for i := 0; i < lockoutThreshold; i++ {
		clock = clock.Add(time.Second)
		doLogin(t, h, "the-wrong-password")
	}

	for i := 0; i < 3; i++ {
		clock = clock.Add(time.Second)
		rec := doLogin(t, h, testSetupPassword) // even the RIGHT password
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("replay #%d during lockout: status = %d, want 429", i+1, rec.Code)
		}
	}
}

func TestLockout_Login_SuccessMidWindowResetsStreak(t *testing.T) {
	clock := fixedClockAt2026()
	h, _ := newTestAuthHandlersWithDB(t, &clock)
	setupOwnerDirect(t, h, testSetupPassword)

	// 4 failures — one short of the threshold.
	for i := 0; i < lockoutThreshold-1; i++ {
		clock = clock.Add(time.Second)
		doLogin(t, h, "the-wrong-password")
	}

	// A success resets the streak.
	clock = clock.Add(time.Second)
	successRec := doLogin(t, h, testSetupPassword)
	if successRec.Code != http.StatusOK {
		t.Fatalf("success login: status = %d, want 200; body = %q", successRec.Code, successRec.Body.String())
	}

	// 4 MORE failures after the success must NOT trip the lockout — the
	// streak restarted at the success, so this is only 4 consecutive
	// failures again, not 8.
	for i := 0; i < lockoutThreshold-1; i++ {
		clock = clock.Add(time.Second)
		rec := doLogin(t, h, "the-wrong-password")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("post-success failure #%d: status = %d, want 401 (not yet locked out)", i+1, rec.Code)
		}
	}
}

func TestLockout_Login_LiftsAfterWindowElapses(t *testing.T) {
	clock := fixedClockAt2026()
	h, _ := newTestAuthHandlersWithDB(t, &clock)
	setupOwnerDirect(t, h, testSetupPassword)

	for i := 0; i < lockoutThreshold; i++ {
		clock = clock.Add(time.Second)
		doLogin(t, h, "the-wrong-password")
	}

	lockedRec := doLogin(t, h, testSetupPassword)
	if lockedRec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected lockout, status = %d", lockedRec.Code)
	}

	// Advance the clock past the 15-minute window.
	clock = clock.Add(lockoutWindow + time.Second)
	rec := doLogin(t, h, testSetupPassword)
	if rec.Code != http.StatusOK {
		t.Fatalf("after the window elapses: status = %d, want 200 (lockout must lift); body = %q", rec.Code, rec.Body.String())
	}
}

func TestLockout_Reverify_SharesTheSameThreshold(t *testing.T) {
	clock := fixedClockAt2026()
	h, _ := newTestAuthHandlersWithDB(t, &clock)
	setupRec := setupOwnerDirect(t, h, testSetupPassword)
	cookie := sessionCookieFrom(t, setupRec)
	var setupBody struct {
		CSRFToken string `json:"csrf_token"`
	}
	decodeJSON(t, setupRec.Body.Bytes(), &setupBody)

	for i := 0; i < lockoutThreshold; i++ {
		clock = clock.Add(time.Second)
		rec := doReverify(t, h, cookie, setupBody.CSRFToken, "the-wrong-password")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("reverify failure #%d: status = %d, want 401", i+1, rec.Code)
		}
	}

	clock = clock.Add(time.Second)
	rec := doReverify(t, h, cookie, setupBody.CSRFToken, testSetupPassword)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th reverify attempt: status = %d, want 429", rec.Code)
	}
}

func TestLockout_EveryAttemptWritesOneAuthEvent(t *testing.T) {
	clock := fixedClockAt2026()
	h, db := newTestAuthHandlersWithDB(t, &clock)
	setupOwnerDirect(t, h, testSetupPassword)

	clock = clock.Add(time.Second)
	doLogin(t, h, "wrong-1")
	clock = clock.Add(time.Second)
	doLogin(t, h, testSetupPassword)

	rows, err := db.Conn().QueryContext(context.Background(), `SELECT action, result, reason_code FROM auth_events ORDER BY id`)
	if err != nil {
		t.Fatalf("query auth_events: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var got []struct{ action, result, reasonCode string }
	for rows.Next() {
		var action, result string
		var reasonCode *string
		if err := rows.Scan(&action, &result, &reasonCode); err != nil {
			t.Fatalf("scan: %v", err)
		}
		rc := ""
		if reasonCode != nil {
			rc = *reasonCode
		}
		got = append(got, struct{ action, result, reasonCode string }{action, result, rc})
	}

	if len(got) != 2 {
		t.Fatalf("auth_events row count = %d, want 2 (one per login attempt)", len(got))
	}
	if got[0].action != "login" || got[0].result != "failure" || got[0].reasonCode != "invalid_credentials" {
		t.Fatalf("row 0 = %+v, want {login failure invalid_credentials}", got[0])
	}
	if got[1].action != "login" || got[1].result != "success" {
		t.Fatalf("row 1 = %+v, want {login success}", got[1])
	}
}

// TestLockout_Canary_NoPasswordInAnyAuthEventsRow is SEC-006's crown-
// jewel proof: push a distinctive canary password through both the
// login and reverify failure paths, then assert no fragment of it
// appears in any auth_events column.
func TestLockout_Canary_NoPasswordInAnyAuthEventsRow(t *testing.T) {
	const canaryPassword = "CANARY-SECRET-9f3Kx2Qw8pLm0Zt7Vb4Nr1Hy6Dc5Ea-lockout"

	clock := fixedClockAt2026()
	h, db := newTestAuthHandlersWithDB(t, &clock)
	setupRec := setupOwnerDirect(t, h, testSetupPassword)
	cookie := sessionCookieFrom(t, setupRec)
	var setupBody struct {
		CSRFToken string `json:"csrf_token"`
	}
	decodeJSON(t, setupRec.Body.Bytes(), &setupBody)

	clock = clock.Add(time.Second)
	doLogin(t, h, canaryPassword)
	clock = clock.Add(time.Second)
	doReverify(t, h, cookie, setupBody.CSRFToken, canaryPassword)

	rows, err := db.Conn().QueryContext(context.Background(), `SELECT action, result, reason_code, at FROM auth_events`)
	if err != nil {
		t.Fatalf("query auth_events: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var seen int
	for rows.Next() {
		var action, result, at string
		var reasonCode *string
		if err := rows.Scan(&action, &result, &reasonCode, &at); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen++
		assertNoFragment(t, action, canaryPassword, "auth_events.action")
		assertNoFragment(t, result, canaryPassword, "auth_events.result")
		if reasonCode != nil {
			assertNoFragment(t, *reasonCode, canaryPassword, "auth_events.reason_code")
		}
		assertNoFragment(t, at, canaryPassword, "auth_events.at")
	}
	if seen == 0 {
		t.Fatalf("no auth_events rows were written at all — canary is vacuous")
	}
}

// TestLockout_Canary_MetaProvesDetectorIsReal proves findFragment (and
// therefore assertNoFragment, which the canary test above relies on)
// actually detects a leak, by feeding it output that deliberately
// contains a planted fragment — so the canary test above is not
// vacuously green.
func TestLockout_Canary_MetaProvesDetectorIsReal(t *testing.T) {
	const planted = "PLANTED-CANARY-VALUE-should-be-detected"
	leaked := "debug: raw value observed was " + planted + " during processing"

	frag, found := findFragment(leaked, planted)
	if !found {
		t.Fatalf("findFragment did not detect a deliberately embedded leak — the canary would be vacuously green")
	}
	if len(frag) < minFragmentWindow {
		t.Fatalf("detected fragment %q is shorter than the minimum window %d", frag, minFragmentWindow)
	}

	if _, found := findFragment("nothing secret here", planted); found {
		t.Fatalf("findFragment reported a leak in output containing no fragment of the planted value")
	}
}

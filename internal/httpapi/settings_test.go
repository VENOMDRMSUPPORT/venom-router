package httpapi

// settings_test.go exercises the P2b-CAPI-005 owner-settings surface
// (internal/httpapi/settings.go): GET /settings (defaults on a fresh DB),
// PUT /settings (valid values round-trip + audit), the validation_error
// paths (invalid theme / density), the 405 for other methods, and that GET
// emits no audit row. The handler-direct tests build over a migrated test
// DB + a real auditEmitter (the same posture accounts_test.go /
// enrollment_test.go use); the ControlMux tests prove the route is owner-
// session + CSRF gated.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// newTestSettingsHandler builds a SettingsHandler over a fresh migrated DB
// with an injectable clock, returning the handler and the DB (for direct
// audit-row assertions).
func newTestSettingsHandler(t *testing.T, clock func() time.Time) (*SettingsHandler, *storage.DB) {
	t.Helper()
	db := testControlDB(t)
	audit := newAuditEmitter(db, nil)
	return NewSettingsHandler(storage.NewSettingsRepo(db), audit, clock), db
}

func fixedSettingsClock() time.Time {
	return time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
}

// validSettingsBody returns a fully-valid five-field PUT /settings body
// with the given overrides applied — the base for every PUT test, so each
// rejection test varies exactly one field.
func validSettingsBody(overrides map[string]any) map[string]any {
	body := map[string]any{
		"theme":         "venom-dark",
		"density":       "comfortable",
		"accent":        "mono",
		"radius_px":     6,
		"spacing_scale": 1.0,
	}
	for k, v := range overrides {
		if v == nil {
			delete(body, k)
			continue
		}
		body[k] = v
	}
	return body
}

// settingsRequest builds a loopback, allowed-Host request to /settings with
// the given method and optional JSON body.
func settingsRequest(method string, body map[string]any) *http.Request {
	var req *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		req = httptest.NewRequest(method, "/api/control/v1/settings", bytes.NewReader(b))
	} else {
		req = httptest.NewRequest(method, "/api/control/v1/settings", nil)
	}
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost
	return req
}

// decodeSettingsData decodes the success envelope's data.theme/density.
func decodeSettingsData(t *testing.T, body []byte) (theme, density string) {
	t.Helper()
	var got struct {
		Data struct {
			Theme   string `json:"theme"`
			Density string `json:"density"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode settings response: %v; body = %q", err, body)
	}
	return got.Data.Theme, got.Data.Density
}

// decodeCustomizerData decodes the success envelope's customizer trio
// (data.accent/radius_px/spacing_scale).
func decodeCustomizerData(t *testing.T, body []byte) (accent string, radiusPx int, spacingScale float64) {
	t.Helper()
	var got struct {
		Data struct {
			Accent       string  `json:"accent"`
			RadiusPx     int     `json:"radius_px"`
			SpacingScale float64 `json:"spacing_scale"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode settings response: %v; body = %q", err, body)
	}
	return got.Data.Accent, got.Data.RadiusPx, got.Data.SpacingScale
}

// decodeErrorMessage decodes the failure envelope's error.message — the
// validation tests assert the message NAMES the offending field
// (fail-closed 400 per field, spec Batch B).
func decodeErrorMessage(t *testing.T, body []byte) string {
	t.Helper()
	var got struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode error response: %v; body = %q", err, body)
	}
	return got.Error.Message
}

// --- GET ---

// TestSettings_Get_FreshDB_ReturnsDefaults proves GET /settings on a fresh
// (migrated, empty) DB returns the frozen design-system defaults:
// {"data":{"theme":"venom-dark","density":"comfortable"}}.
func TestSettings_Get_FreshDB_ReturnsDefaults(t *testing.T) {
	h, _ := newTestSettingsHandler(t, nil)

	rec := httptest.NewRecorder()
	h.ServeSettings(rec, settingsRequest(http.MethodGet, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}
	theme, density := decodeSettingsData(t, rec.Body.Bytes())
	if theme != "venom-dark" {
		t.Fatalf("theme = %q, want venom-dark", theme)
	}
	if density != "comfortable" {
		t.Fatalf("density = %q, want comfortable", density)
	}
	accent, radiusPx, spacingScale := decodeCustomizerData(t, rec.Body.Bytes())
	if accent != "mono" {
		t.Fatalf("accent = %q, want mono", accent)
	}
	if radiusPx != 6 {
		t.Fatalf("radius_px = %d, want 6", radiusPx)
	}
	if spacingScale != 1.0 {
		t.Fatalf("spacing_scale = %v, want 1.0", spacingScale)
	}
}

// TestSettings_Get_EmitsNoAudit proves GET emits no audit_event row (reads
// are not audited, like GET /accounts).
func TestSettings_Get_EmitsNoAudit(t *testing.T) {
	h, db := newTestSettingsHandler(t, nil)

	rec := httptest.NewRecorder()
	h.ServeSettings(rec, settingsRequest(http.MethodGet, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}

	var n int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = ?`, AuditActionSettingsUpdate).Scan(&n); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if n != 0 {
		t.Fatalf("GET audit rows = %d, want 0 (reads are not audited)", n)
	}
}

// --- PUT success ---

// TestSettings_Put_Valid_EchoesAndPersists proves PUT /settings with valid
// values (all five fields) echoes them back, a subsequent GET returns
// them, and exactly one audit_events row (settings_update / success) is
// recorded.
func TestSettings_Put_Valid_EchoesAndPersists(t *testing.T) {
	clock := fixedSettingsClock()
	h, db := newTestSettingsHandler(t, func() time.Time { return clock })

	rec := httptest.NewRecorder()
	h.ServeSettings(rec, settingsRequest(http.MethodPut, validSettingsBody(map[string]any{
		"theme":         "venom-light",
		"density":       "compact",
		"accent":        "emerald",
		"radius_px":     12,
		"spacing_scale": 0.85,
	})))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}
	theme, density := decodeSettingsData(t, rec.Body.Bytes())
	if theme != "venom-light" || density != "compact" {
		t.Fatalf("PUT echoed = %s/%s, want venom-light/compact", theme, density)
	}
	accent, radiusPx, spacingScale := decodeCustomizerData(t, rec.Body.Bytes())
	if accent != "emerald" || radiusPx != 12 || spacingScale != 0.85 {
		t.Fatalf("PUT echoed customizer = %s/%d/%v, want emerald/12/0.85", accent, radiusPx, spacingScale)
	}

	// A subsequent GET returns the persisted values.
	getRec := httptest.NewRecorder()
	h.ServeSettings(getRec, settingsRequest(http.MethodGet, nil))
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET after PUT status = %d, want 200", getRec.Code)
	}
	theme, density = decodeSettingsData(t, getRec.Body.Bytes())
	if theme != "venom-light" || density != "compact" {
		t.Fatalf("GET after PUT = %s/%s, want venom-light/compact (persisted)", theme, density)
	}
	accent, radiusPx, spacingScale = decodeCustomizerData(t, getRec.Body.Bytes())
	if accent != "emerald" || radiusPx != 12 || spacingScale != 0.85 {
		t.Fatalf("GET after PUT customizer = %s/%d/%v, want emerald/12/0.85 (persisted)", accent, radiusPx, spacingScale)
	}

	// Exactly one audit_events row, settings_update / success.
	var n int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = ? AND result = ?`, AuditActionSettingsUpdate, AuditResultSuccess).Scan(&n); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if n != 1 {
		t.Fatalf("settings_update success audit rows = %d, want 1", n)
	}
}

// --- PUT validation failures ---

// TestSettings_Put_InvalidTheme proves an invalid theme is rejected with
// 400 validation_error, leaves the DB unchanged, and records exactly one
// failure audit row (reason validation_error).
func TestSettings_Put_InvalidTheme(t *testing.T) {
	h, db := newTestSettingsHandler(t, nil)

	// Seed a known value first so we can prove the DB is unchanged.
	if err := h.settings.Put(context.Background(), "venom-dark", "comfortable", "mono", 6, 1.0, time.Now()); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeSettings(rec, settingsRequest(http.MethodPut, validSettingsBody(map[string]any{
		"theme": "dark", // NOT venom-dark — invalid
	})))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid-theme PUT status = %d, want 400; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "validation_error" {
		t.Fatalf("error code = %q, want validation_error", code)
	}

	// DB unchanged.
	row, err := h.settings.Get(context.Background())
	if err != nil {
		t.Fatalf("Get after invalid PUT: %v", err)
	}
	if row.Theme != "venom-dark" || row.Density != "comfortable" {
		t.Fatalf("DB after invalid PUT = %s/%s, want venom-dark/comfortable (unchanged)", row.Theme, row.Density)
	}

	// Exactly one failure audit row.
	var n int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = ? AND result = ?`, AuditActionSettingsUpdate, AuditResultFailure).Scan(&n); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if n != 1 {
		t.Fatalf("settings_update failure audit rows = %d, want 1", n)
	}
}

// TestSettings_Put_InvalidDensity proves an invalid density is rejected
// with 400 validation_error.
func TestSettings_Put_InvalidDensity(t *testing.T) {
	h, _ := newTestSettingsHandler(t, nil)

	rec := httptest.NewRecorder()
	h.ServeSettings(rec, settingsRequest(http.MethodPut, validSettingsBody(map[string]any{
		"density": "cozy", // invalid
	})))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid-density PUT status = %d, want 400; body = %q", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "validation_error" {
		t.Fatalf("error code = %q, want validation_error", code)
	}
}

// TestSettings_Put_CustomizerRejections proves every invalid customizer
// value is rejected fail-closed with 400 validation_error whose message
// NAMES the offending field, leaves the DB unchanged, and records exactly
// one failure audit row — one rejection case per field (accent enum,
// radius_px integer/range/missing, spacing_scale range/missing).
func TestSettings_Put_CustomizerRejections(t *testing.T) {
	for _, tc := range []struct {
		name      string
		overrides map[string]any
		wantField string
	}{
		{"unknown accent", map[string]any{"accent": "teal"}, "accent"},
		{"missing accent", map[string]any{"accent": nil}, "accent"},
		{"radius_px above range", map[string]any{"radius_px": 17}, "radius_px"},
		{"radius_px below range", map[string]any{"radius_px": -1}, "radius_px"},
		{"radius_px non-integer", map[string]any{"radius_px": 3.5}, "radius_px"},
		{"radius_px missing", map[string]any{"radius_px": nil}, "radius_px"},
		{"spacing_scale above range", map[string]any{"spacing_scale": 1.26}, "spacing_scale"},
		{"spacing_scale below range", map[string]any{"spacing_scale": 0.5}, "spacing_scale"},
		{"spacing_scale missing", map[string]any{"spacing_scale": nil}, "spacing_scale"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, db := newTestSettingsHandler(t, nil)

			// Seed a known-good row so "unchanged" is provable.
			if err := h.settings.Put(context.Background(), "venom-dark", "comfortable", "blue", 8, 1.1, time.Now()); err != nil {
				t.Fatalf("seed settings: %v", err)
			}

			rec := httptest.NewRecorder()
			h.ServeSettings(rec, settingsRequest(http.MethodPut, validSettingsBody(tc.overrides)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %q", rec.Code, rec.Body.String())
			}
			if code := decodeErrorCode(t, rec.Body.Bytes()); code != "validation_error" {
				t.Fatalf("error code = %q, want validation_error", code)
			}
			if msg := decodeErrorMessage(t, rec.Body.Bytes()); !strings.Contains(msg, tc.wantField) {
				t.Fatalf("error message = %q, want it to name the offending field %q", msg, tc.wantField)
			}

			// DB unchanged.
			row, err := h.settings.Get(context.Background())
			if err != nil {
				t.Fatalf("Get after invalid PUT: %v", err)
			}
			if row.Accent != "blue" || row.RadiusPx != 8 || row.SpacingScale != 1.1 {
				t.Fatalf("DB after invalid PUT = %s/%d/%v, want blue/8/1.1 (unchanged)", row.Accent, row.RadiusPx, row.SpacingScale)
			}

			// Exactly one failure audit row.
			var n int
			if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = ? AND result = ?`, AuditActionSettingsUpdate, AuditResultFailure).Scan(&n); err != nil {
				t.Fatalf("count audit: %v", err)
			}
			if n != 1 {
				t.Fatalf("settings_update failure audit rows = %d, want 1", n)
			}
		})
	}
}

// --- method ---

// TestSettings_OtherMethod_405 proves POST/DELETE/etc return 405
// method_not_allowed (one handler serves GET+PUT only).
func TestSettings_OtherMethod_405(t *testing.T) {
	h, _ := newTestSettingsHandler(t, nil)

	for _, method := range []string{http.MethodPost, http.MethodDelete, http.MethodPatch} {
		rec := httptest.NewRecorder()
		h.ServeSettings(rec, settingsRequest(method, nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d, want 405; body = %q", method, rec.Code, rec.Body.String())
		}
		if code := decodeErrorCode(t, rec.Body.Bytes()); code != "method_not_allowed" {
			t.Fatalf("%s error code = %q, want method_not_allowed", method, code)
		}
	}
}

// --- P3a-CAPI-003: PUT /settings/enrichment ---

// enrichmentRequest builds a loopback, allowed-Host request to
// /settings/enrichment with the given method and raw JSON body (nil body
// omits the request body entirely).
func enrichmentRequest(method string, body []byte) *http.Request {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, "/api/control/v1/settings/enrichment", bytes.NewReader(body))
	} else {
		req = httptest.NewRequest(method, "/api/control/v1/settings/enrichment", nil)
	}
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost
	return req
}

func decodeSettingsFull(t *testing.T, body []byte) (theme, density string, enrichmentEnabled bool) {
	t.Helper()
	var got struct {
		Data struct {
			Theme             string `json:"theme"`
			Density           string `json:"density"`
			EnrichmentEnabled bool   `json:"enrichment_enabled"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode settings response: %v; body = %q", err, body)
	}
	return got.Data.Theme, got.Data.Density, got.Data.EnrichmentEnabled
}

// TestSettings_EnrichmentOffByDefault_HTTP proves GET /settings renders
// "enrichment_enabled": false on a fresh DB.
func TestSettings_EnrichmentOffByDefault_HTTP(t *testing.T) {
	h, _ := newTestSettingsHandler(t, nil)

	rec := httptest.NewRecorder()
	h.ServeSettings(rec, settingsRequest(http.MethodGet, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	_, _, enrichmentEnabled := decodeSettingsFull(t, rec.Body.Bytes())
	if enrichmentEnabled {
		t.Fatalf("enrichment_enabled = true on a fresh DB, want false")
	}
}

// TestSettings_PutEnrichmentValidation proves a missing, null, or
// non-boolean `enabled` is rejected 400 validation_error, with no audit
// row and no write.
func TestSettings_PutEnrichmentValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"missing field", []byte(`{}`)},
		{"null", []byte(`{"enabled":null}`)},
		{"string", []byte(`{"enabled":"true"}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, db := newTestSettingsHandler(t, nil)

			rec := httptest.NewRecorder()
			h.ServeEnrichment(rec, enrichmentRequest(http.MethodPut, tc.body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %q", rec.Code, rec.Body.String())
			}
			if code := decodeErrorCode(t, rec.Body.Bytes()); code != "validation_error" {
				t.Fatalf("error code = %q, want validation_error", code)
			}

			var n int
			if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = ?`, AuditActionSettingsUpdate).Scan(&n); err != nil {
				t.Fatalf("count audit: %v", err)
			}
			if n != 0 {
				t.Fatalf("audit rows = %d, want 0 (validation failure)", n)
			}
			if n := countRows(t, db, "owner_settings"); n != 0 {
				t.Fatalf("owner_settings rows = %d, want 0 (no write on validation failure)", n)
			}
		})
	}
}

// TestSettings_PutEnrichmentEmitsExactlyOneAudit proves exactly one
// settings_update/success audit row is recorded on a successful toggle.
func TestSettings_PutEnrichmentEmitsExactlyOneAudit(t *testing.T) {
	h, db := newTestSettingsHandler(t, nil)

	rec := httptest.NewRecorder()
	h.ServeEnrichment(rec, enrichmentRequest(http.MethodPut, []byte(`{"enabled":true}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}

	var n int
	if err := db.Conn().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = ? AND result = ?`, AuditActionSettingsUpdate, AuditResultSuccess).Scan(&n); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if n != 1 {
		t.Fatalf("settings_update success audit rows = %d, want 1", n)
	}
}

// TestSettings_PutEnrichmentPersistsAndPreservesThemeDensity_HTTP proves
// toggling enrichment via the handler persists and a subsequent GET
// /settings still returns the earlier-set theme/density unchanged.
func TestSettings_PutEnrichmentPersistsAndPreservesThemeDensity_HTTP(t *testing.T) {
	h, _ := newTestSettingsHandler(t, nil)

	putRec := httptest.NewRecorder()
	h.ServeSettings(putRec, settingsRequest(http.MethodPut, validSettingsBody(map[string]any{"theme": "venom-hc", "density": "compact"})))
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT /settings status = %d, want 200", putRec.Code)
	}

	enRec := httptest.NewRecorder()
	h.ServeEnrichment(enRec, enrichmentRequest(http.MethodPut, []byte(`{"enabled":true}`)))
	if enRec.Code != http.StatusOK {
		t.Fatalf("PUT /settings/enrichment status = %d, want 200; body = %q", enRec.Code, enRec.Body.String())
	}
	theme, density, enrichmentEnabled := decodeSettingsFull(t, enRec.Body.Bytes())
	if theme != "venom-hc" || density != "compact" {
		t.Fatalf("theme/density after enrichment toggle = %s/%s, want venom-hc/compact (unchanged)", theme, density)
	}
	if !enrichmentEnabled {
		t.Fatalf("enrichment_enabled = false, want true")
	}

	getRec := httptest.NewRecorder()
	h.ServeSettings(getRec, settingsRequest(http.MethodGet, nil))
	theme, density, enrichmentEnabled = decodeSettingsFull(t, getRec.Body.Bytes())
	if theme != "venom-hc" || density != "compact" || !enrichmentEnabled {
		t.Fatalf("GET /settings after enrichment toggle = %s/%s/%v, want venom-hc/compact/true", theme, density, enrichmentEnabled)
	}
}

// TestSettings_EnrichmentToggleDoesNotAffectFreeSafety proves the card's
// mandated cross-check (04 §2b): toggling enrichment never changes a
// single offering's resolved cost fact — the routing-critical free-safety
// pipeline (FreeSafetyResolver) reads nothing this setting controls.
func TestSettings_EnrichmentToggleDoesNotAffectFreeSafety(t *testing.T) {
	settingsHandler, db := newTestSettingsHandler(t, nil)
	modelsHandler := NewModelsHandler(storage.NewCatalogRepo(db), nil)

	modelsSeedOffering(t, db, offeringSeed{
		AccountID: "acct-enrich", ProviderID: "prov-enrich", ProviderModelID: "model-enrich", ModelID: "cm-enrich",
		PricingJSON: modelsStrPtr(`{"cost":{"input":0,"output":0}}`),
	})

	before := httptest.NewRecorder()
	modelsHandler.ServeOfferings(before, modelsRequest(http.MethodGet, "/api/control/v1/offerings?account_id=acct-enrich"))
	if before.Code != http.StatusOK {
		t.Fatalf("before: status = %d, want 200", before.Code)
	}

	enRec := httptest.NewRecorder()
	settingsHandler.ServeEnrichment(enRec, enrichmentRequest(http.MethodPut, []byte(`{"enabled":true}`)))
	if enRec.Code != http.StatusOK {
		t.Fatalf("toggle enrichment: status = %d, want 200", enRec.Code)
	}

	after := httptest.NewRecorder()
	modelsHandler.ServeOfferings(after, modelsRequest(http.MethodGet, "/api/control/v1/offerings?account_id=acct-enrich"))
	if after.Code != http.StatusOK {
		t.Fatalf("after: status = %d, want 200", after.Code)
	}

	type costOnly struct {
		Cost json.RawMessage `json:"cost"`
	}
	var beforeEnv, afterEnv struct {
		Data []costOnly `json:"data"`
	}
	if err := json.Unmarshal(before.Body.Bytes(), &beforeEnv); err != nil {
		t.Fatalf("decode before: %v", err)
	}
	if err := json.Unmarshal(after.Body.Bytes(), &afterEnv); err != nil {
		t.Fatalf("decode after: %v", err)
	}
	if len(beforeEnv.Data) != 1 || len(afterEnv.Data) != 1 {
		t.Fatalf("expected exactly one offering before/after, got %d/%d", len(beforeEnv.Data), len(afterEnv.Data))
	}
	if string(beforeEnv.Data[0].Cost) != string(afterEnv.Data[0].Cost) {
		t.Fatalf("cost changed after toggling enrichment:\nbefore = %s\nafter  = %s", beforeEnv.Data[0].Cost, afterEnv.Data[0].Cost)
	}
}

// --- Gating (ControlMux composition) ---

// TestControlMux_SettingsEnrichment_IsOwnerGatedAndPutOnly proves: no
// session -> 401; GET /settings/enrichment -> 405 (PUT only); a valid
// session + CSRF -> 200; and GET /settings still works (no route
// shadowing between /settings and /settings/enrichment).
func TestControlMux_SettingsEnrichment_IsOwnerGatedAndPutOnly(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, newAuthRequest(t, http.MethodPut, "/api/control/v1/settings/enrichment", bytes.NewBufferString(`{"enabled":true}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated PUT status = %d, want 401", rec.Code)
	}

	cookie, csrfToken := setupOwnerWithCSRF(t, mux)

	getRec := httptest.NewRecorder()
	getReq := newAuthRequest(t, http.MethodGet, "/api/control/v1/settings/enrichment", nil)
	getReq.AddCookie(cookie)
	mux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /settings/enrichment status = %d, want 405", getRec.Code)
	}

	putReq := newAuthRequest(t, http.MethodPut, "/api/control/v1/settings/enrichment", bytes.NewBufferString(`{"enabled":true}`))
	putReq.AddCookie(cookie)
	putReq.Header.Set("X-CSRF-Token", csrfToken)
	putRec := httptest.NewRecorder()
	mux.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT /settings/enrichment status = %d, want 200; body = %q", putRec.Code, putRec.Body.String())
	}

	settingsGetReq := newAuthRequest(t, http.MethodGet, "/api/control/v1/settings", nil)
	settingsGetReq.AddCookie(cookie)
	settingsRec := httptest.NewRecorder()
	mux.ServeHTTP(settingsRec, settingsGetReq)
	if settingsRec.Code != http.StatusOK {
		t.Fatalf("GET /settings status = %d, want 200 (no route shadowing)", settingsRec.Code)
	}
}

// TestControlMux_Settings_UnauthenticatedRejected proves the real
// ControlMux composition rejects an unauthenticated GET /settings with 401.
func TestControlMux_Settings_UnauthenticatedRejected(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))

	req := newAuthRequest(t, http.MethodGet, "/api/control/v1/settings", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /settings unauthenticated status = %d, want 401", rec.Code)
	}
}

// TestControlMux_SettingsPut_SessionWithoutCSRFRejected proves a mutating
// PUT /settings with a valid session but no CSRF token is rejected 403
// before the handler runs.
func TestControlMux_SettingsPut_SessionWithoutCSRFRejected(t *testing.T) {
	mux := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))
	cookie, _ := setupOwnerWithCSRF(t, mux)

	req := newAuthRequest(t, http.MethodPut, "/api/control/v1/settings", nil)
	req.AddCookie(cookie) // no X-CSRF-Token
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("PUT /settings without CSRF status = %d, want 403", rec.Code)
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "csrf_failed" {
		t.Fatalf("error code = %q, want csrf_failed", code)
	}
}

// TestControlMux_SettingsPut_ValidSessionAndCSRF_Succeeds proves a fully
// authenticated + CSRF'd PUT reaches the handler and persists (the
// end-to-end path through the real ControlMux).
func TestControlMux_SettingsPut_ValidSessionAndCSRF_Succeeds(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
	cookie, csrfToken := setupOwnerWithCSRF(t, mux)

	body, _ := json.Marshal(validSettingsBody(map[string]any{
		"theme": "venom-hc", "density": "compact", "accent": "rose", "radius_px": 0, "spacing_scale": 1.25,
	}))
	req := newAuthRequest(t, http.MethodPut, "/api/control/v1/settings", bytes.NewBuffer(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrfToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /settings status = %d, want 200; body = %q", rec.Code, rec.Body.String())
	}
	theme, density := decodeSettingsData(t, rec.Body.Bytes())
	if theme != "venom-hc" || density != "compact" {
		t.Fatalf("PUT /settings echoed = %s/%s, want venom-hc/compact", theme, density)
	}
	accent, radiusPx, spacingScale := decodeCustomizerData(t, rec.Body.Bytes())
	if accent != "rose" || radiusPx != 0 || spacingScale != 1.25 {
		t.Fatalf("PUT /settings echoed customizer = %s/%d/%v, want rose/0/1.25", accent, radiusPx, spacingScale)
	}

	// Persisted at the DB layer.
	repo := storage.NewSettingsRepo(db)
	row, err := repo.Get(context.Background())
	if err != nil {
		t.Fatalf("Get after PUT: %v", err)
	}
	if row.Theme != "venom-hc" || row.Density != "compact" {
		t.Fatalf("DB after PUT = %s/%s, want venom-hc/compact", row.Theme, row.Density)
	}
	if row.Accent != "rose" || row.RadiusPx != 0 || row.SpacingScale != 1.25 {
		t.Fatalf("DB after PUT customizer = %s/%d/%v, want rose/0/1.25", row.Accent, row.RadiusPx, row.SpacingScale)
	}
}

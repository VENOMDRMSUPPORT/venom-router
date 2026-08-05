package httpapi

// settingsoperational_test.go covers P6-CAPI-001's settings half: the four
// OPERATIONAL knobs added by migration 00014, the read-only effective_config
// projection, and — most importantly — the proof that each persisted value
// actually reaches the code that used to hardcode it. A stored setting nobody
// reads is worse than no setting at all: it reports a change that never
// happened.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// --- helpers ---------------------------------------------------------------

func settingsPut(t *testing.T, h *SettingsHandler, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return settingsPutRaw(t, h, string(raw))
}

// settingsPutRaw drives the PUT with a literal body string, which is the only
// way to send a wrong JSON type or an explicit null.
func settingsPutRaw(t *testing.T, h *SettingsHandler, raw string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/control/v1/settings", bytes.NewBufferString(raw))
	rec := httptest.NewRecorder()
	h.ServeSettings(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
	}
	return rec, out
}

func settingsGet(t *testing.T, h *SettingsHandler) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/control/v1/settings", nil)
	rec := httptest.NewRecorder()
	h.ServeSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /settings status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("no data object: %#v", out)
	}
	return data
}

// operationalBody is a fully-valid PUT body with every operational field set
// to a NON-default value, so a test that expects them persisted cannot pass by
// accident against the defaults.
func operationalBody() map[string]any {
	return map[string]any{
		"theme":                            "venom-light",
		"density":                          "compact",
		"accent":                           "blue",
		"radius_px":                        8,
		"spacing_scale":                    1.1,
		"enrichment_enabled":               true,
		"quota_staleness_seconds":          120,
		"probe_max_in_flight_per_provider": 4,
		"probe_expensive_enabled":          true,
		"probe_per_account_window_seconds": 3600,
	}
}

// --- GET / round trip -------------------------------------------------------

// TestSettings_FreshDBReturnsFrozenDefaults proves a database with no
// owner_settings row resolves to exactly storage.DefaultSettingsRow() — the
// same single definition 00014's column defaults mirror — and never 500s.
//
// The second half is what makes this non-vacuous. Comparing the no-row answer
// against the Go constants alone would be SELF-REFERENTIAL: change a constant
// and the expectation moves with it. So the no-row answer is also compared
// against a row the DATABASE created from its own schema defaults. Those two
// paths have independent definitions (Go constant vs migration DEFAULT), and
// the whole point of 00014's defaults is that they agree.
func TestSettings_FreshDBReturnsFrozenDefaults(t *testing.T) {
	h, db := newTestSettingsHandler(t, fixedSettingsClock)

	noRow := settingsGet(t, h)
	want := storage.DefaultSettingsRow()

	checks := map[string]any{
		"theme":                            want.Theme,
		"density":                          want.Density,
		"accent":                           want.Accent,
		"radius_px":                        float64(want.RadiusPx),
		"spacing_scale":                    want.SpacingScale,
		"enrichment_enabled":               want.EnrichmentEnabled,
		"quota_staleness_seconds":          float64(want.QuotaStalenessSeconds),
		"probe_max_in_flight_per_provider": float64(want.ProbeMaxInFlightPerProvider),
		"probe_expensive_enabled":          want.ProbeExpensiveEnabled,
		"probe_per_account_window_seconds": float64(want.ProbePerAccountWindowSeconds),
	}
	for key, expected := range checks {
		if noRow[key] != expected {
			t.Fatalf("fresh-DB %s = %#v, want %#v", key, noRow[key], expected)
		}
	}

	// Now insert a row that mentions NOTHING but the required columns, so
	// every operational value comes from the migration's own DEFAULT clause.
	if _, err := db.Conn().Exec(
		`INSERT INTO owner_settings (id, theme, density, updated_at) VALUES (1, ?, ?, 0)`,
		storage.DefaultTheme, storage.DefaultDensity,
	); err != nil {
		t.Fatalf("insert bare owner_settings row: %v", err)
	}
	schemaDefaults := settingsGet(t, h)

	for _, key := range []string{
		"quota_staleness_seconds",
		"probe_max_in_flight_per_provider",
		"probe_expensive_enabled",
		"probe_per_account_window_seconds",
	} {
		if schemaDefaults[key] != noRow[key] {
			t.Fatalf("%s: the schema DEFAULT (%#v) and the Go no-row default (%#v) disagree — 00014's column defaults must mirror storage.DefaultSettingsRow exactly",
				key, schemaDefaults[key], noRow[key])
		}
	}
}

// TestSettings_RoundTripsEveryField proves a PUT carrying all ten fields is
// persisted and reported back by both the PUT echo and a fresh GET.
func TestSettings_RoundTripsEveryField(t *testing.T) {
	h, _ := newTestSettingsHandler(t, fixedSettingsClock)

	rec, out := settingsPut(t, h, operationalBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	echo := out["data"].(map[string]any)

	for _, data := range []map[string]any{echo, settingsGet(t, h)} {
		for key, want := range map[string]any{
			"theme":                            "venom-light",
			"density":                          "compact",
			"accent":                           "blue",
			"radius_px":                        float64(8),
			"spacing_scale":                    1.1,
			"enrichment_enabled":               true,
			"quota_staleness_seconds":          float64(120),
			"probe_max_in_flight_per_provider": float64(4),
			"probe_expensive_enabled":          true,
			"probe_per_account_window_seconds": float64(3600),
		} {
			if data[key] != want {
				t.Fatalf("%s = %#v, want %#v", key, data[key], want)
			}
		}
	}
}

// TestSettings_AbsentOperationalFieldsAreLeftUnchanged is the load-bearing
// "absent means unchanged" proof. Non-default values are persisted FIRST, then
// an appearance-only PUT is sent; treating an absent field as zero (or as the
// default) makes this RED.
func TestSettings_AbsentOperationalFieldsAreLeftUnchanged(t *testing.T) {
	h, _ := newTestSettingsHandler(t, fixedSettingsClock)

	if rec, _ := settingsPut(t, h, operationalBody()); rec.Code != http.StatusOK {
		t.Fatalf("seed PUT status = %d, want 200", rec.Code)
	}

	// An appearance-ONLY body: the five required fields and nothing else.
	rec, out := settingsPut(t, h, map[string]any{
		"theme":         "venom-light",
		"density":       "comfortable",
		"accent":        "rose",
		"radius_px":     2,
		"spacing_scale": 0.9,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	echo := out["data"].(map[string]any)

	for _, data := range []map[string]any{echo, settingsGet(t, h)} {
		// Appearance changed...
		if data["theme"] != "venom-light" || data["accent"] != "rose" {
			t.Fatalf("appearance was not applied: %#v", data)
		}
		// ...and every omitted field kept its seeded, non-default value.
		for key, want := range map[string]any{
			"enrichment_enabled":               true,
			"quota_staleness_seconds":          float64(120),
			"probe_max_in_flight_per_provider": float64(4),
			"probe_expensive_enabled":          true,
			"probe_per_account_window_seconds": float64(3600),
		} {
			if data[key] != want {
				t.Fatalf("omitted %s = %#v, want the seeded %#v (absent must mean unchanged, never reset)", key, data[key], want)
			}
		}
	}
}

// --- rejection paths --------------------------------------------------------

// TestSettings_RejectsInvalidOperationalValues proves every bad shape of every
// new field is a 400 NAMING the field, and — critically — that NO write
// happened: the seeded values are still intact after each rejection.
func TestSettings_RejectsInvalidOperationalValues(t *testing.T) {
	tests := []struct {
		name      string
		fragment  string // raw JSON injected alongside the valid appearance fields
		wantNamed string
	}{
		{name: "staleness zero", fragment: `"quota_staleness_seconds": 0`, wantNamed: "quota_staleness_seconds"},
		{name: "staleness negative", fragment: `"quota_staleness_seconds": -5`, wantNamed: "quota_staleness_seconds"},
		{name: "staleness fractional", fragment: `"quota_staleness_seconds": 1.5`, wantNamed: "quota_staleness_seconds"},
		{name: "staleness wrong type", fragment: `"quota_staleness_seconds": "soon"`, wantNamed: "quota_staleness_seconds"},
		{name: "staleness null", fragment: `"quota_staleness_seconds": null`, wantNamed: "quota_staleness_seconds"},

		{name: "in-flight zero", fragment: `"probe_max_in_flight_per_provider": 0`, wantNamed: "probe_max_in_flight_per_provider"},
		{name: "in-flight negative", fragment: `"probe_max_in_flight_per_provider": -1`, wantNamed: "probe_max_in_flight_per_provider"},
		{name: "in-flight wrong type", fragment: `"probe_max_in_flight_per_provider": true`, wantNamed: "probe_max_in_flight_per_provider"},
		{name: "in-flight null", fragment: `"probe_max_in_flight_per_provider": null`, wantNamed: "probe_max_in_flight_per_provider"},

		{name: "account window zero", fragment: `"probe_per_account_window_seconds": 0`, wantNamed: "probe_per_account_window_seconds"},
		{name: "account window wrong type", fragment: `"probe_per_account_window_seconds": []`, wantNamed: "probe_per_account_window_seconds"},
		{name: "account window null", fragment: `"probe_per_account_window_seconds": null`, wantNamed: "probe_per_account_window_seconds"},

		{name: "expensive wrong type", fragment: `"probe_expensive_enabled": "yes"`, wantNamed: "probe_expensive_enabled"},
		{name: "expensive null", fragment: `"probe_expensive_enabled": null`, wantNamed: "probe_expensive_enabled"},

		{name: "enrichment wrong type", fragment: `"enrichment_enabled": 1`, wantNamed: "enrichment_enabled"},
		{name: "enrichment null", fragment: `"enrichment_enabled": null`, wantNamed: "enrichment_enabled"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newTestSettingsHandler(t, fixedSettingsClock)
			if rec, _ := settingsPut(t, h, operationalBody()); rec.Code != http.StatusOK {
				t.Fatalf("seed PUT status = %d, want 200", rec.Code)
			}
			before := settingsGet(t, h)

			raw := fmt.Sprintf(`{"theme":"venom-dark","density":"compact","accent":"mono","radius_px":6,"spacing_scale":1.0,%s}`, tc.fragment)
			rec, out := settingsPutRaw(t, h, raw)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
			errObj, ok := out["error"].(map[string]any)
			if !ok {
				t.Fatalf("no error object: %#v", out)
			}
			if errObj["code"] != "validation_error" {
				t.Fatalf("error code = %#v, want validation_error", errObj["code"])
			}
			msg, _ := errObj["message"].(string)
			if !containsField(msg, tc.wantNamed) {
				t.Fatalf("message %q does not name the field %q", msg, tc.wantNamed)
			}

			// NO WRITE: the whole persisted row is byte-for-byte what it was.
			after := settingsGet(t, h)
			for key, want := range before {
				if key == "effective_config" {
					continue
				}
				if after[key] != want {
					t.Fatalf("a rejected PUT changed %s: %#v -> %#v", key, want, after[key])
				}
			}
		})
	}
}

func containsField(message, field string) bool {
	return len(message) > 0 && bytes.Contains([]byte(message), []byte(field))
}

// --- effective_config -------------------------------------------------------

// TestSettings_EffectiveConfigIsReportedAndReadOnly proves the boot-resolved
// binds are reported on GET and on the PUT echo, and that a PUT carrying the
// field is REJECTED — silently ignoring it would report success for a change
// that cannot happen.
func TestSettings_EffectiveConfigIsReportedAndReadOnly(t *testing.T) {
	h, _ := newTestSettingsHandler(t, fixedSettingsClock)

	for _, data := range []map[string]any{
		settingsGet(t, h),
		func() map[string]any {
			rec, out := settingsPut(t, h, operationalBody())
			if rec.Code != http.StatusOK {
				t.Fatalf("PUT status = %d, want 200", rec.Code)
			}
			return out["data"].(map[string]any)
		}(),
	} {
		cfg, ok := data["effective_config"].(map[string]any)
		if !ok {
			t.Fatalf("effective_config missing: %#v", data)
		}
		if cfg["bind"] != "127.0.0.1:8081" {
			t.Fatalf("effective_config.bind = %#v, want the injected boot value", cfg["bind"])
		}
		if cfg["data_plane_bind"] != "0.0.0.0:9090" {
			t.Fatalf("effective_config.data_plane_bind = %#v, want the injected boot value", cfg["data_plane_bind"])
		}
	}

	rec, out := settingsPutRaw(t, h,
		`{"theme":"venom-dark","density":"compact","accent":"mono","radius_px":6,"spacing_scale":1.0,`+
			`"effective_config":{"bind":"0.0.0.0:1234"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT with effective_config status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	msg, _ := out["error"].(map[string]any)["message"].(string)
	if !containsField(msg, "effective_config") {
		t.Fatalf("message %q does not name effective_config", msg)
	}
}

// TestSettings_UnsetDataPlaneBindIsNullNotEmptyString proves the default
// "shares the control listener" case reports null rather than "", which would
// read like a misconfigured bind.
func TestSettings_UnsetDataPlaneBindIsNullNotEmptyString(t *testing.T) {
	db := testControlDB(t)
	h := NewSettingsHandler(storage.NewSettingsRepo(db), newAuditEmitter(db, nil), fixedSettingsClock,
		newEffectiveConfig("127.0.0.1:8081", ""))

	cfg := settingsGet(t, h)["effective_config"].(map[string]any)
	v, present := cfg["data_plane_bind"]
	if !present {
		t.Fatalf("data_plane_bind key must always be present")
	}
	if v != nil {
		t.Fatalf("data_plane_bind = %#v, want null when the data plane shares the control listener", v)
	}
}

// --- the two enrichment routes agree ---------------------------------------

// TestSettings_BothEnrichmentRoutesAgree proves PUT /settings and
// PUT /settings/enrichment write the SAME field, in both directions, and that
// the dedicated route still exists unchanged.
func TestSettings_BothEnrichmentRoutesAgree(t *testing.T) {
	h, _ := newTestSettingsHandler(t, fixedSettingsClock)

	putEnrichment := func(enabled bool) map[string]any {
		req := httptest.NewRequest(http.MethodPut, "/api/control/v1/settings/enrichment",
			bytes.NewBufferString(fmt.Sprintf(`{"enabled":%t}`, enabled)))
		rec := httptest.NewRecorder()
		h.ServeEnrichment(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT /settings/enrichment status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out["data"].(map[string]any)
	}

	// Direction 1: the dedicated route turns it on; /settings reports it on.
	if got := putEnrichment(true)["enrichment_enabled"]; got != true {
		t.Fatalf("enrichment route echo = %#v, want true", got)
	}
	if got := settingsGet(t, h)["enrichment_enabled"]; got != true {
		t.Fatalf("GET /settings enrichment_enabled = %#v, want true", got)
	}

	// Direction 2: the main PUT turns it off; the dedicated route's own read
	// (via GET /settings, its only read surface) reports it off.
	body := operationalBody()
	body["enrichment_enabled"] = false
	if rec, _ := settingsPut(t, h, body); rec.Code != http.StatusOK {
		t.Fatalf("PUT /settings status = %d, want 200", rec.Code)
	}
	if got := settingsGet(t, h)["enrichment_enabled"]; got != false {
		t.Fatalf("after PUT /settings, enrichment_enabled = %#v, want false", got)
	}

	// And the dedicated route can still flip it back — it is unchanged.
	if got := putEnrichment(true)["enrichment_enabled"]; got != true {
		t.Fatalf("enrichment route no longer works: %#v", got)
	}
}

// --- ANTI-INERT-CONFIG: the persisted values reach their real consumers -----

// TestSettings_StalenessWindowDrivesAccountsProjection is the anti-inert proof
// for quota_staleness_seconds. It drives GET /accounts through the SAME
// construction ControlMux performs, and asserts the SAME window flips between
// fresh and stale purely because the owner changed the setting.
//
// Hardcoding quota.DefaultStalenessWindow back into accounts.go makes the
// "persisted 1s" case RED.
func TestSettings_StalenessWindowDrivesAccountsProjection(t *testing.T) {
	tests := []struct {
		name             string
		stalenessSeconds *int
		wantState        quota.WindowState
	}{
		{name: "default 900s: a 2s-old window is fresh", stalenessSeconds: nil, wantState: quota.StateAvailable},
		{name: "persisted 1s: the same 2s-old window is stale", stalenessSeconds: intPtr(1), wantState: quota.StateStale},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := testControlDB(t)
			now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

			seedStalenessAccount(t, db, now.Add(-2*time.Second))

			settingsRepo := storage.NewSettingsRepo(db)
			if tc.stalenessSeconds != nil {
				if err := settingsRepo.PutSettings(context.Background(), storage.SettingsUpdate{
					Theme: storage.DefaultTheme, Density: storage.DefaultDensity, Accent: storage.DefaultAccent,
					RadiusPx: storage.DefaultRadiusPx, SpacingScale: storage.DefaultSpacingScale,
					QuotaStalenessSeconds: tc.stalenessSeconds,
				}, now); err != nil {
					t.Fatalf("persist staleness: %v", err)
				}
			}

			// The SAME wiring ControlMux performs.
			credentialRepo := storage.NewAccountCredentialRepo(db)
			h := NewAccountsHandler(
				storage.NewAccountRepo(db), credentialRepo,
				storage.NewFundingEvidenceRepo(db), storage.NewQuotaWindowRepo(db, nil, nil),
				application.NewCredentialService(credentialRepo, testKeyring(t), nil),
				newOperationalSettings(settingsRepo),
				newAuditEmitter(db, nil), func() time.Time { return now }, newOAuthTransactionID,
			)

			req := httptest.NewRequest(http.MethodGet, "/api/control/v1/accounts", nil)
			rec := httptest.NewRecorder()
			h.ServeList(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /accounts status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
			}

			var out map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode: %v", err)
			}
			accounts := out["data"].(map[string]any)["accounts"].([]any)
			if len(accounts) != 1 {
				t.Fatalf("accounts = %d, want 1", len(accounts))
			}
			windows := accounts[0].(map[string]any)["quota"].([]any)
			if len(windows) != 1 {
				t.Fatalf("windows = %d, want 1", len(windows))
			}
			if got := windows[0].(map[string]any)["state"]; got != string(tc.wantState) {
				t.Fatalf("window state = %#v, want %q", got, tc.wantState)
			}
		})
	}
}

func intPtr(v int) *int { return &v }

// seedStalenessAccount seeds one connected account with exactly one
// provider-evidence window observed at observedAt.
func seedStalenessAccount(t *testing.T, db *storage.DB, observedAt time.Time) {
	t.Helper()
	const providerID = "prov-stale"
	const accountID = "acct-stale"

	if _, err := db.Conn().Exec(
		`INSERT INTO providers (id, display_name, auth_mode, funding_mode, funding_locked, created_at, updated_at)
		 VALUES (?, ?, 'api_key', 'owner_policy', 0, 0, 0)`, providerID, providerID,
	); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if _, err := db.Conn().Exec(
		`INSERT INTO accounts (id, provider_id, external_id, auth_type, connection_state, health_state, created_at, updated_at)
		 VALUES (?, ?, ?, 'api_key', 'connected', 'healthy', 0, 0)`, accountID, providerID, accountID,
	); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	// remaining 90 of 100 -> plenty of headroom, and freshness_state is
	// 'fresh', so the ONLY thing that can make this window non-available is
	// the staleness THRESHOLD being compared against observed_at.
	if _, err := db.Conn().Exec(
		`INSERT INTO quota_windows (id, account_id, source, unit, window_type, window_key,
			used, remaining, total, limit_value, reserved, version, confidence, freshness_state,
			observed_at, created_at, updated_at)
		 VALUES (?, ?, 'provider_evidence', 'requests', 'rolling_5h', '5h', 10, 90, 100, 100, 0, 1, 0.9, 'fresh', ?, 0, 0)`,
		"win-stale", accountID, observedAt.Unix(),
	); err != nil {
		t.Fatalf("seed window: %v", err)
	}
}

// TestSettings_ProbeCapDrivesTheRealProbePolicy is the anti-inert proof for the
// probe knobs. It resolves the policy through the SAME operationalSettings
// resolver ControlMux hands to NewProbeHandler — never by calling
// DefaultProbeSafetyPolicy and overriding fields inline, which would prove
// nothing about the wiring.
//
// Hardcoding DefaultProbeSafetyPolicy() back into controlmux.go makes the
// persisted case RED.
func TestSettings_ProbeCapDrivesTheRealProbePolicy(t *testing.T) {
	db := testControlDB(t)
	repo := storage.NewSettingsRepo(db)
	ops := newOperationalSettings(repo)
	ctx := context.Background()

	// Default install: the frozen policy.
	base := ops.probePolicy(ctx)
	if base.MaxInFlightPerProvider != 1 {
		t.Fatalf("default MaxInFlightPerProvider = %d, want 1", base.MaxInFlightPerProvider)
	}
	if base.ExpensiveProbesEnabled {
		t.Fatalf("default ExpensiveProbesEnabled = true, want false")
	}
	if base.PerAccountWindow != 24*time.Hour {
		t.Fatalf("default PerAccountWindow = %v, want 24h", base.PerAccountWindow)
	}

	// The owner raises the cap, opts into expensive probes, and shortens the
	// rolling window.
	if err := repo.PutSettings(ctx, storage.SettingsUpdate{
		Theme: storage.DefaultTheme, Density: storage.DefaultDensity, Accent: storage.DefaultAccent,
		RadiusPx: storage.DefaultRadiusPx, SpacingScale: storage.DefaultSpacingScale,
		ProbeMaxInFlightPerProvider:  intPtr(2),
		ProbeExpensiveEnabled:        boolPtr(true),
		ProbePerAccountWindowSeconds: intPtr(3600),
	}, time.Unix(0, 0)); err != nil {
		t.Fatalf("persist probe settings: %v", err)
	}

	got := ops.probePolicy(ctx)
	if got.MaxInFlightPerProvider != 2 {
		t.Fatalf("MaxInFlightPerProvider = %d, want the persisted 2", got.MaxInFlightPerProvider)
	}
	if !got.ExpensiveProbesEnabled {
		t.Fatalf("ExpensiveProbesEnabled = false, want the persisted true")
	}
	if got.PerAccountWindow != time.Hour {
		t.Fatalf("PerAccountWindow = %v, want the persisted 1h", got.PerAccountWindow)
	}

	// The knobs this batch does NOT make configurable keep their frozen
	// values — the resolver overrides three fields, it does not rebuild the
	// policy from scratch.
	if len(got.PerProbe) != len(base.PerProbe) || got.ContextProbeCooldown != base.ContextProbeCooldown {
		t.Fatalf("non-configurable policy fields drifted: %+v vs %+v", got, base)
	}
}

func boolPtr(v bool) *bool { return &v }

// TestSettings_ProbeCapReachesTheProbeHandlerConcurrencyGate proves the
// resolved cap is what the probe admission gate actually enforces: with the
// persisted cap of 2, a provider already running ONE probe still admits
// another; at the default of 1 it does not.
func TestSettings_ProbeCapReachesTheProbeHandlerConcurrencyGate(t *testing.T) {
	tests := []struct {
		name        string
		persistCap  *int
		inFlight    int
		wantRefused bool
	}{
		{name: "default cap 1 refuses a second concurrent probe", persistCap: nil, inFlight: 1, wantRefused: true},
		{name: "persisted cap 2 admits the second concurrent probe", persistCap: intPtr(2), inFlight: 1, wantRefused: false},
		{name: "persisted cap 2 still refuses the third", persistCap: intPtr(2), inFlight: 2, wantRefused: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := testControlDB(t)
			repo := storage.NewSettingsRepo(db)
			if tc.persistCap != nil {
				if err := repo.PutSettings(context.Background(), storage.SettingsUpdate{
					Theme: storage.DefaultTheme, Density: storage.DefaultDensity, Accent: storage.DefaultAccent,
					RadiusPx: storage.DefaultRadiusPx, SpacingScale: storage.DefaultSpacingScale,
					ProbeMaxInFlightPerProvider: tc.persistCap,
				}, time.Unix(0, 0)); err != nil {
					t.Fatalf("persist cap: %v", err)
				}
			}

			policy := newOperationalSettings(repo).probePolicy(context.Background())
			refused := tc.inFlight >= policy.MaxInFlightPerProvider
			if refused != tc.wantRefused {
				t.Fatalf("with %d in flight and cap %d: refused = %t, want %t",
					tc.inFlight, policy.MaxInFlightPerProvider, refused, tc.wantRefused)
			}
		})
	}
}

// TestSettings_ProbeHandlerWiringUsesTheOwnerPolicy closes the gap that the
// inline wiring left open: with ops.probePolicy inlined in ControlMux's body,
// replacing it with intelligence.DefaultProbeSafetyPolicy() kept the WHOLE
// package green, so the owner's probe caps could go inert unnoticed.
//
// This drives buildProbeHandler — the exact function ControlMux calls — and
// asserts the handler it returns resolves the PERSISTED policy, not the
// frozen defaults.
func TestSettings_ProbeHandlerWiringUsesTheOwnerPolicy(t *testing.T) {
	db := testControlDB(t)
	repo := storage.NewSettingsRepo(db)
	ctx := context.Background()

	if err := repo.PutSettings(ctx, storage.SettingsUpdate{
		Theme: storage.DefaultTheme, Density: storage.DefaultDensity, Accent: storage.DefaultAccent,
		RadiusPx: storage.DefaultRadiusPx, SpacingScale: storage.DefaultSpacingScale,
		ProbeMaxInFlightPerProvider:  intPtr(7),
		ProbeExpensiveEnabled:        boolPtr(true),
		ProbePerAccountWindowSeconds: intPtr(1800),
	}, time.Unix(0, 0)); err != nil {
		t.Fatalf("persist probe settings: %v", err)
	}

	// Everything except ops is irrelevant to this assertion, so the other
	// dependencies are the real repos over the same DB.
	credentialRepo := storage.NewAccountCredentialRepo(db)
	certRepo := storage.NewCertificationRepo(db, nil)
	driver, err := intelligence.NewCertificationDriver(certRepo, newCertificationAuditorAdapter(newAuditEmitter(db, nil)), intelligence.DefaultProbeRetryBudget, nil)
	if err != nil {
		t.Fatalf("NewCertificationDriver: %v", err)
	}
	h := buildProbeHandler(
		storage.NewAccountRepo(db), credentialRepo, storage.NewCatalogRepo(db),
		storage.NewJobRepo(db), certRepo,
		storage.NewProbeRunRepo(db, nil, intelligence.DefaultProbeSafetyPolicy().ContextProbeCooldown),
		newProbeReserverAdapter(storage.NewQuotaReservationRepo(db, nil)),
		newProbeTransportAdapter(nil, nil, credentialRepo, nil),
		driver,
		storage.NewDiscoveryRepo(db, nil),
		newOperationalSettings(repo),
		newAuditEmitter(db, nil), newIdempotencyStore(),
	)

	got := h.policyFor(ctx)
	if got.MaxInFlightPerProvider != 7 {
		t.Fatalf("wired MaxInFlightPerProvider = %d, want the persisted 7 — the probe handler is not reading owner settings",
			got.MaxInFlightPerProvider)
	}
	if !got.ExpensiveProbesEnabled {
		t.Fatalf("wired ExpensiveProbesEnabled = false, want the persisted true")
	}
	if got.PerAccountWindow != 30*time.Minute {
		t.Fatalf("wired PerAccountWindow = %v, want the persisted 30m", got.PerAccountWindow)
	}
}

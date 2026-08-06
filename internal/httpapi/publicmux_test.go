package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/platform"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// TestPublicMux_NoControlSurface proves the standalone data-plane mux serves
// ONLY /v1/* — every control path, /health, and the SPA root return 404 (a
// probe of the public bind cannot even learn a control surface exists).
//
// Mutation U2-M5: mount the SPA / control routes on the public-only mux → one
// of these paths stops 404-ing → RED.
func TestPublicMux_NoControlSurface(t *testing.T) {
	db := testControlDB(t)
	mux := PublicMux(db, nil, nil, func() time.Time { return vkFixedNow })

	for _, path := range []string{
		"/api/control/v1/auth/login",
		"/api/control/v1/accounts",
		"/health",
		"/",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("public mux GET %s status = %d, want 404 (no control surface on the data plane)", path, rec.Code)
		}
	}
}

// TestControlMux_V1IsVKGatedNotOwnerSession proves that on the shared control
// mux the /v1 surface is authenticated by a vk key, NOT the owner session:
//   - a valid vk key alone (no owner cookie) reaches 200;
//   - an owner session alone (no vk key) is rejected 401 invalid_api_key.
//
// Mutation U2-M5 (mount control/SPA on public mux) is proved by the test
// above; this one guards the reverse overlap direction.
func TestControlMux_V1IsVKGatedNotOwnerSession(t *testing.T) {
	db := testControlDB(t)
	mux := ControlMux(testAllowedHost, fakeSPA(), db, testKeyring(t))
	seedAPIKey(t, db, "k-1", "vk_live_ctrl0000", nil, false)

	// Valid vk key alone → 200 (never owner-session gated).
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = testAllowedHost
	req.Header.Set("Authorization", "Bearer vk_live_ctrl0000")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("vk key alone on /v1/models = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}

	// Owner session alone (valid cookie, no vk key) → 401: an owner session
	// does NOT authenticate the data plane.
	cookie, _ := setupOwnerWithCSRF(t, mux)
	req2 := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req2.RemoteAddr = "127.0.0.1:54321"
	req2.Host = testAllowedHost
	req2.AddCookie(cookie)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("owner session alone on /v1/models = %d, want 401 (session must not authenticate /v1)", rec2.Code)
	}
	if code := decodeErrorCode(t, rec2.Body.Bytes()); code != publicErrInvalidAPIKey {
		t.Fatalf("owner-session-alone error code = %q, want %q", code, publicErrInvalidAPIKey)
	}
}

// decodeModelsList runs the DB-free models handler directly and returns the
// parsed OpenAI-compatible list.
func decodeModelsList(t *testing.T) (object string, ids []string, entries []map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	newModelsListHandler().ServeModels(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/models = %d, want 200", rec.Code)
	}
	var env struct {
		Object string           `json:"object"`
		Data   []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode models list: %v; body=%q", err, rec.Body.String())
	}
	for _, e := range env.Data {
		id, _ := e["id"].(string)
		ids = append(ids, id)
	}
	return env.Object, ids, env.Data
}

// TestModels_ExactlyThreeTiersInOrder proves GET /v1/models returns the
// OpenAI-compatible list shape with EXACTLY the three tier ids in the fixed
// order venom/lite, venom/pro, venom/max — stably across repeated calls.
//
// Mutation U4-M1: add a fourth model id to publicTierOrder → four entries → RED.
// Mutation U4-M4: range a map instead of the ordered slice → the order varies
// across the repeated calls → RED.
func TestModels_ExactlyThreeTiersInOrder(t *testing.T) {
	want := []string{"venom/lite", "venom/pro", "venom/max"}
	for i := 0; i < 8; i++ { // repeat so a non-deterministic (map-ranged) order is caught
		object, ids, entries := decodeModelsList(t)
		if object != "list" {
			t.Fatalf("object = %q, want \"list\"", object)
		}
		if len(ids) != 3 {
			t.Fatalf("got %d models, want exactly 3: %v", len(ids), ids)
		}
		for j := range want {
			if ids[j] != want[j] {
				t.Fatalf("model order = %v, want %v (deterministic)", ids, want)
			}
		}
		for _, e := range entries {
			if e["object"] != "model" {
				t.Fatalf("entry object = %v, want \"model\"", e["object"])
			}
		}
	}
}

// TestModels_NoFleetInternals proves the response leaks no fleet internals: no
// provider slug, no account external id, and every id is a venom/ tier name —
// even though the test DB really contains those provider/account rows.
//
// Mutation U4-M2: return a provider's raw model id alongside the tiers → the
// provider slug appears in the body (or an id stops being venom/-prefixed) → RED.
func TestModels_NoFleetInternals(t *testing.T) {
	db := testControlDB(t)
	if err := storage.SeedProviders(context.Background(), db, providers.BuiltinCatalog(), time.Unix(0, 0)); err != nil {
		t.Fatalf("seed providers: %v", err)
	}
	const canaryExternalID = "acct-canary-ext-9f3a"
	if _, err := db.Conn().Exec(
		`INSERT INTO accounts (id, provider_id, external_id, auth_type, created_at, updated_at) VALUES (?, ?, ?, 'api_key', 0, 0)`,
		"acct-1", string(providers.OpenCodeZenID), canaryExternalID,
	); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	mux := PublicMux(db, nil, nil, func() time.Time { return vkFixedNow })
	seedAPIKey(t, db, "k-1", "vk_live_models00", nil, false)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer vk_live_models00")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/models = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	for _, forbidden := range []string{string(providers.OpenCodeZenID), canaryExternalID} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("/v1/models leaked a fleet internal %q: %s", forbidden, body)
		}
	}
	_, ids, entries := decodeModelsList(t)
	for _, id := range ids {
		if !strings.HasPrefix(id, "venom/") {
			t.Fatalf("model id %q is not a venom/ tier name — a provider/raw model id leaked", id)
		}
	}

	// THE FIELD SET IS FROZEN. Governor review found the two checks above
	// insufficient: adding a `provider_model: "gpt-5-turbo-2026"` field to every
	// entry left this test GREEN, because the fabricated value matched neither
	// seeded fixture and the `id` field stayed venom/-prefixed. The card's DoD is
	// that NO provider/account/raw-model identifier is exposed at all, so the
	// public projection's shape — not merely today's fixture values — is what must
	// be pinned. Any new field is a deliberate review decision (the same
	// frozen-set discipline the M7 column-set test uses).
	allowed := map[string]bool{"id": true, "object": true, "created": true, "owned_by": true}
	for _, e := range entries {
		for field := range e {
			if !allowed[field] {
				t.Fatalf("/v1/models entry carries unexpected field %q — the public projection's field set is frozen; a new field must be reviewed for fleet-internal leakage (entry=%v)", field, e)
			}
		}
		if len(e) != len(allowed) {
			t.Fatalf("/v1/models entry field count = %d, want exactly %d (%v)", len(e), len(allowed), e)
		}
	}
}

// TestModels_RequiresVKAuth proves /v1/models is vk-gated: no key → 401, a
// valid key → 200.
//
// Mutation U4-M3: drop the vk gate from the /v1/models route → the no-key
// request reaches 200 → RED.
func TestModels_RequiresVKAuth(t *testing.T) {
	db := testControlDB(t)
	mux := PublicMux(db, nil, nil, func() time.Time { return vkFixedNow })
	seedAPIKey(t, db, "k-1", "vk_live_models01", nil, false)

	noKey := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	noKeyRec := httptest.NewRecorder()
	mux.ServeHTTP(noKeyRec, noKey)
	if noKeyRec.Code != http.StatusUnauthorized {
		t.Fatalf("no-key /v1/models = %d, want 401", noKeyRec.Code)
	}

	withKey := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	withKey.Header.Set("Authorization", "Bearer vk_live_models01")
	withKeyRec := httptest.NewRecorder()
	mux.ServeHTTP(withKeyRec, withKey)
	if withKeyRec.Code != http.StatusOK {
		t.Fatalf("valid-key /v1/models = %d, want 200", withKeyRec.Code)
	}
}

// TestModels_HandlerIsDBFree proves the handler performs ZERO database queries:
// it is constructed with NO database at all (modelsListHandler has no db field)
// and still serves the full list. A regression that reached for the catalog or
// DB could not even compile against this handler type.
func TestModels_HandlerIsDBFree(t *testing.T) {
	h := newModelsListHandler() // no *storage.DB is available to it, by construction
	rec := httptest.NewRecorder()
	h.ServeModels(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("DB-free handler = %d, want 200", rec.Code)
	}
	if _, ids, _ := decodeModelsList(t); len(ids) != 3 {
		t.Fatalf("DB-free handler returned %d models, want 3", len(ids))
	}
}

// providerIDsOf returns reg's registered provider ids, sorted, via
// providers.Registry.IDs().
func providerIDsOf(reg *providers.Registry) []providers.ProviderID {
	return reg.IDs()
}

// expectedCompositionRootProviderIDs is an INDEPENDENT, literal statement of
// which providers newProviderRegistry() is expected to register — written by
// hand, not derived from newProviderRegistry() or anything it calls. That
// independence is what makes it capable of catching a real regression (a
// `_ = registerX(reg)` line deleted from newProviderRegistry): a test that
// instead re-derives its expectation from production code under test would
// merely restate whatever that code currently does, and could never fail no
// matter what that code did (X == X, no matter what X is).
//
// antigravity is env-gated (registerAntigravityIfConfigured registers it
// only when VENOM_ANTIGRAVITY_CLIENT_ID/_SECRET are both set) — this list
// reflects that condition by calling the SAME predicate production uses
// (platform.AntigravityOAuthClientCredentials), rather than hardcoding
// antigravity's presence or absence, or skipping the assertion when the
// env-gate is closed. That keeps this test non-vacuous in CI regardless of
// which way the gate currently falls, while every OTHER entry stays an
// unconditional literal — only antigravity is genuinely environment-
// dependent, so it is the only one given that treatment.
func expectedCompositionRootProviderIDs() []providers.ProviderID {
	ids := []providers.ProviderID{
		providers.OpenCodeZenID,
		providers.OllamaCloudID,
		providers.AgnesAIID,
		providers.NvidiaNIMID,
		providers.GeminiCLIID,
		providers.ClaudeCodeID,
		providers.ClinePassID,
	}
	if _, _, ok := platform.AntigravityOAuthClientCredentials(); ok {
		ids = append(ids, providers.AntigravityID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// TestNewProviderRegistry_RegistersExactlyTheExpectedProviderSet pins the
// composition root's (newProviderRegistry's) registered provider set against
// the independent literal expectation in expectedCompositionRootProviderIDs.
// This replaces an earlier version of this test that compared
// newProviderRegistry() against publicMuxFallbackRegistry() — whose entire
// body is `return newProviderRegistry()` — which was a tautology (X == X)
// structurally incapable of failing for any real defect, once the fallback
// list had already been collapsed into a one-line delegation. Now, deleting
// any single `_ = registerX(reg)` line from newProviderRegistry fails this
// test directly (see the mutation trace in task-9-report.md).
func TestNewProviderRegistry_RegistersExactlyTheExpectedProviderSet(t *testing.T) {
	got := providerIDsOf(newProviderRegistry())
	want := expectedCompositionRootProviderIDs()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("newProviderRegistry() registered ids = %v, want %v", got, want)
	}
}

// TestPublicMuxFallbackRegistry_DelegatesToCompositionRoot is DOCUMENTATION
// of publicMuxFallbackRegistry's delegation, not a drift guard: once the
// fallback registry's entire body is `return newProviderRegistry()`, there is
// no second hand-maintained list left for it to drift from, so this
// assertion can never do anything
// TestNewProviderRegistry_RegistersExactlyTheExpectedProviderSet does not
// already do on its own. It exists purely to pin the shape of the
// delegation itself (e.g. a future edit that starts building a *separate*
// registry inline again, rather than calling newProviderRegistry, would
// break this immediately even before it drifted in content).
func TestPublicMuxFallbackRegistry_DelegatesToCompositionRoot(t *testing.T) {
	want := providerIDsOf(newProviderRegistry())
	got := providerIDsOf(publicMuxFallbackRegistry())
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("publicMuxFallbackRegistry() ids = %v, want %v (it must delegate to newProviderRegistry, not repeat its list)", got, want)
	}
}

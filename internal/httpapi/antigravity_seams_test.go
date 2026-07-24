package httpapi

import (
	"encoding/json"
	"os"
	"testing"
)

// TestControlMux_Antigravity_RegisteredOnlyWhenBothEnvVarsConfigured
// proves the composition-root wiring (registerAntigravityIfConfigured,
// called from ControlMux) end to end: with both antigravity env vars
// unset, GET /providers/antigravity reports zero capabilities (no live
// OAuth adapter was ever registered into the registry); once both are
// set and a FRESH ControlMux is built (env is read once at construction
// time, exactly like every other composition-root value), the same
// endpoint reports "oauth2" among its capabilities.
func TestControlMux_Antigravity_RegisteredOnlyWhenBothEnvVarsConfigured(t *testing.T) {
	const idVar = "VENOM_ANTIGRAVITY_CLIENT_ID"
	const secretVar = "VENOM_ANTIGRAVITY_CLIENT_SECRET"
	_ = os.Unsetenv(idVar)
	_ = os.Unsetenv(secretVar)

	muxUnset := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))
	cookieUnset := setupOwner(t, muxUnset, testSetupPassword)
	recUnset := getGated(t, muxUnset, "/api/control/v1/providers/antigravity", cookieUnset)

	var gotUnset struct {
		Data providerJSON `json:"data"`
	}
	if err := json.Unmarshal(recUnset.Body.Bytes(), &gotUnset); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(gotUnset.Data.Capabilities) != 0 {
		t.Fatalf("capabilities = %v, want empty with antigravity env unset (no adapter should be registered)", gotUnset.Data.Capabilities)
	}

	t.Setenv(idVar, "test-antigravity-client-id")
	t.Setenv(secretVar, "test-antigravity-client-secret")

	muxSet := ControlMux(testAllowedHost, fakeSPA(), testControlDB(t), testKeyring(t))
	cookieSet := setupOwner(t, muxSet, testSetupPassword)
	recSet := getGated(t, muxSet, "/api/control/v1/providers/antigravity", cookieSet)

	var gotSet struct {
		Data providerJSON `json:"data"`
	}
	if err := json.Unmarshal(recSet.Body.Bytes(), &gotSet); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !gotSet.Data.Configured {
		t.Fatalf("configured = false with both env vars set, want true")
	}
	found := false
	for _, c := range gotSet.Data.Capabilities {
		if c == "oauth2" {
			found = true
		}
	}
	if !found {
		t.Fatalf("capabilities = %v, want to include oauth2 once both env vars are configured", gotSet.Data.Capabilities)
	}
}

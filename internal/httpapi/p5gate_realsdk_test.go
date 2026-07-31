package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/platform"
)

// P5-TEST-001 real-SDK opt-in harness. CI stays CREDENTIAL-FREE: with neither
// env var set (the CI default), TestP5Gate_RealSDK_OptIn t.Skips. An owner
// pointing at a live Venom + a real provider sets both and runs it by hand — the
// dated procedure is docs/evidence/P5-TEST-001-real-sdk-runbook.md. This harness
// uses a plain net/http client (no `openai` dependency may be added, GOVERNOR
// DECISION), which is exactly what a real SDK does on the wire.
// realSDKConfig reports the opt-in configuration and whether BOTH values are
// present. ok=false is the CI default and makes the harness skip.
//
// The env read lives in internal/platform (RealSDKE2EConfig), the ONE place
// forbidigo permits it, exactly as P2b-TEST-003's real-account harness does via
// platform.OpenCodeZenE2ECredential. An earlier revision scanned os.Environ()
// from this file instead: that returns the same values while stepping around an
// intact lint rule rather than honoring it, so it was replaced with the narrow
// accessor the rule intends.
func realSDKConfig() (baseURL, key string, ok bool) {
	return platform.RealSDKE2EConfig()
}

// TestP5Gate_RealSDKHarnessSkipsWithoutEnv proves the opt-in harness is inert in
// CI: with the env vars cleared, realSDKConfig reports ok=false, so
// TestP5Gate_RealSDK_OptIn skips and no credential is ever required.
func TestP5Gate_RealSDKHarnessSkipsWithoutEnv(t *testing.T) {
	// The variable NAMES are owned by internal/platform; clearing them here by
	// their literal wire names keeps this test independent of that package's
	// unexported constants while still proving the harness is inert.
	t.Setenv("VENOM_E2E_REAL_SDK_BASE_URL", "")
	t.Setenv("VENOM_E2E_REAL_SDK_KEY", "")
	if _, _, ok := realSDKConfig(); ok {
		t.Fatalf("realSDKConfig must report ok=false with no env set — CI must stay credential-free")
	}
}

// TestP5Gate_RealSDK_OptIn drives a LIVE Venom instance over the wire when the
// two env vars are set; otherwise it SKIPS. Not part of the CI gate — the CI
// gate is the wire-conformance suite in p5gate_sdk_test.go.
func TestP5Gate_RealSDK_OptIn(t *testing.T) {
	baseURL, key, ok := realSDKConfig()
	if !ok {
		t.Skipf("opt-in: set %s and %s to run against a live Venom (see docs/evidence/P5-TEST-001-real-sdk-runbook.md)", "VENOM_E2E_REAL_SDK_BASE_URL", "VENOM_E2E_REAL_SDK_KEY")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"venom/pro","messages":[{"role":"user","content":"ping"}]}`)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("live request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("live status = %d, body %s", resp.StatusCode, body)
	}
	var got struct {
		Object string `json:"object"`
	}
	if err := json.Unmarshal(body, &got); err != nil || got.Object != "chat.completion" {
		t.Fatalf("live response not an OpenAI completion object: err=%v body=%s", err, body)
	}
	if resp.Header.Get("X-Venom-Request-Id") == "" {
		t.Fatalf("live response missing X-Venom-Request-Id")
	}
}

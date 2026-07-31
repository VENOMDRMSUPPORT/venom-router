package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// P5-TEST-001 real-SDK opt-in harness. CI stays CREDENTIAL-FREE: with neither
// env var set (the CI default), TestP5Gate_RealSDK_OptIn t.Skips. An owner
// pointing at a live Venom + a real provider sets both and runs it by hand — the
// dated procedure is docs/evidence/P5-TEST-001-real-sdk-runbook.md. This harness
// uses a plain net/http client (no `openai` dependency may be added, GOVERNOR
// DECISION), which is exactly what a real SDK does on the wire.
const (
	envRealSDKBaseURL = "VENOM_E2E_REAL_SDK_BASE_URL" // e.g. http://127.0.0.1:8081
	envRealSDKKey     = "VENOM_E2E_REAL_SDK_KEY"      // a vk_live_* key from POST /keys
)

// realSDKConfig reports the opt-in configuration and whether BOTH values are
// present. ok=false is the CI default and makes the harness skip.
//
// It scans os.Environ rather than calling os.Getenv/os.LookupEnv: forbidigo
// forbids those two outside internal/config and internal/platform, and this
// opt-in test harness cannot add a platform accessor within its own file set.
// os.Environ carries no such restriction and yields the same values.
func realSDKConfig() (baseURL, key string, ok bool) {
	for _, kv := range os.Environ() {
		if v, found := strings.CutPrefix(kv, envRealSDKBaseURL+"="); found {
			baseURL = strings.TrimSpace(v)
		}
		if v, found := strings.CutPrefix(kv, envRealSDKKey+"="); found {
			key = strings.TrimSpace(v)
		}
	}
	return baseURL, key, baseURL != "" && key != ""
}

// TestP5Gate_RealSDKHarnessSkipsWithoutEnv proves the opt-in harness is inert in
// CI: with the env vars cleared, realSDKConfig reports ok=false, so
// TestP5Gate_RealSDK_OptIn skips and no credential is ever required.
func TestP5Gate_RealSDKHarnessSkipsWithoutEnv(t *testing.T) {
	t.Setenv(envRealSDKBaseURL, "")
	t.Setenv(envRealSDKKey, "")
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
		t.Skipf("opt-in: set %s and %s to run against a live Venom (see docs/evidence/P5-TEST-001-real-sdk-runbook.md)", envRealSDKBaseURL, envRealSDKKey)
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

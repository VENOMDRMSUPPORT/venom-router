package httpapi

// P6-TEST-002's RECORDED-EVIDENCE half: the opt-in harness.
//
// CI stays credential-free and network-free. With no env set — the CI default
// — TestP6Gate_RealOperate_OptIn t.Skips, and TestP6Gate_RealOperateHarnessSkipsWithoutEnv
// proves it does. An owner running a LIVE Venom sets the two variables and runs
// it by hand; the dated procedure is
// docs/evidence/P6-TEST-002-operate-without-terminal-runbook.md.
//
// WHY IT REUSES THE P5 ENV PAIR rather than inventing its own: the variables
// mean exactly what this harness needs — "the base URL of a live Venom" and "a
// Venom API key for it" — and the accessor already exists in internal/platform,
// the one package forbidigo permits to read the environment. Adding a third
// variable would have meant adding production code to internal/platform for a
// value indistinguishable from the one already there.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/platform"
)

// realOperateConfig reports the opt-in configuration and whether BOTH values
// are present. ok=false is the CI default and makes the harness skip.
func realOperateConfig() (baseURL, key string, ok bool) {
	return platform.RealSDKE2EConfig()
}

// TestP6Gate_RealOperateHarnessSkipsWithoutEnv proves the opt-in harness is
// INERT in CI.
//
// This is the test that makes the skip trustworthy. A harness that silently
// passes when it did nothing is indistinguishable, in a CI log, from one that
// ran and succeeded — and this project has caught that exact shape before.
//
// Mutation M6: make the harness run without credentials (drop the `!ok` guard,
// or default ok=true) → this test goes RED.
func TestP6Gate_RealOperateHarnessSkipsWithoutEnv(t *testing.T) {
	// Cleared by their literal wire names so this test stays independent of
	// internal/platform's unexported constants while still proving inertness.
	t.Setenv("VENOM_E2E_REAL_SDK_BASE_URL", "")
	t.Setenv("VENOM_E2E_REAL_SDK_KEY", "")

	if _, _, ok := realOperateConfig(); ok {
		t.Fatalf("realOperateConfig must report ok=false with no env set — the opt-in operate " +
			"harness must never run (or pass) in CI, where there is no live Venom and no credential")
	}
}

// TestP6Gate_RealOperateHarnessSkipsWithOnlyOneVar proves a HALF-configured
// environment also skips rather than running against a partially specified
// target — the failure mode where someone exports the base URL, forgets the
// key, and reads the resulting pass as evidence.
func TestP6Gate_RealOperateHarnessSkipsWithOnlyOneVar(t *testing.T) {
	t.Setenv("VENOM_E2E_REAL_SDK_BASE_URL", "http://127.0.0.1:8081")
	t.Setenv("VENOM_E2E_REAL_SDK_KEY", "")
	if _, _, ok := realOperateConfig(); ok {
		t.Fatalf("base URL alone must not enable the harness")
	}

	t.Setenv("VENOM_E2E_REAL_SDK_BASE_URL", "")
	t.Setenv("VENOM_E2E_REAL_SDK_KEY", "vk_live_whatever")
	if _, _, ok := realOperateConfig(); ok {
		t.Fatalf("key alone must not enable the harness")
	}
}

// TestP6Gate_RealOperate_OptIn drives a LIVE Venom over the wire when both env
// vars are set; otherwise it SKIPS LOUDLY with the reason and a pointer to the
// runbook.
//
// It asserts the three things that can only be true of a REAL running instance
// and are therefore not provable by the CI half: the control plane is
// reachable on its loopback bind, the owner's session surface answers, and a
// real Venom key authenticates against the live data plane.
func TestP6Gate_RealOperate_OptIn(t *testing.T) {
	baseURL, key, ok := realOperateConfig()
	if !ok {
		t.Skipf("opt-in: set %s and %s to run the operate-without-terminal harness against a live Venom "+
			"(see docs/evidence/P6-TEST-002-operate-without-terminal-runbook.md)",
			"VENOM_E2E_REAL_SDK_BASE_URL", "VENOM_E2E_REAL_SDK_KEY")
	}

	base := strings.TrimRight(baseURL, "/")
	client := &http.Client{Timeout: 30 * time.Second}

	// 1. The control plane is up and reports a completed first run — i.e. the
	//    owner already set themselves up through the dashboard.
	statusResp, err := client.Get(base + "/api/control/v1/auth/status")
	if err != nil {
		t.Fatalf("live control plane unreachable at %s: %v", base, err)
	}
	defer func() { _ = statusResp.Body.Close() }()
	if statusResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /auth/status = %d, want 200", statusResp.StatusCode)
	}
	var status struct {
		Data struct {
			SetupComplete bool `json:"setup_complete"`
		} `json:"data"`
	}
	body, _ := io.ReadAll(statusResp.Body)
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatalf("decode /auth/status: %v; body = %s", err, body)
	}
	if !status.Data.SetupComplete {
		t.Fatalf("the live instance reports setup_complete=false — complete first-run setup in the dashboard first")
	}

	// 2. The dashboard itself is SERVED by the binary. This is the "no
	//    terminal" claim's foundation: if the SPA is not served, the owner has
	//    nothing to operate from.
	spaResp, err := client.Get(base + "/")
	if err != nil {
		t.Fatalf("dashboard SPA unreachable: %v", err)
	}
	defer func() { _ = spaResp.Body.Close() }()
	if spaResp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d, want 200 — the embedded dashboard must be served", spaResp.StatusCode)
	}

	// 3. A real Venom key authenticates against the live data plane — the last
	//    step of "connect a client", proven end to end.
	req, err := http.NewRequest(http.MethodPost, base+"/v1/chat/completions",
		strings.NewReader(`{"model":"venom/pro","messages":[{"role":"user","content":"ping"}]}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	chatResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("live data-plane request failed: %v", err)
	}
	defer func() { _ = chatResp.Body.Close() }()
	if chatResp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("the supplied key did not authenticate — mint one in the dashboard's API Keys surface")
	}
	if chatResp.StatusCode != http.StatusOK {
		chatBody, _ := io.ReadAll(chatResp.Body)
		t.Fatalf("POST /v1/chat/completions = %d; body = %s", chatResp.StatusCode, chatBody)
	}
}

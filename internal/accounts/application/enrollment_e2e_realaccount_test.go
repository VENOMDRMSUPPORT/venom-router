package application_test

// enrollment_e2e_realaccount_test.go is P2b-TEST-003 C.2's opt-in,
// NON-CI-blocking real-account harness for opencode-zen — the one piece
// of this whole P2b-TEST-003 batch that is automatable against a real
// account (antigravity's OAuth is interactive/browser-driven and is
// instead documented as a manual, dated evidence runbook — see
// docs/evidence/P2b-TEST-003-real-account-runbook.md).
//
// This test performs a REAL network call to the real, public opencode-
// zen endpoint (providers.OpenCodeZenBaseURL) — the ONLY real-network
// path in this entire test batch — and ONLY when VENOM_E2E_OPENCODE_ZEN_KEY
// is set to a real free-tier opencode-zen API key. In every normal CI
// run (which sets no such variable) this test calls t.Skip() immediately
// and never opens a socket, so CI stays green with zero credentials
// configured. This is NOT run by `task gate` in any credentialed way —
// it is included in `go test ./...` like every other test, but skips
// itself out cleanly absent the env var.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/platform"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// realOpenCodeZenHTTPTimeout mirrors internal/httpapi/opencode_zen_seams.go's
// openCodeZenHTTPTimeout.
const realOpenCodeZenHTTPTimeout = 15 * time.Second

// realOpenCodeZenChatProbe and realOpenCodeZenModelsProbe are REAL
// (net/http-backed) implementations of providers.ChatProbe/ModelsProbe,
// behaviorally identical to internal/httpapi/opencode_zen_seams.go's
// production seams — duplicated here rather than imported because
// internal/httpapi imports internal/accounts/application, so the
// reverse import would be a layering cycle (internal/staticgate's
// TestLayering_StorageDependencyIsOneDirectionOnly-style guard). This
// is the only place in this test batch that performs a real network
// call, and only ever from inside TestRealAccount_OpenCodeZen_E2E, which
// itself only runs when VENOM_E2E_OPENCODE_ZEN_KEY is set.
func realOpenCodeZenChatProbe(ctx context.Context, baseURL, key string) (int, error) {
	// Mirrors the production seam: resolve a real model id from GET
	// /v1/models FIRST (opencode-zen validates the model before the key, so a
	// model-less body is rejected 401 ModelError even with a valid key), then
	// POST a single short user message with that model and max_tokens: 1. A
	// failed/empty models read reports unavailable (non-nil error), never
	// invalid; a 401/403 that is a model/request-shape problem does too.
	modelID, err := firstRealOpenCodeZenModelID(ctx, baseURL, key)
	if err != nil {
		return 0, err
	}

	reqBody, err := json.Marshal(struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		MaxTokens int `json:"max_tokens"`
	}{
		Model: modelID,
		Messages: []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}{{Role: "user", Content: "ping"}},
		MaxTokens: 1,
	})
	if err != nil {
		return 0, fmt.Errorf("real opencode-zen chat probe marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return 0, fmt.Errorf("real opencode-zen chat probe request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: realOpenCodeZenHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("real opencode-zen chat probe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		probeBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		hay := strings.ToLower(string(probeBody))
		switch {
		case strings.Contains(hay, "creditserror") || strings.Contains(hay, "insufficient balance"):
			// Billing/credits rejection proves the key authenticated (mirrors
			// the production seam's ocz401Authenticated branch).
			return 0, providers.ErrProbeAuthenticated
		case strings.Contains(hay, "autherror") || strings.Contains(hay, "invalid api key") || strings.Contains(hay, "missing api key"):
			return resp.StatusCode, nil
		default:
			return 0, errors.New("real opencode-zen chat probe: 401/403 without a positive authentication signal (unavailable, not invalid)")
		}
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// firstRealOpenCodeZenModelID resolves the first advertised model id from the
// real GET /v1/models, mirroring the production seam's resolveOpenCodeZenModelID.
func firstRealOpenCodeZenModelID(ctx context.Context, baseURL, key string) (string, error) {
	body, err := realOpenCodeZenModelsProbe(ctx, baseURL, key)
	if err != nil {
		return "", fmt.Errorf("real opencode-zen chat probe resolve model: %w", err)
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return "", fmt.Errorf("real opencode-zen chat probe resolve model: parse models: %w", err)
	}
	if len(list.Data) == 0 || list.Data[0].ID == "" {
		return "", errors.New("real opencode-zen chat probe resolve model: catalog returned no usable model id")
	}
	return list.Data[0].ID, nil
}

func realOpenCodeZenModelsProbe(ctx context.Context, baseURL, key string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/models", nil)
	if err != nil {
		return nil, fmt.Errorf("real opencode-zen models probe request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)

	client := &http.Client{Timeout: realOpenCodeZenHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("real opencode-zen models probe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("real opencode-zen models probe: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("real opencode-zen models probe returned status %d", resp.StatusCode)
	}
	return respBody, nil
}

// TestRealAccount_OpenCodeZen_E2E is the opt-in real-account acceptance
// test: with VENOM_E2E_OPENCODE_ZEN_KEY set to a real free-tier
// opencode-zen API key, it connects a real account through the exact
// same ConnectService production code path the fixture contract test
// (opencode_zen_e2e_test.go) exercises against a fixture — only the
// HTTP seams differ (real vs. fixture) — and asserts correct identity/
// funding/health plus the secret-clean canary. Absent the env var, it
// skips immediately (platform.OpenCodeZenE2ECredential's accessor is the
// narrow, single-purpose internal/platform seam this test reads the
// credential through, since forbidigo forbids os.Getenv/os.LookupEnv
// anywhere outside internal/config and internal/platform).
func TestRealAccount_OpenCodeZen_E2E(t *testing.T) {
	key, present := platform.OpenCodeZenE2ECredential()
	if !present || key == "" {
		t.Skip("set VENOM_E2E_OPENCODE_ZEN_KEY to run the real-account opencode-zen E2E")
	}

	db := migratedDB(t)
	seedProvider(t, db, "opencode-zen")

	adapter := providers.NewOpenCodeZenAdapter(realOpenCodeZenChatProbe, realOpenCodeZenModelsProbe)
	enrollment := storage.NewEnrollmentRepo(db)
	accounts := storage.NewAccountRepo(db)
	svc := application.NewConnectService(enrollment, accounts, newTestKeyring(t), sequentialIDGenerator("real-e2e"), nil)

	account, err := svc.ConnectAPIKeyAccount(context.Background(), application.ConnectAPIKeyAccountParams{
		ProviderID: "opencode-zen", Adapter: adapter, PlaintextKey: key,
		FundingMode: domain.FundingModeOwnerPolicy,
	})
	if err != nil {
		t.Fatalf("ConnectAPIKeyAccount against the REAL opencode-zen endpoint: %v", err)
	}

	if account.ConnectionState != domain.ConnectionConnected {
		t.Fatalf("ConnectionState = %q, want connected", account.ConnectionState)
	}
	if account.ExternalID == "" || account.ExternalID == key {
		t.Fatalf("ExternalID = %q, want a non-empty fingerprint distinct from the raw key", account.ExternalID)
	}

	fund, ok, err := storage.NewFundingEvidenceRepo(db).CurrentForAccount(context.Background(), account.ID)
	if err != nil || !ok {
		t.Fatalf("CurrentForAccount: ok=%v err=%v", ok, err)
	}
	if fund.Funding != domain.FundingFree {
		t.Fatalf("Funding = %q, want free (a real opencode-zen free-tier account)", fund.Funding)
	}
	if account.HealthState != domain.HealthUnknown {
		t.Fatalf("HealthState = %q, want %q (no health probe at connect time this phase)", account.HealthState, domain.HealthUnknown)
	}

	// Secret-clean canary: the real API key must never appear in any
	// persisted row, exactly like the fixture contract test.
	assertNoFragmentAnywhere(t, db, "accounts", key)
	assertNoFragmentAnywhere(t, db, "account_credentials", key)
	assertNoFragmentAnywhere(t, db, "account_funding_evidence", key)

	t.Logf("real-account opencode-zen E2E: account_id=%s external_id_fingerprint=%s funding=%s health=%s",
		account.ID, account.ExternalID, fund.Funding, account.HealthState)
}

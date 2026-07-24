package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// openCodeZenHTTPTimeout bounds every real network call opencode-zen's
// two probes make, mirroring antigravityHTTPTimeout's rationale
// (antigravity_seams.go): neither the chat-completions nor the models
// endpoint should ever legitimately take longer than this.
const openCodeZenHTTPTimeout = 15 * time.Second

var openCodeZenHTTPClient = &http.Client{Timeout: openCodeZenHTTPTimeout}

// openCodeZenChatProbeSeam is the real (net/http-backed) implementation
// of providers.ChatProbe for opencode-zen (P2b-PROV-005/CAPI-003): a
// zero-cost POST to {baseURL}/v1/chat/completions (03 §1's authentic
// validation rule — max_tokens: 1, never a mere GET /v1/models
// host-up check). key is sent ONLY as the Authorization header value —
// never logged, never included in any error this function returns.
func openCodeZenChatProbeSeam(ctx context.Context, baseURL, key string) (int, error) {
	body := bytes.NewReader([]byte(`{"max_tokens":1}`))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/chat/completions", body)
	if err != nil {
		return 0, fmt.Errorf("httpapi: opencode-zen chat probe request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := openCodeZenHTTPClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("httpapi: opencode-zen chat probe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.StatusCode, nil
}

// openCodeZenModelsProbeSeam is the real implementation of
// providers.ModelsProbe for opencode-zen's model-discovery capability
// (P2b-PROV-005): a GET to {baseURL}/v1/models. key is sent ONLY as the
// Authorization header value — never logged.
func openCodeZenModelsProbeSeam(ctx context.Context, baseURL, key string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/models", nil)
	if err != nil {
		return nil, fmt.Errorf("httpapi: opencode-zen models probe request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := openCodeZenHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpapi: opencode-zen models probe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("httpapi: opencode-zen models probe: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("httpapi: opencode-zen models probe returned status %d", resp.StatusCode)
	}
	return respBody, nil
}

// registerOpenCodeZen registers the opencode-zen API-key adapter into
// reg using this file's real HTTP seams. Unlike antigravity
// (registerAntigravityIfConfigured), this is unconditional — opencode-zen
// needs no confidential-client env vars, so there is nothing to gate on.
// The returned error is non-nil only if reg.Register itself rejects the
// registration (e.g. a duplicate "opencode-zen" registration), which
// cannot happen in this composition since this is the only call site
// that ever registers opencode-zen into a given Registry.
func registerOpenCodeZen(reg *providers.Registry) error {
	return providers.RegisterOpenCodeZen(reg, openCodeZenChatProbeSeam, openCodeZenModelsProbeSeam)
}

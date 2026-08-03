package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// ollamaCloudHTTPTimeout bounds every real network call ollama-cloud's probes
// make, mirroring openCodeZenHTTPTimeout's rationale.
const ollamaCloudHTTPTimeout = 15 * time.Second

var ollamaCloudHTTPClient = &http.Client{Timeout: ollamaCloudHTTPTimeout}

// ollamaCloudProbeBodyLimit bounds how much of the identity response body we
// read. The /api/me record is tiny; this only exists so a hostile/huge body
// can never exhaust memory.
const ollamaCloudProbeBodyLimit = 64 << 10

// ollamaIdentityProbeSeam is the real (net/http-backed) implementation of
// providers.OllamaIdentityProbe: POST https://ollama.com/api/me with
// Authorization: Bearer <key> and Accept: application/json, no body (03 §3).
// This is ollama-cloud's authentic validation — the /api/me record is returned
// only for a recognized credential, unlike the /v1/models listing. It returns
// the raw status + body so the adapter can classify and parse; key is sent
// ONLY as the Authorization header value and is never logged.
func ollamaIdentityProbeSeam(ctx context.Context, key string) (int, []byte, error) {
	return ollamaIdentityProbeSeamAt(ctx, providers.OllamaCloudIdentityBaseURL, key)
}

// ollamaIdentityProbeSeamAt is the base-URL-parameterized core of the identity
// probe, so its header/method contract is exercisable against an httptest
// server. Production always calls it with OllamaCloudIdentityBaseURL.
func ollamaIdentityProbeSeamAt(ctx context.Context, identityBaseURL, key string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, identityBaseURL+"/me", nil)
	if err != nil {
		return 0, nil, fmt.Errorf("httpapi: ollama-cloud identity probe request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")

	resp, err := ollamaCloudHTTPClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("httpapi: ollama-cloud identity probe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, ollamaCloudProbeBodyLimit))
	if err != nil {
		return 0, nil, fmt.Errorf("httpapi: ollama-cloud identity probe: read response: %w", err)
	}
	return resp.StatusCode, body, nil
}

// ollamaCloudModelsProbeSeam is the real implementation of
// providers.ModelsProbe for ollama-cloud's discovery: a GET to
// {baseURL}/models (baseURL already carries the /v1 segment). key is sent ONLY
// as the Authorization header value — never logged.
func ollamaCloudModelsProbeSeam(ctx context.Context, baseURL, key string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("httpapi: ollama-cloud models probe request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := ollamaCloudHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpapi: ollama-cloud models probe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("httpapi: ollama-cloud models probe: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("httpapi: ollama-cloud models probe: %w", &providers.ModelsProbeStatusError{StatusCode: resp.StatusCode})
	}
	return respBody, nil
}

// registerOllamaCloud registers the ollama-cloud API-key adapter into reg over
// this file's real HTTP seams. It reuses openCodeZenModelsDevProbeSeam for the
// models.dev dataset — that seam is provider-agnostic (a plain GET of the
// public dataset that sends no credential). Always registered unconditionally
// (no confidential-client env vars); the returned error is non-nil only if reg
// rejects the registration (a duplicate), which cannot happen in this
// composition.
func registerOllamaCloud(reg *providers.Registry) error {
	return providers.RegisterOllamaCloud(reg, ollamaIdentityProbeSeam, ollamaCloudModelsProbeSeam, openCodeZenModelsDevProbeSeam, nil)
}

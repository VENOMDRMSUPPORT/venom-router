package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// agnesHTTPTimeout bounds every real network call agnes-ai's probes make,
// mirroring openCodeZenHTTPTimeout's rationale.
const agnesHTTPTimeout = 15 * time.Second

var agnesHTTPClient = &http.Client{Timeout: agnesHTTPTimeout}

// agnesChatProbeMessage / agnesChatProbeRequest are the minimal
// OpenAI-compatible chat-completions request the probe sends.
type agnesChatProbeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type agnesChatProbeRequest struct {
	Model     string                  `json:"model"`
	Messages  []agnesChatProbeMessage `json:"messages"`
	MaxTokens int                     `json:"max_tokens"`
}

// agnesChatProbeSeam is the real (net/http-backed) implementation of
// providers.ChatProbe for agnes-ai: 03 §1's authentic zero-cost chat probe,
// NOT a host-up check. It first resolves a real model id from the provider's
// own catalog (GET {baseURL}/models) and only then POSTs a single short user
// message with that model and max_tokens: 1. If the models read fails or the
// catalog is empty, it reports UNAVAILABLE (returns a non-nil error), NEVER
// invalid — a working key must not be branded invalid because the catalog
// momentarily could not be read. A 401/403 from the chat call is a genuine
// auth rejection and is returned as the status so ValidateAPIKey maps it to
// invalid. key is sent ONLY as the Authorization header value and is never
// logged. Structurally mirrors openCodeZenChatProbeSeam, minus zen's
// billing-quirk triage (agnes has no such quirk).
func agnesChatProbeSeam(ctx context.Context, baseURL, key string) (int, error) {
	modelID, err := resolveAgnesModelID(ctx, baseURL, key)
	if err != nil {
		return 0, err
	}

	reqBody, err := json.Marshal(agnesChatProbeRequest{
		Model:     modelID,
		Messages:  []agnesChatProbeMessage{{Role: "user", Content: "ping"}},
		MaxTokens: 1,
	})
	if err != nil {
		return 0, fmt.Errorf("httpapi: agnes-ai chat probe marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return 0, fmt.Errorf("httpapi: agnes-ai chat probe request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := agnesHTTPClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("httpapi: agnes-ai chat probe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// resolveAgnesModelID reads GET {baseURL}/models and returns the first
// advertised model id. A transport/status failure or an empty catalog returns
// a non-nil error so the caller reports unavailable, never invalid.
func resolveAgnesModelID(ctx context.Context, baseURL, key string) (string, error) {
	body, err := agnesModelsProbeSeam(ctx, baseURL, key)
	if err != nil {
		return "", fmt.Errorf("httpapi: agnes-ai chat probe resolve model: %w", err)
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return "", fmt.Errorf("httpapi: agnes-ai chat probe resolve model: parse models: %w", err)
	}
	if len(list.Data) == 0 || list.Data[0].ID == "" {
		return "", errors.New("httpapi: agnes-ai chat probe resolve model: catalog returned no usable model id")
	}
	return list.Data[0].ID, nil
}

// agnesModelsProbeSeam is the real implementation of providers.ModelsProbe for
// agnes-ai's discovery: a GET to {baseURL}/models. key is sent ONLY as the
// Authorization header value — never logged.
func agnesModelsProbeSeam(ctx context.Context, baseURL, key string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("httpapi: agnes-ai models probe request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := agnesHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpapi: agnes-ai models probe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("httpapi: agnes-ai models probe: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("httpapi: agnes-ai models probe: %w", &providers.ModelsProbeStatusError{StatusCode: resp.StatusCode})
	}
	return respBody, nil
}

// registerAgnesAI registers the agnes-ai API-key adapter into reg over this
// file's real HTTP seams. Always registered unconditionally; the returned
// error is non-nil only if reg rejects the registration (a duplicate).
func registerAgnesAI(reg *providers.Registry) error {
	return providers.RegisterAgnesAI(reg, agnesChatProbeSeam, agnesModelsProbeSeam)
}

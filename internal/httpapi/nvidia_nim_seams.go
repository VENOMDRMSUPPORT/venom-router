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

// nvidiaHTTPTimeout bounds every real network call nvidia-nim's probes make.
const nvidiaHTTPTimeout = 15 * time.Second

var nvidiaHTTPClient = &http.Client{Timeout: nvidiaHTTPTimeout}

type nvidiaChatProbeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type nvidiaChatProbeRequest struct {
	Model     string                   `json:"model"`
	Messages  []nvidiaChatProbeMessage `json:"messages"`
	MaxTokens int                      `json:"max_tokens"`
}

// nvidiaChatProbeSeam is the real (net/http-backed) implementation of
// providers.ChatProbe for nvidia-nim: the same authentic two-step probe agnes
// uses (03 §1) — resolve a real model id from GET {baseURL}/models, then POST a
// single max_tokens:1 chat message with that id. An unreadable/empty catalog is
// UNAVAILABLE (non-nil error), never invalid; a 401/403 from the chat call is
// returned as the status so ValidateAPIKey maps it to invalid. key is sent ONLY
// as the Authorization header value and is never logged.
func nvidiaChatProbeSeam(ctx context.Context, baseURL, key string) (int, error) {
	modelID, err := resolveNvidiaModelID(ctx, baseURL, key)
	if err != nil {
		return 0, err
	}

	reqBody, err := json.Marshal(nvidiaChatProbeRequest{
		Model:     modelID,
		Messages:  []nvidiaChatProbeMessage{{Role: "user", Content: "ping"}},
		MaxTokens: 1,
	})
	if err != nil {
		return 0, fmt.Errorf("httpapi: nvidia-nim chat probe marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return 0, fmt.Errorf("httpapi: nvidia-nim chat probe request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := nvidiaHTTPClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("httpapi: nvidia-nim chat probe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// resolveNvidiaModelID reads GET {baseURL}/models and returns the first
// advertised model id. A transport/status failure or an empty catalog returns
// a non-nil error so the caller reports unavailable, never invalid.
func resolveNvidiaModelID(ctx context.Context, baseURL, key string) (string, error) {
	body, err := nvidiaModelsProbeSeam(ctx, baseURL, key)
	if err != nil {
		return "", fmt.Errorf("httpapi: nvidia-nim chat probe resolve model: %w", err)
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return "", fmt.Errorf("httpapi: nvidia-nim chat probe resolve model: parse models: %w", err)
	}
	if len(list.Data) == 0 || list.Data[0].ID == "" {
		return "", errors.New("httpapi: nvidia-nim chat probe resolve model: catalog returned no usable model id")
	}
	return list.Data[0].ID, nil
}

// nvidiaModelsProbeSeam is the real implementation of providers.ModelsProbe for
// nvidia-nim's discovery: a GET to {baseURL}/models. key is sent ONLY as the
// Authorization header value — never logged.
func nvidiaModelsProbeSeam(ctx context.Context, baseURL, key string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("httpapi: nvidia-nim models probe request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := nvidiaHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpapi: nvidia-nim models probe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("httpapi: nvidia-nim models probe: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("httpapi: nvidia-nim models probe: %w", &providers.ModelsProbeStatusError{StatusCode: resp.StatusCode})
	}
	return respBody, nil
}

// registerNvidiaNIM registers the nvidia-nim API-key adapter into reg over this
// file's real HTTP seams. It reuses openCodeZenModelsDevProbeSeam for the
// public models.dev dataset (provider-agnostic, sends no credential). Always
// registered unconditionally; the returned error is non-nil only if reg
// rejects the registration (a duplicate).
func registerNvidiaNIM(reg *providers.Registry) error {
	return providers.RegisterNvidiaNIM(reg, nvidiaChatProbeSeam, nvidiaModelsProbeSeam, openCodeZenModelsDevProbeSeam, nil)
}

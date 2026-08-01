package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// openCodeZenHTTPTimeout bounds every real network call opencode-zen's
// two probes make, mirroring antigravityHTTPTimeout's rationale
// (antigravity_seams.go): neither the chat-completions nor the models
// endpoint should ever legitimately take longer than this.
const openCodeZenHTTPTimeout = 15 * time.Second

var openCodeZenHTTPClient = &http.Client{Timeout: openCodeZenHTTPTimeout}

// openCodeZenProbeBodyLimit bounds how much of a 401/403 chat-probe body we
// read to classify it. The provider's error envelopes are tiny; this only
// exists so a hostile/huge body can never exhaust memory.
const openCodeZenProbeBodyLimit = 8 << 10

// errOpenCodeZenModelOrShapeRejection is the internal sentinel the chat probe
// returns when the provider answered 401/403 for a MODEL or request-shape
// reason rather than an authentication reason. It carries NO provider text —
// the provider's message is read only to classify and is then discarded — and
// it maps, via providers.ValidateAPIKey's err!=nil branch, to
// ValidationUnavailable (never invalid). This is the fail-closed guard: the
// system must never tell the owner their key is bad when it cannot actually
// establish that.
var errOpenCodeZenModelOrShapeRejection = errors.New("httpapi: opencode-zen chat probe: model/request-shape rejection (not an auth failure)")

// openCodeZenChatProbeMessage / openCodeZenChatProbeRequest are the minimal
// OpenAI-compatible chat-completions request the probe sends.
type openCodeZenChatProbeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openCodeZenChatProbeRequest struct {
	Model     string                        `json:"model"`
	Messages  []openCodeZenChatProbeMessage `json:"messages"`
	MaxTokens int                           `json:"max_tokens"`
}

// openCodeZenModelList is the subset of GET /v1/models the chat probe reads to
// resolve a real model id.
type openCodeZenModelList struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// openCodeZenChatProbeSeam is the real (net/http-backed) implementation of
// providers.ChatProbe for opencode-zen (P2b-PROV-005/CAPI-003): 03 §1's
// authentic zero-cost chat probe (max_tokens: 1), NOT a host-up check.
//
// It first resolves a real model id from the provider's own catalog (GET
// {baseURL}/v1/models, which is public) and only then POSTs a single short
// user message with that model and max_tokens: 1. This models read is NOT a
// host-up check and does not weaken 03 §1: authentication is still exercised
// by the chat call — opencode-zen validates the model BEFORE the key, so a
// model-less body is rejected 401 (ModelError "Model ... is not supported")
// even with a valid key, which is the exact defect this fix repairs. The
// models read only supplies a valid model NAME so the chat call can reach
// authentication; the model id is NEVER hardcoded because the catalog changes.
//
// If the models read fails or the catalog is empty, the probe reports
// unavailable (returns a non-nil error), NEVER invalid. On a 401/403 whose
// body indicates a model/request-shape problem rather than an auth problem, it
// also reports unavailable (the fail-closed guard). key is sent ONLY as the
// Authorization header value — never logged, never included in any error this
// function returns; provider error text is read solely to classify and is
// then discarded.
func openCodeZenChatProbeSeam(ctx context.Context, baseURL, key string) (int, error) {
	modelID, err := resolveOpenCodeZenModelID(ctx, baseURL, key)
	if err != nil {
		return 0, err
	}

	reqBody, err := json.Marshal(openCodeZenChatProbeRequest{
		Model:     modelID,
		Messages:  []openCodeZenChatProbeMessage{{Role: "user", Content: "ping"}},
		MaxTokens: 1,
	})
	if err != nil {
		return 0, fmt.Errorf("httpapi: opencode-zen chat probe marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(reqBody))
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

	// Fail-closed guard: a 401/403 whose body positively indicates a model or
	// request-shape problem must not be read as an invalid credential. The
	// body is read ONLY to classify — its text never leaves this function.
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		probeBody, _ := io.ReadAll(io.LimitReader(resp.Body, openCodeZenProbeBodyLimit))
		if openCodeZenBodyIsModelOrShapeProblem(probeBody) {
			return 0, errOpenCodeZenModelOrShapeRejection
		}
		return resp.StatusCode, nil
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// resolveOpenCodeZenModelID reads GET {baseURL}/v1/models and returns the
// first advertised model id. A transport/status failure or an empty catalog
// returns a non-nil error so the caller reports unavailable, never invalid.
func resolveOpenCodeZenModelID(ctx context.Context, baseURL, key string) (string, error) {
	body, err := openCodeZenModelsProbeSeam(ctx, baseURL, key)
	if err != nil {
		return "", fmt.Errorf("httpapi: opencode-zen chat probe resolve model: %w", err)
	}
	var list openCodeZenModelList
	if err := json.Unmarshal(body, &list); err != nil {
		return "", fmt.Errorf("httpapi: opencode-zen chat probe resolve model: parse models: %w", err)
	}
	if len(list.Data) == 0 || list.Data[0].ID == "" {
		return "", errors.New("httpapi: opencode-zen chat probe resolve model: catalog returned no usable model id")
	}
	return list.Data[0].ID, nil
}

// openCodeZenBodyIsModelOrShapeProblem reports whether a 401/403 response body
// positively indicates a MODEL or request-shape problem rather than an
// authentication failure. It keys only on markers the recorded model/shape
// responses carry ("ModelError", "... is not supported", "unsupported",
// "invalid_request") — the recorded auth responses ("Invalid API key." /
// "Missing API key.") carry none, so a genuine auth 401 is NOT matched and
// still classifies as invalid. When it cannot positively identify a model/
// shape problem it returns false; any misread therefore errs toward
// unavailable only on a positive signal, and toward invalid otherwise —
// preserving genuine auth-failure detection.
func openCodeZenBodyIsModelOrShapeProblem(body []byte) bool {
	hay := strings.ToLower(string(body))
	for _, marker := range []string{"modelerror", "not supported", "unsupported", "invalid_request", "invalid request"} {
		if strings.Contains(hay, marker) {
			return true
		}
	}
	return false
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

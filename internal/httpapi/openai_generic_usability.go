package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// classifyOpenAICompatibleChatUsability judges one plain OpenAI-compatible
// chat-completion probe (agnes-ai, ollama-cloud, nvidia-nim). Unlike zen,
// these providers signal with the HTTP STATUS first — the status alone
// resolves every non-2xx case; the body only refines a 2xx into usable vs.
// inconclusive. Per the design table (task-2 brief):
//
//   - 401                -> auth failure (account-level, not per-model)
//   - 402, 403, 404      -> paid-unusable (billing, entitlement, or the model
//     is definitively not servable for this account — all permanent verdicts)
//   - 429                -> free-exhausted (transient, re-probed after backoff)
//   - 2xx w/ choices      -> usable
//   - 2xx w/o choices, 5xx, anything else -> inconclusive (establishes nothing)
func classifyOpenAICompatibleChatUsability(status int, body []byte) zenChatUsability {
	switch status {
	case http.StatusUnauthorized:
		return zenChatAuthFailure
	case http.StatusPaymentRequired, http.StatusForbidden, http.StatusNotFound:
		return zenChatPaidUnusable
	case http.StatusTooManyRequests:
		return zenChatFreeExhausted
	}

	if status < 200 || status >= 300 {
		return zenChatInconclusive
	}

	var env struct {
		Choices []json.RawMessage `json:"choices"`
	}
	// A parse failure (including an empty body) leaves env zero-valued, so the
	// length check below also fails -> inconclusive, never a guessed success.
	_ = json.Unmarshal(body, &env)
	if len(env.Choices) > 0 {
		return zenChatUsable
	}
	return zenChatInconclusive
}

// probeOpenAICompatibleChatUsability runs ONE minimal real chat completion
// against a plain OpenAI-compatible endpoint and classifies the outcome. It
// serves all three providers that speak this exact wire shape (agnes-ai,
// ollama-cloud, nvidia-nim): identical bearer auth, identical
// "{base}/chat/completions" path. baseURL is a liveProviderBaseURLs entry,
// which already carries that provider's version segment (e.g. ".../v1") — the
// probe appends ONLY "/chat/completions", never its own version prefix.
//
// The error is non-nil ONLY on a transport failure (the model's usability is
// then simply unknown) — never on a provider error response, which is a real
// verdict the classifier reads from the status/body. key travels only as the
// Authorization header and is never logged; the body is read only to classify,
// bounded by the existing openCodeZenProbeBodyLimit.
func probeOpenAICompatibleChatUsability(ctx context.Context, baseURL, key, modelID string) (usabilityProbeResult, error) {
	reqBody, err := json.Marshal(openCodeZenChatProbeRequest{
		Model:     modelID,
		Messages:  []openCodeZenChatProbeMessage{{Role: "user", Content: "ping"}},
		MaxTokens: 1,
	})
	if err != nil {
		return usabilityProbeResult{Verdict: zenChatInconclusive}, fmt.Errorf("httpapi: openai-compatible usability probe marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return usabilityProbeResult{Verdict: zenChatInconclusive}, fmt.Errorf("httpapi: openai-compatible usability probe request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := openCodeZenHTTPClient.Do(req)
	if err != nil {
		return usabilityProbeResult{Verdict: zenChatInconclusive}, fmt.Errorf("httpapi: openai-compatible usability probe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, openCodeZenProbeBodyLimit))
	verdict := classifyOpenAICompatibleChatUsability(resp.StatusCode, body)
	return usabilityProbeResult{Verdict: verdict, RetryAfter: usabilityRetryAfter(resp.Header, body)}, nil
}

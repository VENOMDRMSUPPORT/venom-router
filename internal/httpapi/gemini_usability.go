package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// geminiChatProbeRequest is the minimal generateContent request body the
// usability probe sends (task-3 brief): a single "ping" user turn with the
// output capped at 1 token, so the probe classifies reachability/entitlement
// without spending a real completion's worth of tokens.
type geminiChatProbeRequest struct {
	Contents         []geminiChatProbeContent        `json:"contents"`
	GenerationConfig geminiChatProbeGenerationConfig `json:"generationConfig"`
}

type geminiChatProbeContent struct {
	Role  string                `json:"role"`
	Parts []geminiChatProbePart `json:"parts"`
}

type geminiChatProbePart struct {
	Text string `json:"text"`
}

type geminiChatProbeGenerationConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens"`
}

// geminiChatUsabilityEnvelope is the subset of a generateContent response this
// classifier reads: Google's error envelope's machine-readable `status`
// string, and the presence of a `candidates` array (a well-formed
// completion). Both fields live in the SAME struct (mirroring
// zenErrorEnvelope) so one Unmarshal call serves both the error and the
// success path.
type geminiChatUsabilityEnvelope struct {
	Error struct {
		Status string `json:"status"`
	} `json:"error"`
	Candidates []json.RawMessage `json:"candidates"`
}

// classifyGeminiChatUsability judges a single generateContent probe result
// against the taxonomy in the task-3 brief. The body wins over the HTTP
// status, exactly like zen: when the response parses into a recognized
// Google error envelope (a non-empty `error.status`), that string is
// authoritative regardless of the HTTP code — a 200 that somehow carries an
// error envelope is never blessed as usable, and an unrecognized status
// string is inconclusive rather than guessed from the HTTP code. Only when no
// error envelope is present does the HTTP status decide (covering providers
// or proxies that signal purely by status), and only a 2xx with a non-empty
// `candidates` array is usable.
func classifyGeminiChatUsability(status int, body []byte) zenChatUsability {
	var env geminiChatUsabilityEnvelope
	// A parse failure (including non-JSON garbage bytes) leaves env
	// zero-valued; every check below then falls through to inconclusive.
	_ = json.Unmarshal(body, &env)

	if env.Error.Status != "" {
		switch env.Error.Status {
		case "UNAUTHENTICATED":
			return zenChatAuthFailure
		case "PERMISSION_DENIED", "NOT_FOUND":
			return zenChatPaidUnusable
		case "RESOURCE_EXHAUSTED":
			return zenChatFreeExhausted
		default:
			// A recognized error SHAPE but an unknown status: a real failure
			// occurred but its meaning is unjudged — inconclusive, never a guess.
			return zenChatInconclusive
		}
	}

	switch status {
	case http.StatusUnauthorized:
		return zenChatAuthFailure
	case http.StatusForbidden, http.StatusNotFound:
		return zenChatPaidUnusable
	case http.StatusTooManyRequests:
		return zenChatFreeExhausted
	}

	if status >= 200 && status < 300 && len(env.Candidates) > 0 {
		return zenChatUsable
	}
	return zenChatInconclusive
}

// probeGeminiChatUsability runs ONE minimal real generateContent call for a
// SPECIFIC gemini-cli model and returns the usability verdict. Gemini is NOT
// OpenAI-shaped (internal/providers/gemini_cli.go, liveProviderBaseURLs):
// baseURL already carries the version segment ("{base}/v1beta" — see
// chatcompletions.go's liveProviderBaseURLs), so the probe appends ONLY
// "/models/{modelID}:generateContent", never its own version prefix; auth
// travels as the x-goog-api-key header, never Authorization.
//
// The error is non-nil ONLY on a transport failure (the model's usability is
// then simply unknown) — never on a provider error response, which is a real
// verdict the classifier reads from the body. key travels only as the
// x-goog-api-key header and is never logged; the body is read only to
// classify, bounded by the existing openCodeZenProbeBodyLimit.
func probeGeminiChatUsability(ctx context.Context, baseURL, key, modelID string) (usabilityProbeResult, error) {
	reqBody, err := json.Marshal(geminiChatProbeRequest{
		Contents: []geminiChatProbeContent{
			{Role: "user", Parts: []geminiChatProbePart{{Text: "ping"}}},
		},
		GenerationConfig: geminiChatProbeGenerationConfig{MaxOutputTokens: 1},
	})
	if err != nil {
		return usabilityProbeResult{Verdict: zenChatInconclusive}, fmt.Errorf("httpapi: gemini usability probe marshal: %w", err)
	}

	url := baseURL + "/models/" + modelID + ":generateContent"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return usabilityProbeResult{Verdict: zenChatInconclusive}, fmt.Errorf("httpapi: gemini usability probe request: %w", err)
	}
	req.Header.Set("x-goog-api-key", key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := openCodeZenHTTPClient.Do(req)
	if err != nil {
		return usabilityProbeResult{Verdict: zenChatInconclusive}, fmt.Errorf("httpapi: gemini usability probe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, openCodeZenProbeBodyLimit))
	verdict := classifyGeminiChatUsability(resp.StatusCode, body)
	return usabilityProbeResult{Verdict: verdict, RetryAfter: usabilityRetryAfter(resp.Header, body)}, nil
}

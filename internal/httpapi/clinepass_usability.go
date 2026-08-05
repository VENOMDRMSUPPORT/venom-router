package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// clinePassUsabilityTimeout bounds one usability chat completion. The legacy
// reference used 30s for its model test; the sweep's own 25s budget is the
// harder ceiling, so the client timeout just has to outlast a slow model's
// short completion.
const clinePassUsabilityTimeout = 20 * time.Second

var clinePassUsabilityHTTPClient = &http.Client{Timeout: clinePassUsabilityTimeout}

// clinePassProbeBodyLimit mirrors openCodeZenProbeBodyLimit's rationale, but
// completions carry real content, so it is roomier.
const clinePassProbeBodyLimit = 64 << 10

// clinePassModelTestPrompt / clinePassModelTestMaxTokens are the legacy
// reference's PROVEN model test (docs/evidence/clinepass-legacy-wire-reference
// .md §7): ask for the literal word "ok" and validate the response TEXT — a
// 200 with no real completion is a failure, never a working model.
const (
	clinePassModelTestPrompt    = `Reply with exactly the word "ok" and nothing else.`
	clinePassModelTestMaxTokens = 256
)

var clinePassOkPattern = regexp.MustCompile(`(?i)\bok\b`)

// clinePassChatProbeResponse is the subset of a clinepass chat completion the
// usability classifier reads. The NON-STREAM completion is ENVELOPED
// ({success, data:{choices}}); errors may be either the envelope's string
// ({success:false, error:"..."}) or an OpenAI-style {error:{code,message}}.
type clinePassChatProbeResponse struct {
	Success *bool `json:"success"`
	Data    *struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	} `json:"data"`
	Error json.RawMessage `json:"error"`
}

// clinePassErrorText extracts the human/machine error wording from either
// error shape (string or {code,message}); "" when neither is present.
func clinePassErrorText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	var asObject struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &asObject); err == nil {
		return strings.TrimSpace(asObject.Code + " " + asObject.Message)
	}
	return ""
}

// classifyClinePassChatUsability judges one clinepass chat-completion probe,
// mapping onto the shared usability vocabulary (zenChatUsability — the name
// is historical; the vocabulary is provider-agnostic). The mapping follows
// the legacy classifier (reference §7):
//
//   - 2xx + enveloped choices whose text contains the word "ok" → usable.
//     A well-formed 200 WITHOUT that proof is inconclusive — "responded" is
//     not "works" (the owner's explicit requirement), and inconclusive means
//     re-probed later, never a false positive either way.
//   - 401/403 with subscription wording → the account cannot use this model
//     tier (definitive per-account unsupported); with token wording → an
//     ACCOUNT-level auth failure that stops the sweep.
//   - 402 / credits wording, and 429 → transient (retry after backoff): a
//     spent balance or rate limit is never a permanent per-model verdict.
//   - 400/404 model-not-found wording → definitive unsupported.
//   - 5xx / anything unrecognized → inconclusive.
func classifyClinePassChatUsability(status int, body []byte) zenChatUsability {
	var resp clinePassChatProbeResponse
	_ = json.Unmarshal(body, &resp)
	errText := strings.ToLower(clinePassErrorText(resp.Error))

	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		if strings.Contains(errText, "not subscribed") ||
			strings.Contains(errText, "subscription") ||
			strings.Contains(errText, "upgrade") ||
			strings.Contains(errText, "individual inference") {
			return zenChatPaidUnusable
		}
		return zenChatAuthFailure
	}
	if status == http.StatusTooManyRequests || strings.Contains(errText, "rate limit") {
		return zenChatFreeExhausted
	}
	if status == http.StatusPaymentRequired ||
		strings.Contains(errText, "insufficient") ||
		strings.Contains(errText, "balance") ||
		strings.Contains(errText, "credits") ||
		strings.Contains(errText, "out of credits") {
		return zenChatFreeExhausted
	}
	if status == http.StatusBadRequest || status == http.StatusNotFound {
		if strings.Contains(errText, "not available") ||
			strings.Contains(errText, "not found") ||
			strings.Contains(errText, "unsupported model") ||
			strings.Contains(errText, "does not exist") {
			return zenChatPaidUnusable
		}
		return zenChatInconclusive
	}
	if status < 200 || status >= 300 {
		return zenChatInconclusive
	}

	if resp.Data == nil || len(resp.Data.Choices) == 0 {
		return zenChatInconclusive
	}
	text := resp.Data.Choices[0].Message.Content
	if strings.TrimSpace(text) == "" {
		text = resp.Data.Choices[0].Message.ReasoningContent
	}
	if clinePassOkPattern.MatchString(text) {
		return zenChatUsable
	}
	return zenChatInconclusive
}

// probeClinePassChatUsability runs ONE real chat completion against a SPECIFIC
// clinepass model and classifies the outcome. `credentialPlaintext` is the
// account's stored credential VALUE (the adapter's token JSON) exactly as the
// lease hands it over — the access token is extracted here and travels only as
// the workos-prefixed Authorization header, never logged. The error is non-nil
// ONLY on a transport failure (usability then unknown); a provider error
// response is a verdict the classifier reads from the body.
func probeClinePassChatUsability(ctx context.Context, baseURL, credentialPlaintext, modelID string) (usabilityProbeResult, error) {
	var stored struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal([]byte(credentialPlaintext), &stored); err != nil || stored.AccessToken == "" {
		// Not a parseable token envelope — an account-level credential
		// problem, not a per-model verdict.
		return usabilityProbeResult{Verdict: zenChatAuthFailure}, nil
	}

	reqBody, err := json.Marshal(openCodeZenChatProbeRequest{
		Model:     modelID,
		Messages:  []openCodeZenChatProbeMessage{{Role: "user", Content: clinePassModelTestPrompt}},
		MaxTokens: clinePassModelTestMaxTokens,
	})
	if err != nil {
		return usabilityProbeResult{Verdict: zenChatInconclusive}, fmt.Errorf("httpapi: clinepass usability probe marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/v1/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return usabilityProbeResult{Verdict: zenChatInconclusive}, fmt.Errorf("httpapi: clinepass usability probe request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+clinePassWorkosPrefixed(stored.AccessToken))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "https://cline.bot")
	req.Header.Set("X-Title", "Cline")
	req.Header.Set("X-CLIENT-TYPE", "venom-router")

	resp, err := clinePassUsabilityHTTPClient.Do(req)
	if err != nil {
		return usabilityProbeResult{Verdict: zenChatInconclusive}, fmt.Errorf("httpapi: clinepass usability probe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, clinePassProbeBodyLimit))
	verdict := classifyClinePassChatUsability(resp.StatusCode, body)
	return usabilityProbeResult{Verdict: verdict, RetryAfter: usabilityRetryAfter(resp.Header, body)}, nil
}

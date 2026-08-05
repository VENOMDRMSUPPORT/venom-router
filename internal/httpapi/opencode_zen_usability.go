package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
)

// zenChatUsability is the semantic verdict of one real opencode-zen chat
// completion, used by the chat-usability probe to decide whether a specific
// model actually works for THIS account (design 2026-08-03). It is deliberately
// NOT derived from the HTTP status alone: opencode returns errors inside the
// body (sometimes even under a 200), and the SAME 401 can mean three different
// things, so only the body's error taxonomy is authoritative.
type zenChatUsability int

const (
	// zenChatInconclusive: no recognized outcome — a malformed/empty body, an
	// unknown error type, or a 2xx that is not a well-formed completion. Never
	// treated as either working or permanently broken.
	zenChatInconclusive zenChatUsability = iota
	// zenChatUsable: the model returned a well-formed completion (a choices
	// array), so it responded — usable. Empty visible content still counts: a
	// reasoning model can spend the whole tiny max_tokens budget on
	// reasoning_content (big-pickle does exactly this).
	zenChatUsable
	// zenChatFreeExhausted: a FreeUsageLimitError — the model IS free but its
	// free quota is spent RIGHT NOW. Transient, never a permanent verdict: the
	// offering stays listed and is re-probed after backoff.
	zenChatFreeExhausted
	// zenChatPaidUnusable: a CreditsError or GoUsageLimitError — the model is a
	// paid tier the account cannot use for free. Excluded from the free-usable set.
	zenChatPaidUnusable
	// zenChatAuthFailure: an AuthError — the credential itself is rejected. This
	// is an ACCOUNT-level problem, not a per-model verdict, so it must not mark
	// the model unusable.
	zenChatAuthFailure
)

// zenErrorEnvelope is the subset of an opencode-zen response body the usability
// classifier reads: the error object's `type` (the machine signal) and the
// presence of a `choices` array (a well-formed completion). Both an OpenAI-style
// top-level error and zen's `{"type":"error","error":{...}}` wrapper populate
// Error.Type.
type zenErrorEnvelope struct {
	Error struct {
		Type string `json:"type"`
	} `json:"error"`
	Choices []json.RawMessage `json:"choices"`
}

// classifyOpenCodeZenChatUsability judges a single chat-completion probe result.
// The body wins over the status: an error envelope is classified by its error
// type regardless of the HTTP code (so a 200 carrying an error is never blessed
// as usable), and only a well-formed completion with no error envelope, on a
// 2xx, is usable.
func classifyOpenCodeZenChatUsability(status int, body []byte) zenChatUsability {
	var env zenErrorEnvelope
	// A parse failure leaves env zero-valued; the switch below then falls
	// through to the status/choices checks, which also fail -> inconclusive.
	_ = json.Unmarshal(body, &env)

	if env.Error.Type != "" {
		switch {
		case strings.EqualFold(env.Error.Type, "FreeUsageLimitError"):
			return zenChatFreeExhausted
		case strings.EqualFold(env.Error.Type, "CreditsError"),
			strings.EqualFold(env.Error.Type, "GoUsageLimitError"):
			return zenChatPaidUnusable
		case strings.EqualFold(env.Error.Type, "AuthError"):
			return zenChatAuthFailure
		default:
			// A recognized error SHAPE but an unknown type: we saw a failure but
			// cannot judge what it means — inconclusive, never a guess.
			return zenChatInconclusive
		}
	}

	if status >= 200 && status < 300 && len(env.Choices) > 0 {
		return zenChatUsable
	}
	return zenChatInconclusive
}

// usabilityRetryAfter extracts the provider's advertised backoff: the JSON
// body's retry-after-ms / retry-after fields win over the HTTP Retry-After
// header (seconds). 0 = nothing advertised.
func usabilityRetryAfter(header http.Header, body []byte) time.Duration {
	var env struct {
		Error struct {
			RetryAfterMS int `json:"retry-after-ms"`
			RetryAfter   int `json:"retry-after"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	if env.Error.RetryAfterMS > 0 {
		return time.Duration(env.Error.RetryAfterMS) * time.Millisecond
	}
	if env.Error.RetryAfter > 0 {
		return time.Duration(env.Error.RetryAfter) * time.Second
	}
	if s := header.Get("Retry-After"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 0
}

// probeOpenCodeZenChatUsability runs ONE minimal real chat completion for a
// SPECIFIC model (discovery supplies the id) and returns the usability verdict.
// It is the per-model usability probe, distinct from the account-health seam:
// it targets a named model and reads the raw body so classifyOpenCodeZenChat-
// Usability can distinguish working / free-exhausted / paid / auth outcomes.
//
// The error is non-nil ONLY on a transport failure (the model's usability is
// then simply unknown) — never on a provider error response, which is a real
// verdict the classifier reads from the body. key travels only as the
// Authorization header and is never logged; the body is read only to classify.
func probeOpenCodeZenChatUsability(ctx context.Context, baseURL, key, modelID string) (usabilityProbeResult, error) {
	reqBody, err := json.Marshal(openCodeZenChatProbeRequest{
		Model:     modelID,
		Messages:  []openCodeZenChatProbeMessage{{Role: "user", Content: "ping"}},
		MaxTokens: 1,
	})
	if err != nil {
		return usabilityProbeResult{Verdict: zenChatInconclusive}, fmt.Errorf("httpapi: opencode-zen usability probe marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return usabilityProbeResult{Verdict: zenChatInconclusive}, fmt.Errorf("httpapi: opencode-zen usability probe request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := openCodeZenHTTPClient.Do(req)
	if err != nil {
		return usabilityProbeResult{Verdict: zenChatInconclusive}, fmt.Errorf("httpapi: opencode-zen usability probe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, openCodeZenProbeBodyLimit))
	verdict := classifyOpenCodeZenChatUsability(resp.StatusCode, body)
	return usabilityProbeResult{Verdict: verdict, RetryAfter: usabilityRetryAfter(resp.Header, body)}, nil
}

// zenUsabilitySignal maps a chat-usability verdict onto the EXISTING
// probe-signal vocabulary (intelligence, 04 §2/§5) so a usability result drives
// the very same certification machinery every other probe does — no parallel
// taxonomy. The chosen signals encode the design's intent exactly:
//
//   - usable        -> capability_response (definitive supported -> routable chat)
//   - paid-unusable -> semantic_rejection  (definitive unsupported: the account
//     cannot use this paid model, a proven-negative verdict)
//   - free-exhausted-> rate_limited        (retryable, reschedules: a spent free
//     quota is transient, never a permanent unsupported)
//   - auth-failure  -> unauthorized        (terminal credential block; the caller
//     also stops probing the rest of the account)
//   - inconclusive  -> malformed_request   (establishes nothing)
func zenUsabilitySignal(v zenChatUsability) intelligence.ProbeSignalKind {
	switch v {
	case zenChatUsable:
		return intelligence.SignalCapabilityResponse
	case zenChatPaidUnusable:
		return intelligence.SignalSemanticRejection
	case zenChatFreeExhausted:
		return intelligence.SignalRateLimited
	case zenChatAuthFailure:
		return intelligence.SignalUnauthorized
	default:
		return intelligence.SignalMalformedRequest
	}
}

// zenUsabilityProbeOutcome bridges a chat-usability verdict to the ProbeOutcome
// the CertificationDriver.RecordAttempt consumes, via the shared taxonomy.
func zenUsabilityProbeOutcome(v zenChatUsability) (intelligence.ProbeOutcome, error) {
	return intelligence.ClassifyProbeSignal(zenUsabilitySignal(v))
}

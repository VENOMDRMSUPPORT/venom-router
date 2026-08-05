package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// claudeCodeMessagesAnthropicBeta / claudeCodeMessagesAppHeader /
// claudeCodeMessagesUserAgent are the headers the production
// anthropic_messages codec sets on EVERY authenticated /v1/messages call
// (internal/execution/anthropicwire.go's anthropicBetaHeader/anthropicApp-
// Header/anthropicUserAgentHeader constants, applied by
// nativeoauth.go's anthropicMessagesCodec.applyHeaders). They are copied here
// rather than guessed, and deliberately NOT reused from claude_code_seams.go's
// claudeCodeAppHeader/claudeCodeUserAgent — those back the GET identity/usage
// seam (a different call path with its own claude-cli version string), not
// the /v1/messages codec this probe exercises. Must stay in sync with
// anthropicwire.go.
const (
	claudeCodeMessagesAnthropicBeta = "oauth-2025-04-20"
	claudeCodeMessagesAppHeader     = "cli"
	claudeCodeMessagesUserAgent     = "claude-cli/0.1 (venom-router)"
)

// claudeCodeUsabilityEnvelope is the subset of an Anthropic /v1/messages
// response the usability classifier reads: the error envelope's machine
// `error.type` string, and the presence of a non-empty `content` array (a
// well-formed completion). Both fields live in the SAME struct (mirroring
// zenErrorEnvelope / geminiChatUsabilityEnvelope) so one Unmarshal call serves
// both the error and the success path.
type claudeCodeUsabilityEnvelope struct {
	Error struct {
		Type string `json:"type"`
	} `json:"error"`
	Content []json.RawMessage `json:"content"`
}

// classifyClaudeCodeChatUsability judges a single minimal /v1/messages probe
// result against the task-4 brief's taxonomy. The body wins over the HTTP
// status: Anthropic's error envelope ({"type":"error","error":{"type":...}})
// is classified by its error.type regardless of the HTTP code, so a 200 that
// somehow carries an error envelope is never blessed as usable. Only when no
// error envelope is present does a 2xx with a non-empty `content` array count
// as usable; anything else (unknown error type, malformed body, a 2xx without
// content) is inconclusive — a real outcome was not established.
func classifyClaudeCodeChatUsability(status int, body []byte) zenChatUsability {
	var env claudeCodeUsabilityEnvelope
	// A parse failure (including non-JSON garbage bytes) leaves env
	// zero-valued; every check below then falls through to inconclusive.
	_ = json.Unmarshal(body, &env)

	if env.Error.Type != "" {
		switch env.Error.Type {
		case "authentication_error":
			return zenChatAuthFailure
		case "permission_error", "billing_error":
			return zenChatPaidUnusable
		case "not_found_error":
			// The model is not on this plan — a definitive per-account
			// unsupported, not a transport/discovery problem.
			return zenChatPaidUnusable
		case "rate_limit_error", "overloaded_error":
			return zenChatFreeExhausted
		default:
			// A recognized error SHAPE but an unknown type: a real failure
			// occurred but its meaning is unjudged — inconclusive, never a guess.
			return zenChatInconclusive
		}
	}

	if status >= 200 && status < 300 && len(env.Content) > 0 {
		return zenChatUsable
	}
	return zenChatInconclusive
}

// probeClaudeCodeChatUsability runs ONE minimal real /v1/messages completion
// for a SPECIFIC model against a claude-code account and returns the
// usability verdict. credentialPlaintext is the account's stored credential
// VALUE (the adapter's token JSON, exactly as clinepass's) — the access token
// is extracted here and travels only as the Authorization bearer header,
// never logged. An unparseable/empty access_token is an account-level
// credential problem (zenChatAuthFailure), decided WITHOUT any HTTP call,
// exactly like probeClinePassChatUsability.
//
// The request is the smallest possible spend (spec D6): max_tokens: 1, the
// single word "ping" — this runs against the owner's real subscription. The
// anthropic-version, anthropic-beta, X-App, and User-Agent headers are all
// copied verbatim from the production anthropic_messages codec
// (claudeCodeAnthropicVersion / claudeCodeMessagesAnthropicBeta /
// claudeCodeMessagesAppHeader / claudeCodeMessagesUserAgent), never guessed —
// 03 §3 documents that any missing/wrong required header draws a 429.
//
// The error is non-nil ONLY on a transport failure (the model's usability is
// then simply unknown) — never on a provider error response, which is a real
// verdict the classifier reads from the body. The body is read only to
// classify, bounded by the existing openCodeZenProbeBodyLimit.
func probeClaudeCodeChatUsability(ctx context.Context, baseURL, credentialPlaintext, modelID string) (usabilityProbeResult, error) {
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
		Messages:  []openCodeZenChatProbeMessage{{Role: "user", Content: "ping"}},
		MaxTokens: 1,
	})
	if err != nil {
		return usabilityProbeResult{Verdict: zenChatInconclusive}, fmt.Errorf("httpapi: claude-code usability probe marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/messages", bytes.NewReader(reqBody))
	if err != nil {
		return usabilityProbeResult{Verdict: zenChatInconclusive}, fmt.Errorf("httpapi: claude-code usability probe request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+stored.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", claudeCodeAnthropicVersion)
	req.Header.Set("anthropic-beta", claudeCodeMessagesAnthropicBeta)
	req.Header.Set("X-App", claudeCodeMessagesAppHeader)
	req.Header.Set("User-Agent", claudeCodeMessagesUserAgent)

	resp, err := claudeCodeHTTPClient.Do(req)
	if err != nil {
		return usabilityProbeResult{Verdict: zenChatInconclusive}, fmt.Errorf("httpapi: claude-code usability probe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, openCodeZenProbeBodyLimit))
	verdict := classifyClaudeCodeChatUsability(resp.StatusCode, body)
	return usabilityProbeResult{Verdict: verdict, RetryAfter: usabilityRetryAfter(resp.Header, body)}, nil
}

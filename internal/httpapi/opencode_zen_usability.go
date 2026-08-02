package httpapi

import (
	"encoding/json"
	"strings"
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

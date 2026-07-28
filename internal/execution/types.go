// Package execution contains the single InferenceTransport dispatcher
// (01 §4.5): the one entry point every provider's inference call flows
// through, regardless of auth mode or transport type. This unit
// (P0-EXEC-002) freezes the seam's types and the dispatcher skeleton
// only — no transport implementation exists here yet (the Bifrost smoke
// shim is P0-EXEC-003; native/custom transports are P4).
package execution

import (
	"context"
	"time"
)

// ProviderID identifies a provider. Its canonical definition and catalog
// belong to internal/providers (not yet built) — this is a minimal,
// forward-compatible placeholder so ResolvedRoute and the dispatcher can
// be typed now, rather than stringly keyed.
type ProviderID string

// StoredCredentials is the resolved credential material a route carries.
// Its real shape (API key vs OAuth token, expiry, etc.) belongs to
// internal/secrets (P1) — this is a minimal placeholder so
// ResolvedRoute's Credential field is typed at skeleton stage.
type StoredCredentials struct {
	Value string
}

// ResolvedRoute is a fully-decided single-choice route (01 §4.1). The
// transport receives this and can neither re-select nor widen it — every
// InferenceTransport method takes it by value, so a transport cannot
// mutate the caller's copy, only act on its own.
type ResolvedRoute struct {
	Provider   ProviderID
	AccountID  string
	Credential StoredCredentials
	ModelID    string
	BaseURL    string // resolved by Venom before dispatch
}

// Operation identifies a kind of inference capability (01 §4.1's
// SupportedCapabilities comment: "e.g. chat, streaming, tools, vision").
// Additional operations are added only as the tier engine and provider
// certification actually need them, not invented speculatively here.
type Operation string

const (
	OperationChat      Operation = "chat"
	OperationStreaming Operation = "streaming"
	OperationTools     Operation = "tools"
	OperationVision    Operation = "vision"
)

// Message is a single normalized chat message.
type Message struct {
	Role    string
	Content string
}

// NormalizedRequest is Venom's provider-agnostic representation of an
// inference request. Its shape here is intentionally minimal: this unit
// freezes the InferenceTransport seam, not the tier engine's actual
// request contract (P3a/P5 own that).
type NormalizedRequest struct {
	Operation Operation
	Messages  []Message
	Stream    bool
	// MaxTokens is the request's max_tokens; nil means the provider's own
	// default. quota.EstimateInput.MaxOutputTokens has referred to exactly
	// this field since P3b — this is that field's first concrete carrier
	// on the seam (P3c-EXEC-001).
	MaxTokens *int
	// RequestID is the correlation id Cancel has demanded since P0
	// (Cancel(ctx, route, requestID)). Empty means "not cancellable by id"
	// — context cancellation still works. Transports index in-flight calls
	// by this id when non-empty (P4-EXEC-003).
	RequestID string
}

// ToolCall is one tool invocation a provider's response carries — the
// minimal shape a witness classification needs (04 §2/§5): which tool,
// and its raw argument payload. Argument VALUES are provider-controlled
// content, not Venom-generated, so they travel as an opaque JSON string
// rather than a parsed structure this seam would have to interpret.
type ToolCall struct {
	Name          string
	ArgumentsJSON string
}

// NormalizedResponse is Venom's provider-agnostic representation of a
// completed, non-streamed inference response.
type NormalizedResponse struct {
	Message Message
	// Content is already carried by Message; ToolCalls and HTTPStatus are
	// the two additional facts a probe witness classification needs and
	// nothing more (P3c-EXEC-001) — a non-empty ToolCalls or an HTTPStatus
	// somewhere else in the response is never guessed at, only what the
	// transport actually observed.
	ToolCalls    []ToolCall
	HTTPStatus   int
	FinishReason string
}

// Chunk is a single unit of a streamed response.
type Chunk struct {
	Delta string
	Done  bool
	Err   error
}

// VenomError is the internal representation of the stable error envelope
// documented in 08 §3 / 09 §2 ({error:{code,message,request_id,
// retryable,details?}}). Wire (JSON) encoding is httpapi's concern
// (P2b+), not this package's.
type VenomError struct {
	Code      string
	Message   string
	RequestID string
	Retryable bool
	Details   map[string]any
}

// FailureClass is the high-level error category, independent of scope.
type FailureClass string

const (
	FailureClassInvalidRequest FailureClass = "invalid_request" // bad prompt/schema/param
	FailureClassAuth           FailureClass = "auth_error"      // expired/invalid credential
	FailureClassNotFound       FailureClass = "not_found"       // model missing/disabled
	FailureClassQuota          FailureClass = "quota_error"     // account quota exhausted
	FailureClassRateLimit      FailureClass = "rate_limit"      // model-specific rate limit
	FailureClassServer         FailureClass = "server_error"    // provider outage / 5xx
	FailureClassNetwork        FailureClass = "network_error"   // timeout / DNS / reset
)

// FailureScope identifies which boundary a failure should cool down or
// bypass.
type FailureScope string

const (
	FailureScopeRequest            FailureScope = "request"
	FailureScopeAccount            FailureScope = "account"
	FailureScopeOffering           FailureScope = "offering"
	FailureScopeProvider           FailureScope = "provider"
	FailureScopeTransientTransport FailureScope = "transient_transport"
)

// TypedFailure is the normalized error envelope returned by
// InferenceTransport.NormalizeError. Fields are populated by priority:
// provider semantic code → HTTP headers → adapter rules → HTTP fallback
// (01 §4.2).
type TypedFailure struct {
	FailureClass  FailureClass   // high-level error category (independent of scope)
	Scope         FailureScope   // which boundary to cooldown / bypass
	Retryable     bool           // whether retry (possibly after cooldown) may succeed
	CooldownUntil *time.Time     // when the scope may be retried (nil = no cooldown needed)
	RetryAfter    *int           // seconds parsed from Retry-After header (nil = no signal)
	QuotaResetAt  *time.Time     // when the account/offering quota resets (nil = unknown)
	ProviderCode  string         // provider-native error code, if available
	HTTPStatus    int            // HTTP status code (0 if not HTTP)
	SafeMessage   string         // user-safe error description, never raw provider text
	Evidence      map[string]any // sanitized diagnostic data for observability
	// RawMessage is the provider's unredacted text. Probe-path use only:
	// intelligence redacts it before it ever becomes evidence (GOVERNOR
	// DECISION, P3c-EXEC-001's raw-text exception). Never logged, never
	// returned on the wire, never consulted by the ordinary routing path —
	// NormalizeError's SafeMessage remains the only error text that path
	// ever sees.
	RawMessage string
}

// InferenceTransport executes an already-decided inference call. One
// implementation per transport type (bifrost, native_api, native_oauth,
// openai_compatible, custom — 01 §4.3), never one per provider slug.
type InferenceTransport interface {
	// Execute sends a single non-streamed request.
	Execute(ctx context.Context, route ResolvedRoute, req NormalizedRequest) (*NormalizedResponse, error)

	// Stream sends a streaming request and returns a channel of chunks.
	Stream(ctx context.Context, route ResolvedRoute, req NormalizedRequest) (<-chan Chunk, error)

	// Cancel aborts an in-flight stream or request.
	Cancel(ctx context.Context, route ResolvedRoute, requestID string) error

	// NormalizeError converts a provider-native error into the stable
	// error envelope. Must never leak credentials or raw provider text.
	NormalizeError(err error, route ResolvedRoute) VenomError

	// Failure is NormalizeError's richer sibling (P3c-EXEC-001): it
	// returns the already-defined TypedFailure, including HTTPStatus,
	// ProviderCode, and RawMessage — fields NormalizeError's VenomError
	// cannot carry. NormalizeError's existing signature and contract are
	// untouched by this addition, so no existing caller on the ordinary
	// routing path changes; Failure exists for the probe path (and for
	// P4-ROUTE-014, later) to consult instead.
	Failure(err error, route ResolvedRoute) TypedFailure

	// SupportedCapabilities returns the operation set this transport can
	// handle for the given route. Used during capability certification,
	// never during routing (certification is pre-computed).
	SupportedCapabilities(route ResolvedRoute) []Operation
}

// classifyHTTPStatus maps a raw HTTP status code onto the coarse
// FailureClass every transport's Failure implementation reports — shared
// so bifrost.go and openaicompat.go agree on the same mapping rather than
// each inventing their own. This switches on an int, never a provider
// slug string, so it is outside CheckNoSlugSwitch's scope by construction.
func classifyHTTPStatus(status int) FailureClass {
	switch {
	case status == 401 || status == 403:
		return FailureClassAuth
	case status == 404:
		return FailureClassNotFound
	case status == 429:
		return FailureClassRateLimit
	case status >= 500:
		return FailureClassServer
	case status >= 400:
		return FailureClassInvalidRequest
	default:
		return FailureClassServer
	}
}

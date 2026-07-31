package httpapi

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/sanitize"
)

// venomVersion is the value stamped into X-Venom-Version. It is a package-level
// placeholder: the real build-time version scheme is P8-REL-005's concern, and
// internal/cli's unexported `version` is not importable here. When that lands it
// replaces this constant (the ONLY site that names the version) without touching
// any caller.
const venomVersion = "dev"

// venomTelemetry is the routing-outcome fact set the single X-Venom-* builder
// stamps (01 §6c). A pointer metric that is nil is UNKNOWN and its header is
// OMITTED — never fabricated as 0 (the one exception is the stream-start set,
// which passes explicit zero pointers per 01 §6c). Provider and Model ARE
// carried (01 §6c lists them); an account identifier, a credential, or a raw
// provider error is NEVER carried — AccountID is deliberately absent from this
// struct so it structurally cannot be stamped.
type venomTelemetry struct {
	RequestID string
	Tier      string
	Provider  string // served provider id; "" ⇒ omit
	Model     string // public tier model name (see writeVenomHeaders note); "" ⇒ omit
	Funding   string // "" ⇒ omit
	LatencyMS *int64 // nil ⇒ omit (unknown), never fabricated 0
	TokensIn  *int   // nil ⇒ omit
	TokensOut *int   // nil ⇒ omit
	Attempts  *int   // nil ⇒ omit

	// Thinking is the APPLIED thinking level for the served attempt (P5-PAPI-004,
	// 05 §1a); "" ⇒ omit. ThinkingClamped is the clamp indicator (tier-ceiling OR
	// per-offering certified-max); nil ⇒ omit (not applicable / unknown).
	Thinking        string
	ThinkingClamped *bool
}

// writeVenomHeaders is the ONE place any X-Venom-* header is written (a
// repo-shape test pins that no other production file mentions the prefix). Every
// string value passes the sanitize boundary before it is written, so a value
// that somehow carried an embedded credential/token shape is redacted exactly as
// it would be in a log line.
//
// NOTE on -Model: FallbackResult does not carry the served provider model id
// (the authorized P5-PAPI-004 additive change is thinking-only), so -Model
// reflects the requested PUBLIC tier model name ("venom/pro"). -Provider is the
// real served provider id. A later unit that adds the provider model id to the
// result can switch -Model to it without touching any caller.
func writeVenomHeaders(h http.Header, t venomTelemetry) {
	setSanitized(h, "X-Venom-Request-Id", t.RequestID)
	setSanitized(h, "X-Venom-Tier", t.Tier)
	setSanitized(h, "X-Venom-Provider", t.Provider)
	setSanitized(h, "X-Venom-Model", t.Model)
	setSanitized(h, "X-Venom-Funding", t.Funding)
	if t.LatencyMS != nil {
		h.Set("X-Venom-Latency-Ms", strconv.FormatInt(*t.LatencyMS, 10))
	}
	if t.TokensIn != nil {
		h.Set("X-Venom-Tokens-In", strconv.Itoa(*t.TokensIn))
	}
	if t.TokensOut != nil {
		h.Set("X-Venom-Tokens-Out", strconv.Itoa(*t.TokensOut))
	}
	if t.Attempts != nil {
		h.Set("X-Venom-Fallback-Attempts", strconv.Itoa(*t.Attempts))
	}
	setSanitized(h, "X-Venom-Thinking-Applied", t.Thinking)
	if t.ThinkingClamped != nil {
		h.Set("X-Venom-Thinking-Clamped", strconv.FormatBool(*t.ThinkingClamped))
	}
	h.Set("X-Venom-Version", venomVersion)
}

// stampRequestID writes X-Venom-Request-Id through the single builder, so an
// error response can carry (and correlate) a request id without any other file
// naming the header literal — the repo-shape guard stays satisfied.
func stampRequestID(h http.Header, id string) {
	setSanitized(h, "X-Venom-Request-Id", id)
}

// setSanitized writes a header only when the value is non-empty, passing it
// through sanitize.Text first. An empty value is an UNKNOWN dimension and its
// header is omitted rather than emitted blank.
func setSanitized(h http.Header, key, value string) {
	if value == "" {
		return
	}
	h.Set(key, sanitize.Text(value))
}

// streamStartTelemetry is the reduced set stamped as real HTTP headers at stream
// start (01 §6c's documented exception): the identity/version facts that are
// already known plus ZEROED metrics, because the true metrics are only known
// once the stream completes and are delivered in the trailing SSE comment. It
// deliberately does NOT include the provider (unknown to the sink at start); the
// provider travels in the trailer.
func streamStartTelemetry(requestID, tier, model string) venomTelemetry {
	zero64 := int64(0)
	zero := 0
	return venomTelemetry{
		RequestID: requestID,
		Tier:      tier,
		Model:     model,
		LatencyMS: &zero64,
		TokensIn:  &zero,
		TokensOut: &zero,
		Attempts:  &zero,
	}
}

// venomTrailerComment renders the FINAL telemetry as a single SSE comment line
// (a `: ` line, which every compliant SSE reader — including plain OpenAI SDKs —
// ignores). It carries the same key=value facts the headers would, omitting
// unknowns, so a client that wants the final routing outcome can read it without
// the response ever gaining a non-comment frame after it. The returned string
// includes the trailing blank line that terminates the SSE event.
func venomTrailerComment(t venomTelemetry) string {
	pairs := make([]string, 0, 8)
	add := func(k, v string) {
		if v != "" {
			pairs = append(pairs, k+"="+sanitize.Text(v))
		}
	}
	add("x-venom-request-id", t.RequestID)
	add("x-venom-tier", t.Tier)
	add("x-venom-provider", t.Provider)
	add("x-venom-model", t.Model)
	add("x-venom-funding", t.Funding)
	if t.LatencyMS != nil {
		add("x-venom-latency-ms", strconv.FormatInt(*t.LatencyMS, 10))
	}
	if t.TokensIn != nil {
		add("x-venom-tokens-in", strconv.Itoa(*t.TokensIn))
	}
	if t.TokensOut != nil {
		add("x-venom-tokens-out", strconv.Itoa(*t.TokensOut))
	}
	if t.Attempts != nil {
		add("x-venom-fallback-attempts", strconv.Itoa(*t.Attempts))
	}
	add("x-venom-thinking-applied", t.Thinking)
	if t.ThinkingClamped != nil {
		add("x-venom-thinking-clamped", strconv.FormatBool(*t.ThinkingClamped))
	}
	add("x-venom-version", venomVersion)
	sort.Strings(pairs)
	return ": " + strings.Join(pairs, "; ") + "\n\n"
}

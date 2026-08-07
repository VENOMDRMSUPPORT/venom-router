package intelligence

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/sanitize"
)

// ContextProbeInputTokens is the deliberately oversized declared input
// token count the context-window probe sends (04 §2: "~3,000,000
// tokens"). ContextProbeMaxOutputTokens is its max_tokens ("max_tokens:
// 8"). ProbeSnippetMaxRunes bounds the redacted evidence snippet.
const (
	ContextProbeInputTokens     = 3_000_000
	ContextProbeMaxOutputTokens = 8
	ProbeSnippetMaxRunes        = 200
)

// oversizedContextProbeFillerWord is repeated to build the context probe's
// actual oversized message body (whole-branch review, FIX 2). It is a
// single lowercase word plus a trailing space — never punctuation or a
// digit — deliberately so the filler text itself can never be
// misread as a context-limit number by ExtractContextLimit's own rung 4
// (a number within genericKeywordDistance of a keyword) if a provider ever
// echoes a fragment of the oversized request back inside its rejection.
const oversizedContextProbeFillerWord = "a "

// oversizedContextProbeContent builds the message body ContextProbe.Run
// actually sends: filler text repeated ContextProbeInputTokens times — the
// SAME constant ContextProbeInputTokens above declares to the quota
// accounting, never a second, independently chosen size that could drift
// from it. This is deliberate, not cosmetic: DeclaredInputTokens is quota
// bookkeeping ONLY (ProbeGuard reads it to reserve headroom; probeadapters.go's
// Probe never serializes it onto the wire), so without an actually large
// body the provider receives an empty "messages": [] request and rejects
// it with a missing-body error carrying no context-length signal — not the
// genuine context-length rejection this probe exists to elicit (measured:
// "400 messages: at least one message is required", RungNoSignal, no
// extraction ever possible). Built fresh per call, never a package-level
// var, so importing this package never pays this allocation — only an
// actual context-probe attempt (already the single most expensive probe in
// the system, 04 §2) does.
func oversizedContextProbeContent() string {
	return strings.Repeat(oversizedContextProbeFillerWord, ContextProbeInputTokens)
}

// ContextLimitRung is which rung of the extraction ladder produced a
// context-limit reading, first hit wins (04 §2).
type ContextLimitRung string

const (
	RungStructuredField ContextLimitRung = "structured_field"
	RungOpenAIPhrase    ContextLimitRung = "openai_phrase"
	RungProviderRegex   ContextLimitRung = "provider_regex"
	RungGenericKeyword  ContextLimitRung = "generic_keyword"
	RungNoSignal        ContextLimitRung = "no_signal"
)

// ContextLimitRule is one provider-specific rung-3 pattern. Pattern's
// first capture group is the limit (digits, optionally comma-grouped). An
// empty rule set simply skips rung 3 for every provider.
type ContextLimitRule struct {
	ProviderID string
	Pattern    *regexp.Regexp
}

// openAIPhraseRe matches OpenAI's "maximum context length is N tokens"
// rejection phrasing (rung 2). It is only consulted when the caller has
// already confirmed res.ProviderCode == "context_length_exceeded" — an
// ungated phrase match is a defect (04 §2).
var openAIPhraseRe = regexp.MustCompile(`(?i)maximum context length is (\d[\d,]*) tokens`)

// genericKeywords is the fixed, ordered keyword set rung 4 searches for,
// in the exact order 04 §2 lists them.
var genericKeywords = []string{"context length", "context window", "maximum context", "token limit", "context"}

// genericKeywordDistance is the maximum character distance (04 §2:
// "number adjacent... within a documented character distance") a number
// may sit from a rung-4 keyword and still count as adjacent.
const genericKeywordDistance = 40

var numberRe = regexp.MustCompile(`\d[\d,]*`)

// parsePositiveNumber strips comma grouping and parses s as a positive
// int; a non-positive or unparsable value is never returned as a limit
// (04 §2: "never a guess").
func parsePositiveNumber(s string) (int, bool) {
	n, err := strconv.Atoi(strings.ReplaceAll(s, ",", ""))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// extractOpenAIPhrase applies openAIPhraseRe to msg. Callers must gate
// this on res.ProviderCode == "context_length_exceeded" themselves (rung
// 2's hard requirement).
func extractOpenAIPhrase(msg string) (int, bool) {
	m := openAIPhraseRe.FindStringSubmatch(msg)
	if m == nil {
		return 0, false
	}
	return parsePositiveNumber(m[1])
}

// extractProviderRegex applies the first rule matching providerID to msg
// (rung 3).
func extractProviderRegex(msg, providerID string, rules []ContextLimitRule) (int, bool) {
	for _, rule := range rules {
		if rule.ProviderID != providerID || rule.Pattern == nil {
			continue
		}
		m := rule.Pattern.FindStringSubmatch(msg)
		if len(m) < 2 {
			continue
		}
		if n, ok := parsePositiveNumber(m[1]); ok {
			return n, true
		}
	}
	return 0, false
}

// extractGenericKeywordNumber searches msg for the first genericKeywords
// entry (in order) with a number within genericKeywordDistance characters
// (rung 4).
func extractGenericKeywordNumber(msg string) (int, bool) {
	lower := strings.ToLower(msg)
	for _, kw := range genericKeywords {
		idx := strings.Index(lower, kw)
		if idx < 0 {
			continue
		}
		start := idx - genericKeywordDistance
		if start < 0 {
			start = 0
		}
		end := idx + len(kw) + genericKeywordDistance
		if end > len(msg) {
			end = len(msg)
		}
		window := msg[start:end]
		if m := numberRe.FindString(window); m != "" {
			if n, ok := parsePositiveNumber(m); ok {
				return n, true
			}
		}
	}
	return 0, false
}

// ExtractContextLimit implements 04 §2's rejection-reading ladder, first
// hit wins, in this exact order: a structured field, then the OpenAI
// phrase (gated on the context_length_exceeded provider code), then a
// provider-specific regex, then a generic number-near-keyword search,
// otherwise no_signal. A rung whose extracted candidate is non-positive
// does not win — the ladder keeps descending to the next rung — but the
// final result is never a non-positive value: no_signal always reports
// ok=false and limit=0.
func ExtractContextLimit(res ProbeResult, rules []ContextLimitRule, providerID string) (int, ContextLimitRung, bool) {
	if res.StructuredContextLimit != nil {
		if n := *res.StructuredContextLimit; n > 0 {
			return n, RungStructuredField, true
		}
	}
	if res.ProviderCode == "context_length_exceeded" {
		if n, ok := extractOpenAIPhrase(res.Message); ok {
			return n, RungOpenAIPhrase, true
		}
	}
	if n, ok := extractProviderRegex(res.Message, providerID, rules); ok {
		return n, RungProviderRegex, true
	}
	if n, ok := extractGenericKeywordNumber(res.Message); ok {
		return n, RungGenericKeyword, true
	}
	return 0, RungNoSignal, false
}

// credentialShapePatterns are standalone credential-shaped tokens
// sanitize.Text does not already redact (it only redacts a Bearer/Basic
// scheme token or a recognized key=value pair) — a bare API-key-shaped
// string embedded in free text needs its own patterns.
var credentialShapePatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`),
	regexp.MustCompile(`ya29\.[A-Za-z0-9_-]{8,}`),
	regexp.MustCompile(`vk_live_[A-Za-z0-9_-]{8,}`),
	regexp.MustCompile(`ghp_[A-Za-z0-9_-]{8,}`),
}

// redactProbeSnippet builds the short, redacted evidence snippet shared
// by ContextProbe and CapabilityProbe: sanitize.Text first (Authorization
// headers, key=value secrets), then the fixed standalone credential-shape
// patterns above, then truncation to ProbeSnippetMaxRunes runes (by rune
// count, not bytes, so multi-byte text truncates correctly).
func redactProbeSnippet(raw string) string {
	s := sanitize.Text(raw)
	for _, re := range credentialShapePatterns {
		s = re.ReplaceAllString(s, sanitize.Placeholder)
	}
	runes := []rune(s)
	if len(runes) > ProbeSnippetMaxRunes {
		runes = runes[:ProbeSnippetMaxRunes]
	}
	return string(runes)
}

// ErrNilContextProbeDependency is returned by NewContextProbe when
// transport or guard is nil.
var ErrNilContextProbeDependency = errors.New("intelligence: context probe requires a transport and a guard")

// ContextProbe runs the context-window probe (04 §2): exactly one
// deliberately oversized request per attempt, admitted through a
// ProbeGuard first, with the real limit read out of the rejection via
// ExtractContextLimit — never guessed.
type ContextProbe struct {
	transport ProbeTransport
	guard     *ProbeGuard
	rules     []ContextLimitRule
	now       func() time.Time
}

// NewContextProbe builds a ContextProbe. transport and guard are
// required; rules may be nil/empty (rung 3 is simply skipped). now
// defaults to time.Now when nil.
func NewContextProbe(transport ProbeTransport, guard *ProbeGuard, rules []ContextLimitRule, now func() time.Time) (*ContextProbe, error) {
	if transport == nil || guard == nil {
		return nil, ErrNilContextProbeDependency
	}
	if now == nil {
		now = time.Now
	}
	return &ContextProbe{transport: transport, guard: guard, rules: rules, now: now}, nil
}

// ContextProbeReport is Run's result: the probe-execution/capability-truth
// outcome, the extracted limit (nil unless the outcome is definitively
// supported), which ladder rung produced it, the Evidence to merge into
// Resolve (empty unless the outcome is definitive), the redacted message
// snippet, and the reservation obtained for this attempt.
type ContextProbeReport struct {
	Outcome       ProbeOutcome
	Limit         *int
	Rung          ContextLimitRung
	Evidence      []Evidence
	Snippet       string
	ReservationID string
}

// classifyContextProbeResult maps res onto a ProbeSignalKind plus (when
// applicable) the ladder's extraction result, implementing 04 §2's
// mapping for the context probe specifically:
//   - a transport failure (timeout/network) maps directly;
//   - 429/401/403 map directly (infrastructure, never a capability
//     judgment);
//   - a 2xx means the provider ACCEPTED the oversized request — we
//     learned nothing definite about the real limit and must not invent
//     one, so this is malformed_request (inconclusive), never treated as
//     "no limit";
//   - any other 4xx runs the ladder: a hit is the successful observation
//     this probe was designed to produce (capability_response); a miss is
//     malformed_request (inconclusive).
func classifyContextProbeResult(res ProbeResult, rules []ContextLimitRule, providerID string) (ProbeSignalKind, int, ContextLimitRung, bool) {
	switch res.Transport {
	case TransportTimeout:
		return SignalTimeout, 0, RungNoSignal, false
	case TransportNetwork:
		return SignalNetworkError, 0, RungNoSignal, false
	}

	switch {
	case res.HTTPStatus == 429:
		return SignalRateLimited, 0, RungNoSignal, false
	case res.HTTPStatus >= 500:
		return SignalServerError, 0, RungNoSignal, false
	case res.HTTPStatus == 401:
		return SignalUnauthorized, 0, RungNoSignal, false
	case res.HTTPStatus == 403:
		return SignalForbidden, 0, RungNoSignal, false
	case res.HTTPStatus >= 200 && res.HTTPStatus < 300:
		return SignalMalformedRequest, 0, RungNoSignal, false
	case res.HTTPStatus >= 400:
		limit, rung, ok := ExtractContextLimit(res, rules, providerID)
		if ok {
			return SignalCapabilityResponse, limit, rung, true
		}
		return SignalMalformedRequest, 0, rung, false
	default:
		return SignalMalformedRequest, 0, RungNoSignal, false
	}
}

// Run executes one context-probe attempt (04 §2): build the oversized
// request, admit it through the guard (Class=ProbeExpensive — a 3M-token
// declared input is expensive by construction, never assumed free because
// the provider will likely reject it), call the transport exactly once,
// classify the result, and emit Evidence only when the outcome is
// definitive.
func (p *ContextProbe) Run(ctx context.Context, req ProbeRequest) (ContextProbeReport, error) {
	req.Operation = models.OperationContextWindow
	req.DeclaredInputTokens = ContextProbeInputTokens
	req.MaxOutputTokens = ContextProbeMaxOutputTokens
	// FIX 2 (whole-branch review, Critical): the request must actually BE
	// oversized, not merely CLAIM to be via DeclaredInputTokens above — see
	// oversizedContextProbeContent's own doc comment for why the two are
	// not the same fact. Always overwritten here, exactly like the three
	// fields above are unconditionally overwritten regardless of whatever
	// the caller passed in req.Messages.
	req.Messages = []ProbeMessage{{Role: "user", Content: oversizedContextProbeContent()}}

	now := p.now()
	inputTokens := ContextProbeInputTokens
	maxOutput := ContextProbeMaxOutputTokens
	// RequestID/AttemptID are not part of ProbeRequest (see
	// probetransport.go's doc comment) — derive a local admission
	// identity from the offering-operation and the injected clock.
	attemptID := fmt.Sprintf("ctxprobe:%s:%d", req.OfferingOperationID, now.UnixNano())

	admission, err := p.guard.Admit(ctx, ProbeAdmissionRequest{
		AccountID:           req.AccountID,
		ProviderID:          req.ProviderID,
		OfferingOperationID: req.OfferingOperationID,
		RequestID:           attemptID,
		AttemptID:           attemptID,
		Operation:           models.OperationContextWindow,
		Class:               ProbeExpensive,
		Cost:                quota.EstimateInput{InputTokens: &inputTokens, MaxOutputTokens: &maxOutput},
	})
	if err != nil {
		return ContextProbeReport{}, err
	}

	res, err := p.transport.Probe(ctx, req)
	if err != nil {
		return ContextProbeReport{}, fmt.Errorf("intelligence: context probe transport call failed: %w", err)
	}

	kind, limit, rung, extracted := classifyContextProbeResult(res, p.rules, req.ProviderID)
	outcome, err := ClassifyProbeSignal(kind)
	if err != nil {
		return ContextProbeReport{}, err
	}

	report := ContextProbeReport{
		Outcome:       outcome,
		Rung:          rung,
		Snippet:       redactProbeSnippet(res.Message),
		ReservationID: admission.ReservationID,
	}

	if extracted {
		l := limit
		report.Limit = &l
		report.Evidence = []Evidence{{
			Field:        FieldNativeContextTokens,
			Scope:        Scope{AccountID: req.AccountID, ProviderModelID: req.ProviderModelID},
			Source:       SourceVerifiedProbe,
			Verification: VerificationVerified,
			Confidence:   1.0,
			ObservedAt:   now,
			Value:        limit,
		}}
	}

	return report, nil
}

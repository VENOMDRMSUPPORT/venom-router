package quota

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Source identifies a quota window's budget origin (02 §3): where its
// authority comes from. The three sources are never conflated — a
// local_safety limit is never presented as provider evidence and vice
// versa, and neither is ever conflated with the unrelated
// account-funding-evidence source vocabulary (provider_policy /
// owner_policy), which is a distinct axis entirely (02 §2).
type Source string

const (
	SourceProviderEvidence Source = "provider_evidence"
	SourceLocalSafety      Source = "local_safety"
	SourceOwnerOverride    Source = "owner_override"
)

// ErrUnknownSource is returned by ParseSource for any token outside the
// three canonical Source values.
var ErrUnknownSource = errors.New("quota: unknown source")

// ParseSource fails closed: any token outside the canonical three
// (including a wrong-case variant or an empty string) is rejected rather
// than silently accepted or coerced.
func ParseSource(s string) (Source, error) {
	switch Source(s) {
	case SourceProviderEvidence, SourceLocalSafety, SourceOwnerOverride:
		return Source(s), nil
	default:
		return "", ErrUnknownSource
	}
}

// Unit is one of the eight canonical quota dimensions (02 §3 DDL
// comment). Unlike WindowType (a provider-driven open vocabulary), Unit
// is closed.
type Unit string

const (
	UnitRequests     Unit = "requests"
	UnitInputTokens  Unit = "input_tokens"
	UnitOutputTokens Unit = "output_tokens"
	UnitTokens       Unit = "tokens"
	UnitCredits      Unit = "credits"
	UnitBalance      Unit = "balance"
	UnitPercent      Unit = "percent"
	UnitConcurrency  Unit = "concurrency"
)

// ErrUnknownUnit is returned by ParseUnit, and by NormalizeWindowKey when
// given a Unit outside the canonical eight.
var ErrUnknownUnit = errors.New("quota: unknown unit")

func isValidUnit(u Unit) bool {
	switch u {
	case UnitRequests, UnitInputTokens, UnitOutputTokens, UnitTokens, UnitCredits, UnitBalance, UnitPercent, UnitConcurrency:
		return true
	default:
		return false
	}
}

// ParseUnit fails closed: an unrecognized token is rejected, never
// passed through as a stringly-typed Unit.
func ParseUnit(s string) (Unit, error) {
	u := Unit(s)
	if !isValidUnit(u) {
		return "", ErrUnknownUnit
	}
	return u, nil
}

// Freshness is the persisted freshness_state column vocabulary (02 §3).
type Freshness string

const (
	FreshnessFresh   Freshness = "fresh"
	FreshnessStale   Freshness = "stale"
	FreshnessUnknown Freshness = "unknown"
)

// ErrUnknownFreshness is returned by ParseFreshness for any token outside
// the three canonical Freshness values.
var ErrUnknownFreshness = errors.New("quota: unknown freshness")

func ParseFreshness(s string) (Freshness, error) {
	switch Freshness(s) {
	case FreshnessFresh, FreshnessStale, FreshnessUnknown:
		return Freshness(s), nil
	default:
		return "", ErrUnknownFreshness
	}
}

// WindowState is a window's DERIVED per-attempt eligibility verdict
// (05 §4). Unlike Freshness (the persisted freshness_state column),
// WindowState is never persisted — it is recomputed from a window's
// current fields plus the caller's now/need at evaluation time.
type WindowState string

const (
	StateAvailable    WindowState = "available"
	StateInsufficient WindowState = "insufficient"
	StateExhausted    WindowState = "exhausted"
	StateUnknown      WindowState = "unknown"
	StateStale        WindowState = "stale"
)

// rankStaleOrUnknown is the shared Restrictiveness rank of StateStale and
// StateUnknown (05 §4: "stale ... treated as unknown"). MostRestrictive
// uses this constant to resolve a tie between the two deterministically.
const rankStaleOrUnknown = 1

// Restrictiveness ranks a WindowState from least to most restrictive:
// available(0) < stale=unknown(1) < insufficient(2) < exhausted(3). An
// input outside this closed set (which cannot arise from this package's
// own constructors) ranks as maximally restrictive — fail closed rather
// than silently treating unrecognized state as available.
func Restrictiveness(s WindowState) int {
	switch s {
	case StateAvailable:
		return 0
	case StateStale, StateUnknown:
		return rankStaleOrUnknown
	case StateInsufficient:
		return 2
	case StateExhausted:
		return 3
	default:
		return 3
	}
}

// MostRestrictive selects the most restrictive WindowState across all of
// an attempt's applicable windows (05 §4: "the attempt takes the most
// restrictive"). An empty or nil set is never treated as available — no
// windows to check is itself an unknown situation, so it fails closed to
// StateUnknown. A [stale, unknown] tie (in either order) resolves to
// StateUnknown deterministically, since 05 §4 documents stale as treated
// as unknown.
func MostRestrictive(states []WindowState) WindowState {
	if len(states) == 0 {
		return StateUnknown
	}

	worst := states[0]
	worstRank := Restrictiveness(worst)
	for _, s := range states[1:] {
		rank := Restrictiveness(s)
		switch {
		case rank > worstRank:
			worst, worstRank = s, rank
		case rank == worstRank && worstRank == rankStaleOrUnknown:
			worst = StateUnknown
		}
	}
	return worst
}

// NeedsRefresh reports whether s should trigger a background quota
// refresh (05 §4: stale and unknown both do).
func NeedsRefresh(s WindowState) bool {
	return s == StateStale || s == StateUnknown
}

// WindowKeyInput is the input to NormalizeWindowKey.
type WindowKeyInput struct {
	// ProviderKey is the provider-supplied window identifier; "" when the
	// adapter supplied none (mirrors providers.QuotaWindow.WindowKey).
	ProviderKey string
	// DurationSeconds is the window length; nil (or non-positive) means
	// non-time-boxed / absent.
	DurationSeconds *int
	Unit            Unit
}

// NormalizeWindowKey implements the canonical window_key normalization
// rule (02 §3): deterministic, never "", form "<namespace>:<token>".
// Precedence: a non-blank, non-degenerate ProviderKey wins
// ("provider:"+normalizeToken(key)); otherwise a positive DurationSeconds
// produces "rolling:<n>s"; otherwise "local:"+unit. A ProviderKey that
// normalizes to the empty token (e.g. all punctuation) falls through to
// the next rule rather than producing a bare "provider:".
func NormalizeWindowKey(in WindowKeyInput) (string, error) {
	if !isValidUnit(in.Unit) {
		return "", ErrUnknownUnit
	}

	if token := normalizeToken(in.ProviderKey); token != "" {
		return "provider:" + token, nil
	}
	if in.DurationSeconds != nil && *in.DurationSeconds > 0 {
		return fmt.Sprintf("rolling:%ds", *in.DurationSeconds), nil
	}
	return "local:" + string(in.Unit), nil
}

// normalizeToken lowercases s, trims surrounding whitespace, collapses
// every run of non-alphanumeric characters to a single "_", and trims any
// leading/trailing "_". A string with no alphanumeric characters at all
// normalizes to "".
func normalizeToken(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))

	var b strings.Builder
	pendingUnderscore := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			if pendingUnderscore && b.Len() > 0 {
				b.WriteByte('_')
			}
			pendingUnderscore = false
			b.WriteRune(r)
		default:
			pendingUnderscore = true
		}
	}
	return b.String()
}

// Window is one concurrently-tracked quota budget dimension (02 §3).
// Nullable numeric fields mean unknown — never 0-as-unknown.
type Window struct {
	ID, AccountID   string
	Source          Source
	Unit            Unit
	WindowType      string
	Key             string
	DurationSeconds *int
	Used            *float64
	Remaining       *float64
	Total           *float64
	Reserved        float64
	LimitValue      *float64
	ResetAt         *int64
	Version         int64
	Confidence      float64
	Freshness       Freshness
	ObservedAt      time.Time
}

// DefaultStalenessWindow is the "~15 min" staleness threshold (05 §4).
const DefaultStalenessWindow = 15 * time.Minute

// Capacity returns COALESCE(remaining, limit_value): provider-evidence
// windows carry Remaining; local_safety/owner_override windows carry
// only LimitValue as their authoritative ceiling. ok is false when
// neither is known — capacity is genuinely unknown, never 0.
func (w Window) Capacity() (float64, bool) {
	if w.Remaining != nil {
		return *w.Remaining, true
	}
	if w.LimitValue != nil {
		return *w.LimitValue, true
	}
	return 0, false
}

// Headroom is capacity minus the currently reserved amount, floored at 0.
// ok is false when capacity is unknown.
func (w Window) Headroom() (float64, bool) {
	capacity, ok := w.Capacity()
	if !ok {
		return 0, false
	}
	headroom := capacity - w.Reserved
	if headroom < 0 {
		headroom = 0
	}
	return headroom, true
}

// State evaluates this window's per-attempt eligibility for a candidate
// needing `need` units, as of `now`, treating data older than staleAfter
// as stale (05 §4). Staleness is evaluated BEFORE the numeric headroom
// branches: stale data must never produce a confident
// available/exhausted verdict, even when its recorded headroom happens
// to be 0.
func (w Window) State(need float64, now time.Time, staleAfter time.Duration) WindowState {
	if w.Freshness == FreshnessUnknown {
		return StateUnknown
	}
	if w.Freshness == FreshnessStale || now.Sub(w.ObservedAt) > staleAfter {
		return StateStale
	}

	headroom, ok := w.Headroom()
	if !ok {
		return StateUnknown
	}
	switch {
	case headroom <= 0:
		return StateExhausted
	case headroom < need:
		return StateInsufficient
	default:
		return StateAvailable
	}
}

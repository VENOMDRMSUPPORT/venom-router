package routing

import (
	"errors"
	"fmt"
)

// Tier is the closed product-tier vocabulary (05 §1): lite | pro | max.
// Policies and every routing decision are keyed by Tier only — never by a
// model name or provider slug.
type Tier string

const (
	TierLite Tier = "lite"
	TierPro  Tier = "pro"
	TierMax  Tier = "max"
)

// ErrUnknownTier is returned by ParseTier for any value outside the
// three-tier vocabulary.
var ErrUnknownTier = errors.New("routing: unrecognized tier value")

// ParseTier fails closed on any value outside the exact vocabulary — no
// case folding, no trimming, no aliases.
func ParseTier(s string) (Tier, error) {
	switch Tier(s) {
	case TierLite, TierPro, TierMax:
		return Tier(s), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownTier, s)
	}
}

// ThinkingLevel is the provider-neutral thinking/reasoning effort level
// (05 §1a), a closed ordered vocabulary:
// none < standard < extended < ultra. It never names a provider mechanism
// or a model.
type ThinkingLevel string

const (
	ThinkingNone     ThinkingLevel = "none"
	ThinkingStandard ThinkingLevel = "standard"
	ThinkingExtended ThinkingLevel = "extended"
	ThinkingUltra    ThinkingLevel = "ultra"
)

// ErrUnknownThinkingLevel is returned by ParseThinkingLevel for any value
// outside the four-level vocabulary.
var ErrUnknownThinkingLevel = errors.New("routing: unrecognized thinking level")

// thinkingOrder is the single source of the level ordering; rank and
// ParseThinkingLevel both derive from it so they can never drift apart.
var thinkingOrder = []ThinkingLevel{ThinkingNone, ThinkingStandard, ThinkingExtended, ThinkingUltra}

// ParseThinkingLevel fails closed on any value outside the exact
// vocabulary — no case folding, no trimming.
func ParseThinkingLevel(s string) (ThinkingLevel, error) {
	for _, level := range thinkingOrder {
		if ThinkingLevel(s) == level {
			return level, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownThinkingLevel, s)
}

// rank returns the level's position in the none < standard < extended <
// ultra order; ok is false for a value outside the vocabulary (an unknown
// level has no rank — fail closed).
func (l ThinkingLevel) rank() (int, bool) {
	for i, level := range thinkingOrder {
		if l == level {
			return i, true
		}
	}
	return 0, false
}

// atMost reports whether l is at or below other in the level ordering.
// Either side being outside the vocabulary reports false (fail closed).
func (l ThinkingLevel) atMost(other ThinkingLevel) bool {
	li, lok := l.rank()
	oi, ook := other.rank()
	return lok && ook && li <= oi
}

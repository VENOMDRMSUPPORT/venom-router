package routing

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// DefaultLargeContextThreshold is the default large_context trigger
// (05 §2 Step 7): a context need S strictly greater than 32K tokens.
// The threshold is owner-tunable; the property semantics are not.
const DefaultLargeContextThreshold int64 = 32768

// ErrInvalidThreshold is returned by NewBucketKeyer for a non-positive
// large-context threshold (fail closed — a zero threshold would mark
// every request large_context and silently collapse the buckets).
var ErrInvalidThreshold = errors.New("routing: large-context threshold must be positive")

// The workload-profile property vocabulary (05 §2 Step 7). standard is
// the fallback property: it applies exactly when no other property
// matches, never alongside one.
const (
	propertyVision       = "vision"
	propertyToolUse      = "tool_use"
	propertyStructured   = "structured"
	propertyLargeContext = "large_context"
	propertyStandard     = "standard"
)

// BucketKeyer derives the deterministic workload_profile_bucket key from
// a request's Requirements. It is a scoring and monitoring signal, NOT a
// hard gate: it never consults certification, funding, or eligibility —
// required capabilities remain the Step-3 gates.
type BucketKeyer struct {
	largeContextThreshold int64
}

// NewBucketKeyer validates the large-context threshold at construction;
// zero and negative thresholds are rejected.
func NewBucketKeyer(largeContextThreshold int64) (BucketKeyer, error) {
	if largeContextThreshold <= 0 {
		return BucketKeyer{}, fmt.Errorf("%w: %d", ErrInvalidThreshold, largeContextThreshold)
	}
	return BucketKeyer{largeContextThreshold: largeContextThreshold}, nil
}

// BucketKey is THE canonical bucket-key serialization (05 §2 Step 7):
// matched properties, normalized to lowercase, sorted lexicographically,
// deduplicated, and +-joined into one string. The same property set
// always maps to the same key. This serialization is fixed here once —
// no other call site may re-serialize a property set into a key.
func (k BucketKeyer) BucketKey(reqs Requirements) string {
	var properties []string
	if reqs.VisionModality {
		properties = append(properties, propertyVision)
	}
	if hasCapability(reqs.Capabilities, CapabilityTools) {
		properties = append(properties, propertyToolUse)
	}
	if hasCapability(reqs.Capabilities, CapabilityStructuredOutput) {
		properties = append(properties, propertyStructured)
	}
	if reqs.ContextTokens > k.largeContextThreshold {
		properties = append(properties, propertyLargeContext)
	}
	if len(properties) == 0 {
		properties = append(properties, propertyStandard)
	}

	for i := range properties {
		properties[i] = strings.ToLower(properties[i])
	}
	sort.Strings(properties)
	deduped := properties[:1]
	for _, property := range properties[1:] {
		if property != deduped[len(deduped)-1] {
			deduped = append(deduped, property)
		}
	}
	return strings.Join(deduped, "+")
}

// hasCapability reports whether the derived capability set contains c.
func hasCapability(capabilities []Capability, c Capability) bool {
	for _, existing := range capabilities {
		if existing == c {
			return true
		}
	}
	return false
}

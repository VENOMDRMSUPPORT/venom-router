package httpapi

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/routing"
)

// Stable routing error codes (05 §5). Exposed on the public inference endpoint
// in P5; here they are the wire vocabulary the composition layer renders.
const (
	CodeFreeCapacityExhausted = "venom_free_capacity_exhausted"
	CodeNoEligibleOffering    = "venom_no_eligible_offering"
	CodeContextExceedsTier    = "venom_context_exceeds_tier"
	CodeCapabilityUnsupported = "venom_capability_unsupported"
	CodeInvalidExtension      = "venom_invalid_extension"
)

// Fixed, secret-free wire messages. They never interpolate an error's fields or
// a wrapped provider message, so no raw provider text or credential material can
// reach the envelope (05 §5: "no secrets, no raw provider errors").
const (
	msgFreeCapacityExhausted = "free capacity is temporarily exhausted for this tier; retry later"
	msgNoEligibleOffering    = "no eligible offering for this tier and request"
	msgContextExceedsTier    = "request context exceeds this tier's ceiling"
	msgCapabilityUnsupported = "a required capability is not available for this tier"
	msgInvalidExtension      = "the venom request extension is invalid"
)

// RoutingErrorEnvelope is the rendered shape of a routing error: the stable
// code, its HTTP status, a fixed safe message, whether a retry may succeed, and
// an optional retry_after (seconds) when the code carries one.
type RoutingErrorEnvelope struct {
	Code       string
	HTTPStatus int
	Message    string
	Retryable  bool
	RetryAfter *int
}

// RoutingErrorFor maps a routing error to its stable envelope, or ok=false when
// err is not a recognized routing error. The exhaustion split is drawn here: a
// *NoEligibleOfferingError that carries a retry_after is the temporary
// free-capacity-exhausted (429) flavor; without one it is the structural
// no-eligible-offering (503). Both fail closed — neither ever implies escalating
// a Lite request to a paid or unknown route (that decision lives in the loop's
// funding gate, upstream of this mapping).
func RoutingErrorFor(err error) (RoutingErrorEnvelope, bool) {
	var noOffer *routing.NoEligibleOfferingError
	if errors.As(err, &noOffer) {
		if noOffer.RetryAfter > 0 {
			secs := retryAfterSeconds(noOffer.RetryAfter)
			return RoutingErrorEnvelope{
				Code:       CodeFreeCapacityExhausted,
				HTTPStatus: http.StatusTooManyRequests, // 429
				Message:    msgFreeCapacityExhausted,
				Retryable:  true,
				RetryAfter: &secs,
			}, true
		}
		return RoutingErrorEnvelope{
			Code:       CodeNoEligibleOffering,
			HTTPStatus: http.StatusServiceUnavailable, // 503
			Message:    msgNoEligibleOffering,
			Retryable:  true,
		}, true
	}

	var capErr *routing.CapabilityUnsupportedError
	if errors.As(err, &capErr) {
		status := http.StatusNotImplemented // 501: valid capability, fleet gap
		if capErr.TierStructural {
			status = http.StatusBadRequest // 400: the client asked for something this tier never serves
		}
		return RoutingErrorEnvelope{
			Code:       CodeCapabilityUnsupported,
			HTTPStatus: status,
			Message:    msgCapabilityUnsupported,
			Retryable:  false,
		}, true
	}

	if errors.Is(err, routing.ErrContextExceedsTier) {
		return RoutingErrorEnvelope{Code: CodeContextExceedsTier, HTTPStatus: http.StatusBadRequest, Message: msgContextExceedsTier, Retryable: false}, true
	}
	if errors.Is(err, routing.ErrInvalidExtension) {
		return RoutingErrorEnvelope{Code: CodeInvalidExtension, HTTPStatus: http.StatusBadRequest, Message: msgInvalidExtension, Retryable: false}, true
	}

	return RoutingErrorEnvelope{}, false
}

// retryAfterSeconds converts a retry-after duration to whole seconds, rounded
// UP and floored at 1 (a sub-second hint still tells the client to wait).
func retryAfterSeconds(d time.Duration) int {
	secs := int(math.Ceil(d.Seconds()))
	if secs < 1 {
		secs = 1
	}
	return secs
}

// writeRoutingError renders a recognized routing error via the shared error
// envelope (09 §1 / P2b-CAPI-002), surfacing retry_after both as a Retry-After
// header and in the error details. It returns false (writing nothing) when err
// is not a routing error, so callers can fall through to their default handler.
func writeRoutingError(w http.ResponseWriter, err error) bool {
	env, ok := RoutingErrorFor(err)
	if !ok {
		return false
	}
	var details any
	if env.RetryAfter != nil {
		w.Header().Set("Retry-After", strconv.Itoa(*env.RetryAfter))
		details = map[string]any{"retry_after": *env.RetryAfter}
	}
	writeErrorDetails(w, env.HTTPStatus, env.Code, env.Message, env.Retryable, details)
	return true
}

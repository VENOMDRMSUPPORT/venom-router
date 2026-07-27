package intelligence

import (
	"errors"
	"fmt"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

// ProbeExecution is what happened when Venom tried to test a capability (04
// §2's "probe execution" layer) — a dimension kept strictly separate from
// capability truth (models.CapabilityTruth): execution tracks the attempt,
// truth tracks the verdict. Exactly six values, mirroring
// models.CertificationState's closed-vocabulary pattern.
type ProbeExecution string

const (
	ProbePending          ProbeExecution = "pending"
	ProbeRunning          ProbeExecution = "running"
	ProbeSucceeded        ProbeExecution = "succeeded"
	ProbeInconclusive     ProbeExecution = "inconclusive"
	ProbeRetryableFailure ProbeExecution = "retryable_failure"
	ProbeTerminalFailure  ProbeExecution = "terminal_failure"
)

// probeExecutionSet is the fixed six-value vocabulary, in the order 04 §2
// lists it.
var probeExecutionSet = []ProbeExecution{
	ProbePending,
	ProbeRunning,
	ProbeSucceeded,
	ProbeInconclusive,
	ProbeRetryableFailure,
	ProbeTerminalFailure,
}

// ErrUnknownProbeExecution is returned by ParseProbeExecution for any value
// outside the exact six-value vocabulary.
var ErrUnknownProbeExecution = errors.New("intelligence: unrecognized probe execution")

// ParseProbeExecution fails closed on any value outside the exact
// six-value vocabulary.
func ParseProbeExecution(s string) (ProbeExecution, error) {
	for _, v := range probeExecutionSet {
		if ProbeExecution(s) == v {
			return v, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownProbeExecution, s)
}

// ProbeExecutions returns the fixed six-value probe-execution vocabulary,
// in the order 04 §2 lists it. Each call returns a fresh defensive copy —
// mutating the returned slice never affects a later call.
func ProbeExecutions() []ProbeExecution {
	out := make([]ProbeExecution, len(probeExecutionSet))
	copy(out, probeExecutionSet)
	return out
}

// ProbeSignalKind is the transport-independent, already-classified
// semantic signal one probe attempt produced — the boundary between "what
// happened on the wire" and "what it means". Mapping a raw transport
// result (an HTTP status, a timeout, a network error) onto a
// ProbeSignalKind is each probe's own job (P3c-CERT-002's context probe,
// P3c-CERT-003's capability probes): it is precisely why an HTTP 400 can
// be a semantic_rejection FAILURE for a capability probe and the intended
// capability_response SUCCESS for a context probe (the rejection itself
// carries the context-limit number) without either interpretation leaking
// into the other. ClassifyProbeSignal below is the single place that turns
// a ProbeSignalKind into a ProbeOutcome; no other code may re-derive that
// mapping.
type ProbeSignalKind string

const (
	SignalCapabilityResponse ProbeSignalKind = "capability_response"
	SignalSemanticRejection  ProbeSignalKind = "semantic_rejection"
	SignalRateLimited        ProbeSignalKind = "rate_limited"
	SignalTimeout            ProbeSignalKind = "timeout"
	SignalServerError        ProbeSignalKind = "server_error"
	SignalNetworkError       ProbeSignalKind = "network_error"
	SignalUnauthorized       ProbeSignalKind = "unauthorized"
	SignalForbidden          ProbeSignalKind = "forbidden"
	SignalMalformedRequest   ProbeSignalKind = "malformed_request"
)

// probeSignalKindSet is the fixed nine-value vocabulary, in the order 04
// §2's taxonomy table lists it.
var probeSignalKindSet = []ProbeSignalKind{
	SignalCapabilityResponse,
	SignalSemanticRejection,
	SignalRateLimited,
	SignalTimeout,
	SignalServerError,
	SignalNetworkError,
	SignalUnauthorized,
	SignalForbidden,
	SignalMalformedRequest,
}

// ErrUnknownProbeSignalKind is returned by ParseProbeSignalKind, and by
// ClassifyProbeSignal, for any value outside the exact nine-value
// vocabulary.
var ErrUnknownProbeSignalKind = errors.New("intelligence: unrecognized probe signal kind")

// ParseProbeSignalKind fails closed on any value outside the exact
// nine-value vocabulary.
func ParseProbeSignalKind(s string) (ProbeSignalKind, error) {
	for _, v := range probeSignalKindSet {
		if ProbeSignalKind(s) == v {
			return v, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownProbeSignalKind, s)
}

// ProbeReasonCode is a typed, user-safe reason accompanying a ProbeOutcome
// — never a raw provider error or upstream response body.
type ProbeReasonCode string

const (
	ReasonCapabilityConfirmed ProbeReasonCode = "capability_confirmed"
	ReasonCapabilityAbsent    ProbeReasonCode = "capability_absent"
	ReasonRateLimited         ProbeReasonCode = "rate_limited"
	ReasonTimeout             ProbeReasonCode = "timeout"
	ReasonServerError         ProbeReasonCode = "server_error"
	ReasonNetworkError        ProbeReasonCode = "network_error"
	ReasonCredentialBlocked   ProbeReasonCode = "credential_blocked"
	ReasonMalformedProbe      ProbeReasonCode = "malformed_probe"
)

// ProbeOutcome is ClassifyProbeSignal's result: the probe-execution state
// this signal establishes, the capability truth it establishes (ONLY when
// Definitive is true — a caller must never write a truth when Definitive
// is false, since a non-definitive Truth is always the zero-information
// models.TruthUnknown), whether the probe should be rescheduled, and a
// typed reason.
type ProbeOutcome struct {
	Execution  ProbeExecution
	Truth      models.CapabilityTruth
	Definitive bool
	Reschedule bool
	Reason     ProbeReasonCode
}

// probeOutcomeTable is the literal 04 §2/§5 taxonomy: every one of the nine
// signal kinds maps to exactly one outcome. Built as an explicit map
// keyed by the closed ProbeSignalKind vocabulary (not derived or computed)
// so the table itself is the single source of truth ClassifyProbeSignal
// reads from — there is no other path that could disagree with it.
var probeOutcomeTable = map[ProbeSignalKind]ProbeOutcome{
	SignalCapabilityResponse: {
		Execution:  ProbeSucceeded,
		Truth:      models.TruthSupported,
		Definitive: true,
		Reschedule: false,
		Reason:     ReasonCapabilityConfirmed,
	},
	SignalSemanticRejection: {
		Execution:  ProbeSucceeded,
		Truth:      models.TruthUnsupported,
		Definitive: true,
		Reschedule: false,
		Reason:     ReasonCapabilityAbsent,
	},
	SignalRateLimited: {
		Execution:  ProbeRetryableFailure,
		Truth:      models.TruthUnknown,
		Definitive: false,
		Reschedule: true,
		Reason:     ReasonRateLimited,
	},
	SignalTimeout: {
		Execution:  ProbeRetryableFailure,
		Truth:      models.TruthUnknown,
		Definitive: false,
		Reschedule: true,
		Reason:     ReasonTimeout,
	},
	SignalServerError: {
		Execution:  ProbeRetryableFailure,
		Truth:      models.TruthUnknown,
		Definitive: false,
		Reschedule: true,
		Reason:     ReasonServerError,
	},
	SignalNetworkError: {
		Execution:  ProbeRetryableFailure,
		Truth:      models.TruthUnknown,
		Definitive: false,
		Reschedule: true,
		Reason:     ReasonNetworkError,
	},
	SignalUnauthorized: {
		Execution:  ProbeTerminalFailure,
		Truth:      models.TruthUnknown,
		Definitive: false,
		Reschedule: false,
		Reason:     ReasonCredentialBlocked,
	},
	SignalForbidden: {
		Execution:  ProbeTerminalFailure,
		Truth:      models.TruthUnknown,
		Definitive: false,
		Reschedule: false,
		Reason:     ReasonCredentialBlocked,
	},
	SignalMalformedRequest: {
		Execution:  ProbeInconclusive,
		Truth:      models.TruthUnknown,
		Definitive: false,
		Reschedule: false,
		Reason:     ReasonMalformedProbe,
	},
}

// ClassifyProbeSignal implements 04 §2/§5's probe outcome taxonomy exactly:
// it maps kind onto the ProbeOutcome the spec's table assigns it. An
// unrecognized kind returns the zero ProbeOutcome and a wrapped
// ErrUnknownProbeSignalKind — this function never guesses an outcome for a
// signal it does not recognize.
//
// Truth is what THIS outcome establishes about the capability, and is only
// ever non-unknown when Definitive is true: a quota or rate-limit failure
// (or any other infrastructure signal) during a probe must never flip a
// capability to unsupported — only a genuine capability_response or
// semantic_rejection may (04 §2's hard rule).
func ClassifyProbeSignal(kind ProbeSignalKind) (ProbeOutcome, error) {
	out, ok := probeOutcomeTable[kind]
	if !ok {
		return ProbeOutcome{}, fmt.Errorf("%w: %q", ErrUnknownProbeSignalKind, kind)
	}
	return out, nil
}

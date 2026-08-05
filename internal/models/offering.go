package models

import (
	"errors"
	"fmt"
	"time"
)

// Availability mirrors the M4 account_model_offerings.availability CHECK
// vocabulary verbatim (02 §3): available | withdrawn | unknown.
type Availability string

const (
	AvailabilityAvailable Availability = "available"
	AvailabilityWithdrawn Availability = "withdrawn"
	AvailabilityUnknown   Availability = "unknown"
)

// ErrUnknownAvailability is returned by ParseAvailability for any value
// outside the three-value M4 CHECK vocabulary.
var ErrUnknownAvailability = errors.New("models: unrecognized availability value")

// ParseAvailability fails closed on any value outside the exact M4
// vocabulary — no case folding, no trimming.
func ParseAvailability(s string) (Availability, error) {
	switch Availability(s) {
	case AvailabilityAvailable, AvailabilityWithdrawn, AvailabilityUnknown:
		return Availability(s), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownAvailability, s)
	}
}

// Operation is the offering-operation vocabulary (02 §3 / 04 §5): the unit
// of certification and routing. image_generation and reasoning (added
// 2026-08-05, bounded additive unfreeze — see 02 §3 / 04 §5 / 05 §9) are
// recognized and certifiable but reserved for future scope — not routed by
// any V1 tier (05 §9).
type Operation string

const (
	OperationChat             Operation = "chat"
	OperationStreaming        Operation = "streaming"
	OperationTools            Operation = "tools"
	OperationStructuredOutput Operation = "structured_output"
	OperationVision           Operation = "vision"
	OperationContextWindow    Operation = "context_window"
	OperationImageGeneration  Operation = "image_generation"
	OperationReasoning        Operation = "reasoning"
)

// ErrUnknownOperation is returned by ParseOperation for any value outside
// the fixed eight-operation vocabulary.
var ErrUnknownOperation = errors.New("models: unrecognized operation value")

// operationSet backs both ParseOperation's validity check and Operations'
// enumeration, so the two can never drift apart.
var operationSet = []Operation{
	OperationChat,
	OperationStreaming,
	OperationTools,
	OperationStructuredOutput,
	OperationVision,
	OperationContextWindow,
	OperationImageGeneration,
	OperationReasoning,
}

// ParseOperation fails closed on any value outside the exact eight-value
// vocabulary — no case folding, no trimming, no provider-specific
// extensions.
func ParseOperation(s string) (Operation, error) {
	for _, op := range operationSet {
		if Operation(s) == op {
			return op, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownOperation, s)
}

// Operations returns the fixed eight-value operation enumeration, in the
// order documented by 02 §3 / 04 §5.
func Operations() []Operation {
	out := make([]Operation, len(operationSet))
	copy(out, operationSet)
	return out
}

// OfferingIdentity is an offering's identity per 02 §3: (account_id,
// provider_model_id). The same provider_model_id under two accounts is two
// distinct offerings.
type OfferingIdentity struct {
	AccountID       string
	ProviderModelID string
}

// Offering is a provider account's exposure of a model (02 §3 / 04 §3):
// what this account can do with it. All Offerings under an account inherit
// the account's funding classification — there is no per-Offering funding
// override in v1, so no funding field exists here. ContextLength,
// MaxInputTokens, and MaxOutputTokens are nil when unknown, distinguishing
// unknown from a real zero.
type Offering struct {
	Identity        OfferingIdentity
	Availability    Availability
	ContextLength   *int
	MaxInputTokens  *int
	MaxOutputTokens *int
	Capabilities    []Operation
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
}

// OfferingOperation is the routable and certifiable unit (02 §3 / 04 §5):
// an offering scoped to exactly one operation. Chat and vision on the same
// offering are two distinct offering-operations, each independently
// certified.
type OfferingOperation struct {
	AccountID       string
	ProviderID      string
	ProviderModelID string
	Operation       Operation
}

// NewOfferingOperation validates and constructs an OfferingOperation. It
// fails closed on any empty identity component and on an operation string
// outside the fixed vocabulary.
func NewOfferingOperation(accountID, providerID, providerModelID, operation string) (OfferingOperation, error) {
	if accountID == "" || providerID == "" || providerModelID == "" {
		return OfferingOperation{}, fmt.Errorf("models: NewOfferingOperation: account_id, provider_id, and provider_model_id must all be non-empty")
	}

	op, err := ParseOperation(operation)
	if err != nil {
		return OfferingOperation{}, err
	}

	return OfferingOperation{
		AccountID:       accountID,
		ProviderID:      providerID,
		ProviderModelID: providerModelID,
		Operation:       op,
	}, nil
}

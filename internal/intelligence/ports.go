package intelligence

import (
	"context"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

// CredentialLeaser is the local port matching
// accounts/application.(*CredentialService).Use's exact signature,
// declared here so this package never imports internal/accounts/
// application (which would in turn pull in internal/secrets and
// internal/storage-adjacent concerns). The composition root supplies
// *application.CredentialService, which already implements this
// structurally. fn's plaintext argument must never be copied into a
// variable, field, or return value that outlives fn — the same contract
// CredentialService.Use itself documents.
type CredentialLeaser interface {
	Use(ctx context.Context, credentialID string, fn func(plaintext []byte) error) error
}

// GenerationAllocator allocates the monotonic per-account discovery
// generation and records the discovery_runs 'running' row it belongs to
// (04 §1 step 2). internal/storage's DiscoveryRepo implements this
// structurally over the frozen M4 discovery_runs table.
type GenerationAllocator interface {
	// BeginRun allocates the next generation for accountID — strictly
	// greater than any generation already recorded for that account — and
	// inserts a 'running' discovery_runs row (id=runID, started_at=now).
	// runID is a fresh id the caller supplies; this port never mints one
	// (mirroring StoreCredentialParams.ID's convention).
	BeginRun(ctx context.Context, accountID, runID string, now time.Time) (generation int64, err error)
}

// DiscoverySnapshotModel is one normalized, validated, and sanitized model
// ready for atomic apply (04 §1 steps 4-5). Capabilities is the raw
// (validated, UTF-8/control-char-clean) capability strings the provider
// reported, retained for the record; Operations is the subset of those
// strings that parse against models.Operation's fixed eight-value
// vocabulary and is what actually produces offering_operations rows — an
// unrecognized capability string is kept in Capabilities but contributes
// no operation. Pricing and Evidence have already passed through this
// package's sanitize layer.
//
// CandidateOperations (Task 3) is the parsed form of
// providers.DiscoveredModel.CandidateOperations — operations the adapter did
// NOT declare but still wants a probeable offering_operations row for, dropped
// through the same models.ParseOperation vocabulary check as Operations
// (unrecognized strings are silently dropped, identically). The storage layer
// writes an offering_operations row for the UNION of Operations and
// CandidateOperations, but capabilities_json is derived from Capabilities
// only — CandidateOperations never appears there, which is exactly what keeps
// ListNonChatOperationsToCertify from certifying a candidate as if it had
// been declared.
type DiscoverySnapshotModel struct {
	CanonicalKey        string
	ProviderModelID     string
	DisplayName         string
	ContextLength       *int
	MaxInputTokens      *int
	MaxOutputTokens     *int
	Capabilities        []string
	Operations          []models.Operation
	CandidateOperations []models.Operation
	Pricing             map[string]any
	Evidence            map[string]any
}

// DiscoverySnapshot is one discovery run's fully-validated, ready-to-apply
// result (04 §1 step 5). Withdraw=true is the explicit-empty-list
// authoritative-withdraw case (04 §1's "Semantics"): Models is empty and
// every existing offering for (AccountID, ProviderID) is to be withdrawn.
// Withdraw=false with a non-empty Models is the normal apply case, itself
// authoritative for the account's complete current model list — an
// offering not present in Models is withdrawn too, not merely left alone
// (see DiscoveryRepo.Apply's doc comment for the rationale).
type DiscoverySnapshot struct {
	AccountID  string
	ProviderID string
	Generation int64
	Models     []DiscoverySnapshotModel
	Withdraw   bool
}

// SnapshotApplier is the CAS-guarded atomic snapshot-apply port (04 §1 step
// 5): the "is this generation still the account's newest" decision and the
// resulting offering mutation happen together, inside one storage-side
// transaction, so two concurrently-finishing runs can never both apply.
// internal/storage's DiscoveryRepo implements this structurally over the
// frozen M4 tables.
type SnapshotApplier interface {
	// Apply commits snapshot as its AccountID's offering state, but only if
	// snapshot.Generation is still the highest generation recorded for that
	// account. If a newer generation has since been allocated, this run's
	// discovery_runs row is marked 'superseded' (its snapshot is NOT
	// applied) and applied=false is returned with a nil error. If
	// snapshot.Generation is still newest, the snapshot is written and the
	// discovery_runs row is marked 'applied'.
	Apply(ctx context.Context, runID string, snapshot DiscoverySnapshot, now time.Time) (applied bool, err error)

	// MarkFailed marks runID's discovery_runs row 'failed' with reasonCode,
	// leaving every existing offering row for the account completely
	// untouched (04 §1: "keep the previous snapshot intact").
	MarkFailed(ctx context.Context, runID, reasonCode string, now time.Time) error
}

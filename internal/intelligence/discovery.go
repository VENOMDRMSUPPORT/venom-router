package intelligence

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/sanitize"
)

// MaxDiscoveredModels bounds a single discovery run's reported model count
// (04 §1 step 4: "cap the count"). A count above this is treated the same
// as a truncated/garbled response — a whole-run failure that keeps the
// previous snapshot intact — rather than silently accepted or truncated.
const MaxDiscoveredModels = 5000

// RunOutcome is DiscoveryService.Run's terminal result shape.
type RunOutcome string

const (
	OutcomeApplied    RunOutcome = "applied"
	OutcomeSuperseded RunOutcome = "superseded"
	OutcomeFailed     RunOutcome = "failed"
)

// Typed, safe reason codes for a failed run's discovery_runs.reason_code —
// never a raw provider error, upstream response body, or credential
// fragment.
const (
	ReasonCredentialUnavailable = "credential_unavailable"
	ReasonDiscoveryFailed       = "discovery_failed"
	ReasonInvalidModel          = "invalid_model"
	ReasonTooManyModels         = "too_many_models"
)

// ErrEmptyRunParams is returned when any of RunParams' required fields is
// empty — this service fails closed rather than allocating a generation or
// leasing a credential against a partially-specified request.
var ErrEmptyRunParams = errors.New("intelligence: discovery run requires a non-empty account, provider, credential, and run id")

// RunParams is DiscoveryService.Run's input. AccountID and ProviderID are
// trusted values the caller has already resolved from the account record
// (04 §1 step 1: "take the provider from the trusted account record, never
// client-supplied") — this service does not look them up or validate them
// against storage itself. CredentialID names the account's active
// credential to lease. RunID is a fresh id the caller mints (mirroring
// StoreCredentialParams.ID's convention); this service never mints one.
type RunParams struct {
	AccountID    string
	ProviderID   string
	CredentialID string
	RunID        string
}

// RunResult is Run's outcome: which discovery_runs row it produced, the
// generation allocated, and the terminal outcome. ReasonCode is set only
// when Outcome is OutcomeFailed. ModelCount is the number of models
// included in an applied, non-withdraw snapshot; it is 0 for a withdraw, a
// superseded run, or a failed run.
type RunResult struct {
	RunID      string
	Generation int64
	Outcome    RunOutcome
	ReasonCode string
	ModelCount int
}

// DiscoveryService orchestrates one account-scoped discovery run (04 §1):
// allocate a generation, lease the credential, call the provider's
// DiscoverModels, normalize/validate/sanitize every model, and apply the
// result atomically only if it is still the newest generation. It is pure
// orchestration — every side effect goes through an injected port, and Now
// is an injected clock, so every run is deterministic under test.
type DiscoveryService struct {
	adapter    providers.ModelDiscoveryAdapter
	leaser     CredentialLeaser
	generation GenerationAllocator
	applier    SnapshotApplier
	now        func() time.Time
}

// NewDiscoveryService builds a DiscoveryService over adapter (the trusted
// account's provider-specific ModelDiscoveryAdapter), leaser (the
// credential lease seam), generation (the monotonic generation/run-row
// port), and applier (the CAS-guarded atomic snapshot-apply port). now
// defaults to time.Now when nil.
func NewDiscoveryService(adapter providers.ModelDiscoveryAdapter, leaser CredentialLeaser, generation GenerationAllocator, applier SnapshotApplier, now func() time.Time) *DiscoveryService {
	if now == nil {
		now = time.Now
	}
	return &DiscoveryService{adapter: adapter, leaser: leaser, generation: generation, applier: applier, now: now}
}

// Run executes one discovery run end to end (04 §1's five-step flow).
func (s *DiscoveryService) Run(ctx context.Context, p RunParams) (RunResult, error) {
	if p.AccountID == "" || p.ProviderID == "" || p.CredentialID == "" || p.RunID == "" {
		return RunResult{}, ErrEmptyRunParams
	}

	now := s.now()

	generation, err := s.generation.BeginRun(ctx, p.AccountID, p.RunID, now)
	if err != nil {
		return RunResult{}, fmt.Errorf("intelligence: begin discovery run: %w", err)
	}
	result := RunResult{RunID: p.RunID, Generation: generation}

	var (
		discovered  []providers.DiscoveredModel
		discoverErr error
	)
	leaseErr := s.leaser.Use(ctx, p.CredentialID, func(plaintext []byte) error {
		creds := providers.StoredCredentials{Value: string(plaintext)}
		discovered, discoverErr = s.adapter.DiscoverModels(ctx, creds)
		return nil
	})
	if leaseErr != nil {
		return s.fail(ctx, result, ReasonCredentialUnavailable, now)
	}
	if discoverErr != nil {
		return s.fail(ctx, result, ReasonDiscoveryFailed, now)
	}

	// An explicit empty list is authoritative (04 §1 "Semantics"): it is a
	// successful, applied withdrawal — never a failure.
	if len(discovered) == 0 {
		snapshot := DiscoverySnapshot{AccountID: p.AccountID, ProviderID: p.ProviderID, Generation: generation, Withdraw: true}
		return s.apply(ctx, result, snapshot, now)
	}

	if len(discovered) > MaxDiscoveredModels {
		return s.fail(ctx, result, ReasonTooManyModels, now)
	}

	snapModels := make([]DiscoverySnapshotModel, 0, len(discovered))
	for _, dm := range discovered {
		sm, err := normalizeDiscoveredModel(p.ProviderID, dm)
		if err != nil {
			return s.fail(ctx, result, ReasonInvalidModel, now)
		}
		snapModels = append(snapModels, sm)
	}

	snapshot := DiscoverySnapshot{AccountID: p.AccountID, ProviderID: p.ProviderID, Generation: generation, Models: snapModels}
	return s.apply(ctx, result, snapshot, now)
}

// fail marks the run failed via the applier port (never mutating any
// offering) and returns the corresponding failed RunResult. An error here
// is an infrastructure fault (the MarkFailed call itself failing), not a
// domain outcome, and is propagated as a genuine error.
func (s *DiscoveryService) fail(ctx context.Context, result RunResult, reason string, now time.Time) (RunResult, error) {
	if err := s.applier.MarkFailed(ctx, result.RunID, reason, now); err != nil {
		return RunResult{}, fmt.Errorf("intelligence: mark discovery run failed: %w", err)
	}
	result.Outcome = OutcomeFailed
	result.ReasonCode = reason
	return result, nil
}

// apply hands snapshot to the applier port and translates its CAS outcome
// into a RunResult. An error here is an infrastructure fault, not a domain
// outcome, and is propagated as a genuine error; applied=false (no error)
// is the legitimate "a newer generation already won" outcome.
func (s *DiscoveryService) apply(ctx context.Context, result RunResult, snapshot DiscoverySnapshot, now time.Time) (RunResult, error) {
	applied, err := s.applier.Apply(ctx, result.RunID, snapshot, now)
	if err != nil {
		return RunResult{}, fmt.Errorf("intelligence: apply discovery snapshot: %w", err)
	}
	if !applied {
		result.Outcome = OutcomeSuperseded
		return result, nil
	}
	result.Outcome = OutcomeApplied
	result.ModelCount = len(snapshot.Models)
	return result, nil
}

// normalizeDiscoveredModel normalizes, validates, and sanitizes one
// provider-reported model (04 §1 step 4 / 04 §2: bounds, UTF-8, control
// chars, a zero/negative declared limit fails the record, evidence
// sanitized) and derives its canonical key. Any validation failure returns
// a non-nil error, which the caller treats as a whole-run failure.
func normalizeDiscoveredModel(providerID string, dm providers.DiscoveredModel) (DiscoverySnapshotModel, error) {
	if dm.ProviderModelID == "" {
		return DiscoverySnapshotModel{}, fmt.Errorf("intelligence: discovered model has empty provider_model_id")
	}
	if !validText(dm.ProviderModelID) || !validText(dm.DisplayName) {
		return DiscoverySnapshotModel{}, fmt.Errorf("intelligence: discovered model %q has invalid text (non-UTF-8 or control characters)", dm.ProviderModelID)
	}
	for _, c := range dm.Capabilities {
		if !validText(c) {
			return DiscoverySnapshotModel{}, fmt.Errorf("intelligence: discovered model %q has an invalid capability string", dm.ProviderModelID)
		}
	}
	for _, c := range dm.CandidateOperations {
		if !validText(c) {
			return DiscoverySnapshotModel{}, fmt.Errorf("intelligence: discovered model %q has an invalid candidate operation string", dm.ProviderModelID)
		}
	}
	if err := validLimit(dm.ContextLength); err != nil {
		return DiscoverySnapshotModel{}, fmt.Errorf("intelligence: discovered model %q: context_length: %w", dm.ProviderModelID, err)
	}
	if err := validLimit(dm.MaxInputTokens); err != nil {
		return DiscoverySnapshotModel{}, fmt.Errorf("intelligence: discovered model %q: max_input_tokens: %w", dm.ProviderModelID, err)
	}
	if err := validLimit(dm.MaxOutputTokens); err != nil {
		return DiscoverySnapshotModel{}, fmt.Errorf("intelligence: discovered model %q: max_output_tokens: %w", dm.ProviderModelID, err)
	}

	key, err := models.CanonicalKey(providerID, dm.ProviderModelID)
	if err != nil {
		return DiscoverySnapshotModel{}, err
	}

	var ops []models.Operation
	for _, c := range dm.Capabilities {
		if op, err := models.ParseOperation(c); err == nil {
			ops = append(ops, op)
		}
	}

	// Candidate operations are dropped through the exact same
	// ParseOperation vocabulary check as declared ones (Task 3: "the same
	// dropping rule") — an unrecognized candidate string is silently
	// discarded, never surfaced as a validation error.
	var candidateOps []models.Operation
	for _, c := range dm.CandidateOperations {
		if op, err := models.ParseOperation(c); err == nil {
			candidateOps = append(candidateOps, op)
		}
	}

	return DiscoverySnapshotModel{
		CanonicalKey:        key,
		ProviderModelID:     dm.ProviderModelID,
		DisplayName:         dm.DisplayName,
		ContextLength:       dm.ContextLength,
		MaxInputTokens:      dm.MaxInputTokens,
		MaxOutputTokens:     dm.MaxOutputTokens,
		Capabilities:        dm.Capabilities,
		Operations:          ops,
		CandidateOperations: candidateOps,
		Pricing:             sanitizeMap(dm.Pricing),
		Evidence:            sanitizeMap(dm.Evidence),
	}, nil
}

// validText reports whether s is well-formed UTF-8 containing no control
// characters (04 §1 step 4). An empty string is valid text — DisplayName
// may legitimately be absent.
func validText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// validLimit rejects a declared numeric limit that is zero or negative (04
// §2: "a zero/negative declared limit fails the record rather than being
// stored"). nil (unknown) is always valid — unknown stays unknown, never
// coerced to a sentinel zero.
func validLimit(v *int) error {
	if v == nil {
		return nil
	}
	if *v <= 0 {
		return fmt.Errorf("declared limit must be positive, got %d", *v)
	}
	return nil
}

// sanitizeMap redacts every secret-shaped entry of m via internal/sanitize
// (04 §1 step 4: "sanitize evidence"), keying on each map's own field names
// and stripping known secret token shapes out of any string value. It
// recurses into nested maps/slices — the shapes encoding/json produces
// from an arbitrary provider-supplied JSON blob — so a secret is redacted
// no matter how deep it is nested. A nil input returns nil.
func sanitizeMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if sanitize.IsSecretKey(k) {
			out[k] = sanitize.Placeholder
			continue
		}
		out[k] = sanitizeValue(v)
	}
	return out
}

func sanitizeValue(v any) any {
	switch t := v.(type) {
	case string:
		return sanitize.Text(t)
	case map[string]any:
		return sanitizeMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = sanitizeValue(e)
		}
		return out
	default:
		return v
	}
}

package httpapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	accountsdomain "github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/execution"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/routing"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// StreamSink receives streaming deltas as they arrive so the executor can push
// them to the client the instant they are produced (the P4-WIRE-002 first-byte
// producer). Unit 3 provides the SSE-writing implementation; a non-streaming
// request passes nil.
type StreamSink interface {
	Emit(delta string) error
}

// streamDone is the sentinel Response value a completed stream returns — the
// handler already flushed the deltas through the sink, so nothing more travels
// on FallbackResult.Response.
type streamDone struct{}

// --- AttemptRecorder -------------------------------------------------------

// attemptRecorderAdapter records the content-free attempt identity through the
// RouteRecorder (which logs-and-swallows any write error, so this never fails
// the loop). The row is a pre-reservation identity marker (Status pending);
// the terminal outcome is captured by the usage record (P5-PAPI-002).
type attemptRecorderAdapter struct {
	rec        *observability.RouteRecorder
	decisionID string
	now        func() time.Time
	n          int
}

func (a *attemptRecorderAdapter) RecordAttempt(ctx context.Context, rec routing.AttemptRecord) error {
	a.n++
	return a.rec.RecordAttempt(ctx, observability.RouteAttempt{
		ID:                  rec.AttemptID,
		RouteDecisionID:     a.decisionID,
		AttemptNumber:       a.n,
		AccountID:           rec.AccountID,
		OfferingOperationID: rec.ProviderModelID,
		Status:              observability.RouteStatusPending,
		StartedAt:           a.now(),
	})
}

// --- Reserver --------------------------------------------------------------

// reserverAdapter wraps QuotaReservationRepo.Reserve, translating
// storage.ErrReservationRejected into routing.ErrReservationRejected so the
// loop's re-evaluate branch (errors.Is) fires — never releasing on a rejection
// that debited nothing.
// quotaReserver is the storage-side reservation surface, an interface so a test
// can force a rejection without seeding windows. *storage.QuotaReservationRepo
// satisfies it.
type quotaReserver interface {
	Reserve(ctx context.Context, p storage.ReserveParams) (storage.ReserveResult, error)
}

type reserverAdapter struct{ repo quotaReserver }

func (a *reserverAdapter) Reserve(ctx context.Context, p routing.ReserveParams) (routing.ReserveResult, error) {
	res, err := a.repo.Reserve(ctx, storage.ReserveParams{
		AccountID:   p.AccountID,
		RequestID:   p.RequestID,
		AttemptID:   p.AttemptID,
		Allocations: p.Allocations,
	})
	if errors.Is(err, storage.ErrReservationRejected) {
		return routing.ReserveResult{}, fmt.Errorf("%w: %v", routing.ErrReservationRejected, err)
	}
	if err != nil {
		return routing.ReserveResult{}, err
	}
	return routing.ReserveResult{ReservationID: res.ReservationID, Idempotent: res.Idempotent}, nil
}

// --- Lifecycle -------------------------------------------------------------

// lifecycleAdapter maps routing's lifecycle onto QuotaLifecycleRepo.
type lifecycleAdapter struct{ repo *storage.QuotaLifecycleRepo }

func (a *lifecycleAdapter) MarkDispatched(ctx context.Context, reservationID string) error {
	return a.repo.MarkDispatched(ctx, reservationID)
}

func (a *lifecycleAdapter) Settle(ctx context.Context, reservationID string, actuals map[quota.Unit]float64) error {
	return a.repo.Settle(ctx, reservationID, unitsToStringMap(actuals))
}

func (a *lifecycleAdapter) SettleEstimate(ctx context.Context, reservationID string) error {
	return a.repo.SettleEstimate(ctx, reservationID)
}

func (a *lifecycleAdapter) Release(ctx context.Context, reservationID string) error {
	return a.repo.Release(ctx, reservationID)
}

func (a *lifecycleAdapter) MarkReconciliationPending(ctx context.Context, reservationID string) error {
	return a.repo.Transition(ctx, reservationID, quota.ReservationReconciliationPending)
}

// unitsToStringMap converts routing's map[quota.Unit]float64 to the repo's
// map[string]float64 without losing a unit (quota.Unit is a string type).
func unitsToStringMap(in map[quota.Unit]float64) map[string]float64 {
	if in == nil {
		return nil
	}
	out := make(map[string]float64, len(in))
	for u, v := range in {
		out[string(u)] = v
	}
	return out
}

// --- AttemptIDMinter -------------------------------------------------------

// attemptIDMinterFunc adapts a plain function to routing.AttemptIDMinter.
type attemptIDMinterFunc func(requestID string, attemptNumber int) string

func (f attemptIDMinterFunc) MintAttemptID(requestID string, attemptNumber int) string {
	return f(requestID, attemptNumber)
}

// defaultAttemptIDMinter is the deterministic, distinct-per-attempt minter.
func defaultAttemptIDMinter(requestID string, attemptNumber int) string {
	return fmt.Sprintf("%s#att%d", requestID, attemptNumber)
}

// --- PoolReEvaluator -------------------------------------------------------

// poolReEvaluatorAdapter returns a FRESH snapshot (never a cached one) via the
// same assembly the initial pool used.
type poolReEvaluatorAdapter struct {
	builder *SnapshotBuilder
	tier    routing.Tier
	reqs    routing.Requirements
}

func (a *poolReEvaluatorAdapter) ReEvaluate(ctx context.Context) (routing.RoutePool, error) {
	res, err := a.builder.Build(ctx, a.tier, a.reqs)
	if err != nil {
		return routing.RoutePool{}, err
	}
	return res.Pool, nil
}

// --- Executor --------------------------------------------------------------

// executorAdapter resolves the credential, builds a single-choice
// ResolvedRoute + NormalizedRequest, dispatches (streaming or not), and maps
// the result to routing.ExecOutcome. The credential is decrypted once inside
// CredentialService.Use and NEVER copied out of that callback or logged.
// credentialLister and credentialUser are the executor's two credential seams,
// interfaces so a test can drive the executor without the full encryption
// stack. *storage.AccountCredentialRepo and *application.CredentialService
// satisfy them.
type credentialLister interface {
	ListForAccount(ctx context.Context, accountID string) ([]accountsdomain.Credential, error)
}

type credentialUser interface {
	Use(ctx context.Context, credentialID string, fn func(plaintext []byte) error) error
}

type executorAdapter struct {
	dispatcher  *execution.Dispatcher
	classify    func(execution.ResolvedRoute, error) execution.TypedFailure
	creds       credentialLister
	credService credentialUser
	baseURLFor  func(providerID string) string
	content     execution.NormalizedRequest // messages/tools/parts/max_tokens only
	stream      bool
	sink        StreamSink
	inflight    *inflightCounter
	routeHolder *execution.ResolvedRoute // shared with the classify closure (loop is single-goroutine)
}

// ErrNoActiveCredential is returned (as a request-scope failure) when a chosen
// account has no active credential to decrypt.
var ErrNoActiveCredential = errors.New("httpapi: account has no active credential")

func (a *executorAdapter) Execute(ctx context.Context, attempt routing.ResolvedAttempt) routing.ExecOutcome {
	cand := attempt.Candidate
	credID, ok := a.activeCredentialID(ctx, cand.AccountID)
	if !ok {
		return routing.ExecOutcome{Err: ErrNoActiveCredential}
	}

	a.inflight.inc(cand.AccountID)
	defer a.inflight.dec(cand.AccountID)

	req := a.content
	req.RequestID = attempt.RequestID
	req.Operation = execution.OperationChat
	req.Stream = a.stream

	// The route WITHOUT the credential is shared with the classifier (it needs
	// only Provider/Model to resolve the transport). The credential is added to
	// a LOCAL copy inside the decrypt callback and never escapes it.
	base := execution.ResolvedRoute{
		Provider:  execution.ProviderID(cand.ProviderID),
		AccountID: cand.AccountID,
		ModelID:   cand.ProviderModelID,
		BaseURL:   a.baseURLFor(cand.ProviderID),
	}
	if a.routeHolder != nil {
		*a.routeHolder = base
	}

	var outcome routing.ExecOutcome
	useErr := a.credService.Use(ctx, credID, func(plaintext []byte) error {
		route := base
		route.Credential = execution.StoredCredentials{Value: string(plaintext)}
		if a.stream {
			outcome = a.runStream(ctx, route, req)
		} else {
			outcome = a.runExecute(ctx, route, req)
		}
		return nil
	})
	if useErr != nil {
		return routing.ExecOutcome{Err: useErr}
	}
	return outcome
}

func (a *executorAdapter) runExecute(ctx context.Context, route execution.ResolvedRoute, req execution.NormalizedRequest) routing.ExecOutcome {
	resp, err := a.dispatcher.Execute(ctx, route, req)
	if err != nil {
		return routing.ExecOutcome{Err: err, RetryAfter: retryAfterOf(a.classify(route, err))}
	}
	return routing.ExecOutcome{Response: resp}
}

func (a *executorAdapter) runStream(ctx context.Context, route execution.ResolvedRoute, req execution.NormalizedRequest) routing.ExecOutcome {
	ch, err := a.dispatcher.Stream(ctx, route, req)
	if err != nil {
		// Pre-first-byte failure: no chunk reached the client.
		return routing.ExecOutcome{Err: err, RetryAfter: retryAfterOf(a.classify(route, err)), StreamStarted: false}
	}
	started := false
	for chunk := range ch {
		switch {
		case chunk.Err != nil:
			return routing.ExecOutcome{Err: chunk.Err, StreamStarted: started, RetryAfter: retryAfterOf(a.classify(route, chunk.Err))}
		case chunk.Done:
			return routing.ExecOutcome{Response: streamDone{}, StreamStarted: started}
		case chunk.Delta != "":
			started = true // the first byte has now reached the client
			if a.sink != nil {
				if serr := a.sink.Emit(chunk.Delta); serr != nil {
					return routing.ExecOutcome{Err: serr, StreamStarted: started}
				}
			}
		}
	}
	// Channel closed without a Done marker — a truncated stream.
	return routing.ExecOutcome{Err: execution.ErrStreamTruncated, StreamStarted: started}
}

// activeCredentialID returns the id of accountID's active credential.
func (a *executorAdapter) activeCredentialID(ctx context.Context, accountID string) (string, bool) {
	creds, err := a.creds.ListForAccount(ctx, accountID)
	if err != nil {
		return "", false
	}
	for _, c := range creds {
		if c.State == accountsdomain.CredentialActive {
			return c.ID, true
		}
	}
	return "", false
}

// NewDispatcherFailureClassifier builds the Classify function EngineDeps needs:
// it resolves the transport type for a route via the same registry resolver the
// dispatcher uses, then calls that transport's Failure(err, route). An
// unresolvable route or missing impl fails closed to a retryable transient
// server failure (never a guessed pre-consumption release). The concrete
// transports ignore the route in Failure, but the route's Provider is what
// selects the RIGHT transport — so classification is always by the transport
// that actually produced the error.
func NewDispatcherFailureClassifier(reg *providers.Registry, impls map[execution.TransportType]execution.InferenceTransport) func(execution.ResolvedRoute, error) execution.TypedFailure {
	resolver := NewRegistryTransportResolver(reg)
	return func(route execution.ResolvedRoute, err error) execution.TypedFailure {
		tt, terr := resolver.TransportTypeFor(route)
		if terr != nil {
			return execution.TypedFailure{FailureClass: execution.FailureClassServer, Scope: execution.FailureScopeTransientTransport, Retryable: true, SafeMessage: "an internal error occurred"}
		}
		t, ok := impls[tt]
		if !ok {
			return execution.TypedFailure{FailureClass: execution.FailureClassServer, Scope: execution.FailureScopeTransientTransport, Retryable: true, SafeMessage: "an internal error occurred"}
		}
		return t.Failure(err, route)
	}
}

// retryAfterOf converts a typed failure's Retry-After (seconds) to a duration.
func retryAfterOf(tf execution.TypedFailure) time.Duration {
	if tf.RetryAfter != nil {
		return time.Duration(*tf.RetryAfter) * time.Second
	}
	return 0
}

// --- Composition -----------------------------------------------------------

// EngineDeps holds the process-wide collaborators the request path composes a
// routing.FallbackInput from. One instance lives at the composition root.
type EngineDeps struct {
	Snapshot      *SnapshotBuilder
	Reservations  *storage.QuotaReservationRepo
	Lifecycle     *storage.QuotaLifecycleRepo
	RouteRecorder *observability.RouteRecorder
	Dispatcher    *execution.Dispatcher
	// Classify resolves the transport for a route and returns its TypedFailure
	// for err (scope + retry-after). The composition root builds it from the
	// provider registry + transport impls; tests inject a fake.
	Classify    func(execution.ResolvedRoute, error) execution.TypedFailure
	Creds       *storage.AccountCredentialRepo
	CredService *application.CredentialService
	BaseURLFor  func(providerID string) string
	Inflight    *inflightCounter
	Cache       *routing.StickinessCache
	Now         func() time.Time
	Minter      routing.AttemptIDMinter
}

// RequestPlan is one request's already-derived inputs.
type RequestPlan struct {
	Tier          routing.Tier
	Policy        routing.TierPolicy
	Requirements  routing.Requirements
	RequestID     string
	DecisionID    string
	StickinessKey string
	EstimateInput quota.EstimateInput
	Content       execution.NormalizedRequest
	Stream        bool
	Sink          StreamSink
}

// BuildFallbackInput assembles every routing port over deps + plan + the
// already-built snapshot into a ready routing.FallbackInput. The ScopeClassifier
// is reused for BOTH Classifier and Scoper (never a second classifier).
func (d *EngineDeps) BuildFallbackInput(plan RequestPlan, snap SnapshotResult) routing.FallbackInput {
	holder := &execution.ResolvedRoute{}
	classifier := NewScopeClassifier(func(err error) execution.TypedFailure {
		return d.Classify(*holder, err)
	})
	minter := d.Minter
	if minter == nil {
		minter = attemptIDMinterFunc(defaultAttemptIDMinter)
	}
	return routing.FallbackInput{
		Tier:          plan.Tier,
		Policy:        plan.Policy,
		Pool:          snap.Pool,
		Requirements:  plan.Requirements,
		RequestID:     plan.RequestID,
		StickinessKey: plan.StickinessKey,
		Cache:         d.Cache,
		DRRState:      routing.DRRState{},
		Need:          1,
		EstimateInput: plan.EstimateInput,
		Now:           d.Now(),
		StaleAfter:    quota.DefaultStalenessWindow,
		Recorder:      &attemptRecorderAdapter{rec: d.RouteRecorder, decisionID: plan.DecisionID, now: d.Now},
		Reserver:      &reserverAdapter{repo: d.Reservations},
		Lifecycle:     &lifecycleAdapter{repo: d.Lifecycle},
		Executor: &executorAdapter{
			dispatcher:  d.Dispatcher,
			classify:    d.Classify,
			creds:       d.Creds,
			credService: d.CredService,
			baseURLFor:  d.BaseURLFor,
			content:     plan.Content,
			stream:      plan.Stream,
			sink:        plan.Sink,
			inflight:    d.Inflight,
			routeHolder: holder,
		},
		Classifier:  classifier,
		Scoper:      classifier,
		ReEvaluator: &poolReEvaluatorAdapter{builder: d.Snapshot, tier: plan.Tier, reqs: plan.Requirements},
		Minter:      minter,
	}
}

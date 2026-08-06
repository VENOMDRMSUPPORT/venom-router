package httpapi

// qualification.go is the automatic-model-qualification design's Task 2: the
// scheduler tick that is now the ONLY writer of models.quality_rating.
//
// The dashboard's benchmark trigger (POST /models/{id}/benchmark,
// benchmark.go) was deleted on the owner's instruction — they will never
// press a button. The endpoint is still mounted (ServeBenchmark, runBenchmark
// still reachable) but nothing calls it: not the dashboard, not a tick, not a
// sweep. Without this file, models.quality_rating has no writer at all, so
// "Not rated" is permanently unearnable.
//
// This is deliberately a WIRING file, not a second measurement engine. Every
// piece of actual work is reused from what task-5's local-benchmark-rating
// plan already built and tested:
//   - runBenchmarkSuite / localBenchmarkRating (benchmark_engine.go) — the
//     suite, medians, and rating formula.
//   - buildBenchmarkStreamFn (benchmark.go) — the production dispatch seam,
//     composed exactly as NewBenchmarkHandler composes it.
//   - benchmarkRatingColumnScale (benchmark.go) — the 0..1 -> 0-100 scale
//     conversion. See that constant's doc comment for the prior incident
//     (a perfect benchmark persisted as a near-worst score) this exists to
//     prevent; the tick below applies it at the exact same single write
//     site, never a second time.
//   - storage.BenchmarkRunRepo.Insert / LatestForModels, storage.CatalogRepo.
//     ListOfferings(LiveOnly) / SetQualityRating — the existing persistence
//     and freshness-read paths.
//
// If a change here starts looking like a second median, a second stream
// drainer, or a second rating formula, that is a wrong turn — stop and reuse
// the existing one instead.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/execution"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// qualificationFreshnessTTL bounds how often ANY one model gets re-measured.
//
// DECISION (design choice 1 of 3, task-2 brief): 24 hours. A provider's
// serving performance (the thing this local benchmark measures — TTFT and
// tokens/sec on the owner's own account) is a property of the provider's
// infrastructure, not something that meaningfully drifts minute to minute.
// Re-measuring on the scheduler's own 30s round (DefaultSchedulerInterval)
// would spend the owner's real inference quota, over and over, on a number
// that has not changed since the last measurement. 24 hours is short enough
// that a genuine provider regression or improvement is reflected within a
// day, and long enough that a model measured once stays "rated" for the
// whole day rather than being re-benchmarked dozens of times. It is a named
// constant, not a magic literal, specifically so a future owner/operator
// decision to tighten or loosen it has exactly one place to change.
const qualificationFreshnessTTL = 24 * time.Hour

// qualificationPerRoundCap bounds how many models ONE tick round will
// measure.
//
// DECISION (design choice 2 of 3, task-2 brief): 5. The freshness TTL above
// already stops a STEADY-STATE fleet from being reselected every round — the
// cap's real job is bounding the CATCH-UP sweep: the first tick after boot,
// or after a fleet of (say) 200 models is discovered fresh, would otherwise
// see all 200 as "never measured" and run 200 sequential 3-request suites in
// one round, potentially stampeding a single provider's rate limits and
// burning a large slice of the owner's quota in seconds. Capping at 5 models
// per round (5 * benchmarkDefaultRequests = 15 sequential streamed
// completions, spread across whatever accounts/providers those 5 models
// happen to live on) keeps one round's fan-out small while a large
// never-measured backlog still drains within a bounded number of rounds
// rather than never draining because the cap is 1, or never protecting
// anyone because the cap is unbounded. A cap enforced SILENTLY would read as
// "everything was measured" to anyone watching only the tick's success
// return value — see qualificationTick.Run's logging of exactly what was
// skipped and why.
const qualificationPerRoundCap = 5

// qualificationCapabilityProbeCap bounds how many capability probes ONE tick
// round will run — mirroring qualificationPerRoundCap's own reasoning: a
// fleet whose declared tools/structured_output/vision capabilities have
// never been probed would otherwise fan out unboundedly the first round
// after this capability landed. These probes are far cheaper than a
// 3-request benchmark suite (one fixed tiny fixture request each), but the
// same "bounded catch-up, never silent" discipline still applies, so the
// number is kept identical to qualificationPerRoundCap rather than invented
// a second time. probeCapabilities logs exactly what was deferred, for the
// same reason qualificationPerRoundCap's own doc comment gives.
const qualificationCapabilityProbeCap = 5

// capabilityProbeOperations is the fixed three-value subset of the non-chat
// operation vocabulary this tick may ever probe (task-3 of the automatic-
// model-qualification plan). tools/structured_output/vision are genuinely
// probeable now that the probe port carries tools/parts/response-format
// (Task 1, commit 52cb717). context_window is Task 4's own, separately
// gated, far more expensive probe; reasoning and image_generation have no
// wire expression on this seam (execution.NormalizedResponse carries
// neither a reasoning field nor an image output part, confirmed by a
// repo-wide search — see task-3-report.md); chat is never probed here at
// all — it is proven only by the usability sweep's real use.
var capabilityProbeOperations = []models.Operation{
	models.OperationTools,
	models.OperationStructuredOutput,
	models.OperationVision,
}

// capabilityProbeCandidate is one offering-operation a round may probe: a
// non-chat capability already certified/supported FROM ITS DECLARATION
// (certifyDeclaredCapabilities, usability_account.go) with no succeeded
// probe run yet — exactly the fact intelligence/readmodel.go's
// projectCapabilities reads (proved[op], derived from
// ProbeRunRepo.SucceededOfferingOperationIDs) to decide whether a
// certified/supported capability's provenance renders as "declared" or
// "probed". Probing it is what moves that chip.
type capabilityProbeCandidate struct {
	OfferingOperationID string
	Operation           models.Operation
	AccountID           string
	ProviderID          string
	ProviderModelID     string
}

// qualificationTick is the scheduler-tick body. Dependencies are closures
// over the existing repos/seam, mirroring tokenRefreshTick/usabilityTick's
// shape — the composition-root wiring lives in BuildQualificationTick, and a
// test drives this struct directly with a fake stream (no network I/O).
type qualificationTick struct {
	catalog *storage.CatalogRepo
	runs    *storage.BenchmarkRunRepo
	stream  benchmarkStreamFn
	newID   func() string
	now     func() time.Time
	log     *observability.Logger

	// db backs dueCapabilityProbes' own raw query: none of catalog/runs above
	// is shaped for the offering_operations/certifications/probe_runs
	// three-way join a "certified/supported, no succeeded probe run yet"
	// selection needs, so this tick reads it directly rather than growing a
	// storage-repo method for a query only this one caller needs.
	db *storage.DB
	// probeRuns is shared for BOTH bookkeeping (Start/Finish/CountAttempts,
	// exactly like ProbeHandler.runProbe) AND every intelligence.ProbeGuard
	// read port that is not the reservation itself (ProbeSpendReader,
	// ProbeInFlightReader via the inFlightExcluding wrapper below,
	// ProbeCooldownReader) — one repo, never a second instance that could
	// disagree with the first about what has already run.
	probeRuns *storage.ProbeRunRepo
	// probeReserver is the ONE quota-consuming port a capability probe's
	// admission needs beyond probeRuns above.
	probeReserver intelligence.ProbeReserver
	// probeGuardPolicy is the safety policy every capability-probe attempt is
	// admitted against — see BuildQualificationTick's own assignment for the
	// task-3 brief's required concurrency-limit decision and its reasoning.
	probeGuardPolicy intelligence.ProbeSafetyPolicy
	// probeTransport is the seam intelligence.CapabilityProbe actually calls:
	// the production probeTransportAdapter (controlmux.go:403's own
	// construction, independently composed here exactly as
	// buildBenchmarkStreamFn already is above) in production, a fixed-result
	// fake in tests — never live network either way, per this plan's global
	// constraint.
	probeTransport intelligence.ProbeTransport
	// driver is the narrow RecordAttempt slice of *intelligence.
	// CertificationDriver usability_verify.go's certRecorder already
	// declares, reused rather than re-declared for the same purpose here:
	// turn a probe outcome into a certification-lifecycle move.
	driver certRecorder
}

// qualificationCandidate is one live-offering model this round considered:
// exactly the (account, provider, provider-model) triple runBenchmarkSuite
// needs, plus the canonical model id the rating is written onto.
type qualificationCandidate struct {
	ModelID         string
	AccountID       string
	ProviderID      string
	ProviderModelID string
}

// Run selects every model with a live chat offering whose most recent
// benchmark_runs row is missing or older than qualificationFreshnessTTL,
// measures up to qualificationPerRoundCap of them through the existing
// suite, and persists through the existing writers.
//
// A LIST-phase failure (the fleet could not even be enumerated) is returned
// for the scheduler to log, exactly like the sibling ticks. A single model's
// measurement failure only logs and moves on — one flaky provider must not
// stop the rest of the round from being measured, mirroring runBenchmarkSuite's
// own "one bad sample never loses the others" discipline one level up.
func (t *qualificationTick) Run(ctx context.Context) error {
	// Task 3: upgrade declared capabilities to measured ones. Run first (its
	// own failure is logged, never fatal to the round — see probeCapabilities'
	// own doc comment) so the performance-scoring pass below still runs
	// exactly as it always has, regardless of whether any capability probing
	// was due this round.
	t.probeCapabilities(ctx)

	due, err := t.dueModels(ctx)
	if err != nil {
		return fmt.Errorf("httpapi: model qualification: list due models: %w", err)
	}
	if len(due) == 0 {
		return nil
	}

	measure := due
	if len(measure) > qualificationPerRoundCap {
		measure = due[:qualificationPerRoundCap]
	}
	if skipped := len(due) - len(measure); skipped > 0 {
		skippedIDs := make([]string, 0, skipped)
		for _, c := range due[len(measure):] {
			skippedIDs = append(skippedIDs, c.ModelID)
		}
		// Logged, never silent: a cap nobody can see reads as "everything was
		// measured this round", which is exactly the dishonesty this project
		// keeps fixing. These models remain due and are picked up on a later
		// round (LatestForModels will keep reporting them as stale/absent
		// until they are actually measured).
		t.log.Info("model qualification: per-round cap reached, deferring the rest to a later round",
			observability.Int("cap", qualificationPerRoundCap),
			observability.Int("due", len(due)),
			observability.Int("deferred", skipped),
			observability.String("deferred_model_ids", fmt.Sprintf("%v", skippedIDs)),
		)
	}

	for _, candidate := range measure {
		if err := t.measureOne(ctx, candidate); err != nil {
			t.log.Warn("model qualification: measuring one model failed, continuing with the rest of the round",
				observability.String("model_id", candidate.ModelID),
				observability.Err(err),
			)
		}
	}
	return nil
}

// dueModels resolves EVERY model with a live chat offering (the honest gate:
// CatalogRepo.ListOfferings' LiveOnly — available, account connected/
// healthy/not-reauthenticating, AND a certified+supported chat
// offering_operation; the SAME basis BenchmarkHandler.targetOffering already
// selects on, storage/catalog.go's CatalogListParams doc comment), walking
// every page rather than assuming the fleet fits on one, then filters to
// those whose most recent benchmark_runs row (LatestForModels — the existing
// indexed batched freshness read) is missing or stale.
//
// LatestForModels is called ONCE PER PAGE of ListOfferings, never once over
// the whole fleet's accumulated id list. BenchmarkRunRepo.LatestForModels's
// own doc comment is explicit about this: "Never remove the caller-side page
// bound on the assumption this query can absorb an unbounded id list" — its
// one existing caller (ServeModels) already respects that by construction,
// scoping each call to one page of offerings (bounded by
// defaultCatalogListLimit). Accumulating every live model id across every
// page into one slice before a single LatestForModels call would silently
// reverse that documented invariant: harmless at today's fleet size, but a
// hard SQL error once the id list exceeds SQLite's bind-parameter ceiling.
// Calling it per page keeps every call's argument bounded by the SAME page
// size ListOfferings itself already enforces.
//
// One offering is kept per model (the first one ListOfferings returns for
// it, in its own deterministic account_id/provider_model_id order) — a model
// with several live offerings only needs ONE of them measured to earn a
// rating, exactly like targetOffering picks exactly one. seen tracks model
// ids already resolved on an earlier page so a model whose several offerings
// land on different pages is never double-counted or double-queried.
func (t *qualificationTick) dueModels(ctx context.Context) ([]qualificationCandidate, error) {
	seen := make(map[string]bool)
	var due []qualificationCandidate
	now := t.now()

	cursor := ""
	for {
		rows, next, err := t.catalog.ListOfferings(ctx, storage.CatalogListParams{LiveOnly: true, Cursor: cursor})
		if err != nil {
			return nil, err
		}

		pageOrder := make([]string, 0, len(rows))
		pageByModel := make(map[string]qualificationCandidate, len(rows))
		for _, row := range rows {
			if seen[row.ModelID] {
				continue
			}
			seen[row.ModelID] = true
			pageByModel[row.ModelID] = qualificationCandidate{
				ModelID:         row.ModelID,
				AccountID:       row.AccountID,
				ProviderID:      row.ProviderID,
				ProviderModelID: row.ProviderModelID,
			}
			pageOrder = append(pageOrder, row.ModelID)
		}

		if len(pageOrder) > 0 {
			latest, err := t.runs.LatestForModels(ctx, pageOrder)
			if err != nil {
				return nil, err
			}
			for _, modelID := range pageOrder {
				if run, ok := latest[modelID]; ok && now.Sub(run.FinishedAt) < qualificationFreshnessTTL {
					continue
				}
				due = append(due, pageByModel[modelID])
			}
		}

		if next == "" {
			break
		}
		cursor = next
	}
	return due, nil
}

// measureOne runs the existing fixed suite against candidate's live offering
// and persists through the existing writers — the SAME two writes
// runBenchmark performs (benchmark.go:296-308), just without the job-row
// bookkeeping a background tick has no use for.
//
// A benchmark_runs row is inserted for every measured attempt, even one
// where every request failed (the measurement is evidence — runBenchmark's
// own documented rule, reused verbatim here). models.quality_rating is
// written ONLY when runBenchmarkSuite populated Rating (every request in the
// suite succeeded) — that success gate belongs to runBenchmarkSuite and is
// never relaxed or re-implemented here — and it is written on the 0-100
// COLUMN scale via benchmarkRatingColumnScale, never the raw 0..1 measurement
// (see that constant's doc comment).
func (t *qualificationTick) measureOne(ctx context.Context, candidate qualificationCandidate) error {
	startedAt := t.now()
	aggregate := runBenchmarkSuite(ctx, t.stream, candidate.AccountID, candidate.ProviderID, candidate.ProviderModelID, benchmarkDefaultRequests)
	finishedAt := t.now()

	run := storage.BenchmarkRun{
		ID:              t.newID(),
		ModelID:         candidate.ModelID,
		AccountID:       candidate.AccountID,
		ProviderID:      candidate.ProviderID,
		ProviderModelID: candidate.ProviderModelID,
		Requests:        aggregate.Requests,
		Successes:       aggregate.Successes,
		TTFTMillis:      aggregate.TTFTMillis,
		TokensPerSec:    aggregate.TokensPerSec,
		Rating:          aggregate.Rating,
		StartedAt:       startedAt,
		FinishedAt:      finishedAt,
	}
	if err := t.runs.Insert(ctx, run); err != nil {
		return fmt.Errorf("insert benchmark run: %w", err)
	}

	if aggregate.Rating != nil {
		if err := t.catalog.SetQualityRating(ctx, candidate.ModelID, *aggregate.Rating*benchmarkRatingColumnScale, t.now()); err != nil {
			return fmt.Errorf("set quality rating: %w", err)
		}
	}
	return nil
}

// dueCapabilityProbes resolves every offering-operation that is
// certified/supported, whose operation is one of capabilityProbeOperations,
// and which has no succeeded probe_runs row yet — a direct query against
// offering_operations/certifications/probe_runs (none of t.catalog's
// existing methods is shaped for this three-way join), in deterministic
// (offering_operation_id ASC) order. Every matching row is returned
// unbounded here; probeCapabilities applies qualificationCapabilityProbeCap
// and logs what it defers, mirroring dueModels/Run's own cap-after-select
// shape.
func (t *qualificationTick) dueCapabilityProbes(ctx context.Context) ([]capabilityProbeCandidate, error) {
	placeholders := make([]string, len(capabilityProbeOperations))
	args := make([]any, len(capabilityProbeOperations))
	for i, op := range capabilityProbeOperations {
		placeholders[i] = "?"
		args[i] = string(op)
	}

	query := `SELECT oo.id, oo.operation, oo.account_id, oo.provider_id, oo.provider_model_id
		FROM offering_operations oo
		JOIN certifications c ON c.offering_operation_id = oo.id
		WHERE c.status = 'certified' AND c.capability_truth = 'supported'
		  AND oo.operation IN (` + strings.Join(placeholders, ",") + `)
		  AND NOT EXISTS (
		    SELECT 1 FROM probe_runs pr
		    WHERE pr.offering_operation_id = oo.id AND pr.execution = 'succeeded'
		  )
		ORDER BY oo.id ASC`

	rows, err := t.db.Conn().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("httpapi: model qualification: list due capability probes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []capabilityProbeCandidate
	for rows.Next() {
		var c capabilityProbeCandidate
		var opStr string
		if err := rows.Scan(&c.OfferingOperationID, &opStr, &c.AccountID, &c.ProviderID, &c.ProviderModelID); err != nil {
			return nil, fmt.Errorf("httpapi: model qualification: scan due capability probe: %w", err)
		}
		op, err := models.ParseOperation(opStr)
		if err != nil {
			return nil, fmt.Errorf("httpapi: model qualification: due capability probe: %w", err)
		}
		c.Operation = op
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("httpapi: model qualification: list due capability probes: %w", err)
	}
	return out, nil
}

// probeCapabilities runs one round of task 3 (automatic-model-qualification
// plan): for every candidate dueCapabilityProbes selects, run the existing
// intelligence.CapabilityProbe through the existing intelligence.ProbeGuard
// and record whatever it produces through the existing
// CertificationDriver.RecordAttempt — never a second interpretation of the
// outcome (04 §2's hard rule; intelligence.ClassifyProbeSignal already
// encodes it, see probeOneCapability's own doc comment for exactly how that
// holds even though every candidate here starts already certified/
// supported).
//
// A LIST-phase failure is logged and this round's capability probing is
// simply skipped — never fatal to the tick as a whole, exactly like
// dueModels' own failure aborts only the performance-scoring half of Run,
// never the other. A per-candidate failure is likewise logged and the round
// continues with the rest, mirroring measureOne's "one bad row never aborts
// the rest" discipline one level up.
func (t *qualificationTick) probeCapabilities(ctx context.Context) {
	due, err := t.dueCapabilityProbes(ctx)
	if err != nil {
		t.log.Warn("model qualification: listing due capability probes failed, skipping this round's capability probing",
			observability.Err(err))
		return
	}
	if len(due) == 0 {
		return
	}

	probe := due
	if len(probe) > qualificationCapabilityProbeCap {
		probe = due[:qualificationCapabilityProbeCap]
	}
	if skipped := len(due) - len(probe); skipped > 0 {
		skippedIDs := make([]string, 0, skipped)
		for _, c := range due[len(probe):] {
			skippedIDs = append(skippedIDs, c.OfferingOperationID)
		}
		// Logged, never silent — see qualificationPerRoundCap's own doc
		// comment for why a cap nobody can see is a dishonesty this project
		// keeps fixing.
		t.log.Info("model qualification: capability-probe per-round cap reached, deferring the rest to a later round",
			observability.Int("cap", qualificationCapabilityProbeCap),
			observability.Int("due", len(due)),
			observability.Int("deferred", skipped),
			observability.String("deferred_offering_operation_ids", fmt.Sprintf("%v", skippedIDs)),
		)
	}

	for _, c := range probe {
		if err := t.probeOneCapability(ctx, c); err != nil {
			t.log.Warn("model qualification: probing one capability failed, continuing with the rest of the round",
				observability.String("offering_operation_id", c.OfferingOperationID),
				observability.String("operation", string(c.Operation)),
				observability.Err(err),
			)
		}
	}
}

// probeOneCapability runs exactly one capability-probe attempt for c and
// records its outcome. The probe_runs row (Start/Finish) is written
// regardless of what the certification driver ends up doing with the
// outcome below — a succeeded row is exactly what intelligence/readmodel.go's
// SucceededOfferingOperationIDs reads to derive provenance=probed,
// independent of the certification lifecycle itself.
//
// c is selected by dueCapabilityProbes ONLY when its certification is
// already certified/supported — declared, in this task's own vocabulary.
// That is exactly why calling t.driver.RecordAttempt(outcome) UNCONDITIONALLY
// below is safe, and not a second interpretation of the outcome:
// models.Certification.Transition's frozen legal-transition table
// (models/certification.go) has no certified -> certified edge and no
// certified -> probing edge. So:
//   - a genuine capability_response (Definitive, Truth=Supported) targets
//     certified -> certified, which Transition rejects as illegal;
//   - any non-definitive/infrastructure outcome (rate-limited, timeout,
//     malformed, …) targets certified -> probing, which Transition rejects
//     the same way;
//   - either way RecordAttempt returns a wrapped
//     models.ErrIllegalCertificationTransition and the certification is
//     returned byte-for-byte unchanged (CompareAndSwap is never reached) —
//     the exact "never downgrade on missing evidence" invariant this task
//     exists to prove, enforced by the SAME frozen table every other
//     certification edge already goes through, not a bespoke check added
//     here.
//
// The one outcome that DOES legally move an already certified/supported row
// is a genuine terminal failure (401/403, credential-blocked): certified ->
// suspended (edge 6) is legal, and Transition's own default branch leaves
// Truth untouched on that edge — a credential problem suspends routing
// without ever fabricating a truth this probe attempt did not earn.
//
// A rejection from RecordAttempt is logged, never returned as this
// function's own error — the probe attempt itself already succeeded (the
// run row above is real evidence), so there is nothing to retry or warn the
// caller about beyond what the log line already says.
func (t *qualificationTick) probeOneCapability(ctx context.Context, c capabilityProbeCandidate) error {
	attempts, err := t.probeRuns.CountAttempts(ctx, c.OfferingOperationID)
	if err != nil {
		return fmt.Errorf("count probe attempts: %w", err)
	}
	attempts++ // this attempt

	runID := t.newID()
	if err := t.probeRuns.Start(ctx, storage.ProbeRunParams{
		ID: runID, OfferingOperationID: c.OfferingOperationID, AccountID: c.AccountID, ProviderID: c.ProviderID,
		Operation: string(c.Operation), Class: intelligence.ProbeStandard,
		Allocations: probeEstimateAllocations(c.Operation), StartedAt: t.now(),
	}); err != nil {
		return fmt.Errorf("start probe run: %w", err)
	}
	// Defaults to terminal_failure, mirroring ProbeHandler.runProbe's own
	// choice: a guard refusal or transport error below never reaches the
	// line that overwrites this, so the run is honestly recorded as failed
	// rather than left "running" forever.
	runExecution := intelligence.ProbeTerminalFailure
	defer func() {
		_ = t.probeRuns.Finish(context.WithoutCancel(ctx), runID, runExecution, t.now())
	}()

	// InFlightProbes must not count this run's own just-written row — see
	// inFlightExcluding's doc comment (probe.go) for why a self-counting
	// reader would make every probe refuse itself under the default cap of 1.
	guard, err := intelligence.NewProbeGuard(t.probeGuardPolicy, t.probeReserver, t.probeRuns,
		inFlightExcluding{runs: t.probeRuns, excludeRun: runID}, t.probeRuns, t.now)
	if err != nil {
		return fmt.Errorf("build probe guard: %w", err)
	}
	cp, err := intelligence.NewCapabilityProbe(t.probeTransport, guard, t.now)
	if err != nil {
		return fmt.Errorf("build capability probe: %w", err)
	}

	report, err := cp.Run(ctx, intelligence.ProbeRequest{
		AccountID: c.AccountID, ProviderID: c.ProviderID, ProviderModelID: c.ProviderModelID,
		OfferingOperationID: c.OfferingOperationID, Operation: c.Operation,
	})
	if err != nil {
		// A guard refusal (cap/cooldown/concurrency) or a transport error:
		// nothing was learned about the capability, so nothing is recorded
		// beyond the terminal_failure run row Finish already writes.
		return fmt.Errorf("run capability probe: %w", err)
	}

	runExecution = report.Outcome.Execution
	if _, err := t.driver.RecordAttempt(ctx, c.OfferingOperationID, report.Outcome, attempts); err != nil {
		t.log.Info("model qualification: capability probe recorded a run, but the certification transition was rejected (expected for an already-certified capability unless the outcome is a genuine terminal failure — see probeOneCapability's own doc comment)",
			observability.String("offering_operation_id", c.OfferingOperationID),
			observability.String("operation", string(c.Operation)),
			observability.Err(err),
		)
	}
	return nil
}

// BuildQualificationTick constructs the automatic-qualification sweep the
// boot scheduler runs. Its own composition root, mirroring
// BuildTokenRefreshTick/BuildAccountMaintenanceTick: it builds the SAME
// production benchmarkStreamFn NewBenchmarkHandler builds
// (buildBenchmarkStreamFn, composed from the shared provider registry +
// credential repo/service), so the tick executes real streamed inference
// through the identical dispatch path the owner-triggered endpoint used to,
// never a second dispatcher. now defaults to time.Now.
//
// Task 3 additionally builds the SAME capability-probe composition
// ControlMux wires for the callerless POST /offerings/{id}/probe endpoint
// (controlmux.go:293-308 for the transport maps, :403 for
// probeTransportAdapter itself) — independently, exactly like
// buildBenchmarkStreamFn above is independently composed rather than shared
// as one instance with NewBenchmarkHandler's own build.
func BuildQualificationTick(db *storage.DB, kr *secrets.Keyring, now func() time.Time) (func(context.Context) error, error) {
	if now == nil {
		now = time.Now
	}

	reg := newProviderRegistry()
	credentialRepo := storage.NewAccountCredentialRepo(db)
	credentialService := application.NewCredentialService(credentialRepo, kr, now)
	stream := buildBenchmarkStreamFn(reg, credentialRepo, credentialService)

	// probeTransports/probeBaseURLs: a DATA lookup (never a slug switch) from
	// provider id to the execution.InferenceTransport that serves it, and the
	// full base URL that transport needs — the identical filter
	// ControlMux's own construction applies (a provider absent from
	// liveProviderBaseURLs, or unregistered, or whose declared transport kind
	// has no wired implementation, is simply absent from both maps: fail
	// closed, never a fabricated capability).
	probeHTTPClient := &http.Client{Timeout: execution.DefaultOpenAICompatibleTimeout}
	probeImpls := liveTransportImpls(probeHTTPClient, reg)
	probeTransports := make(map[string]execution.InferenceTransport)
	probeBaseURLs := make(map[string]string)
	for id, base := range liveProviderBaseURLs() {
		def, registered := reg.Definition(id)
		if !registered {
			continue
		}
		impl, wired := probeImpls[execution.TransportType(def.Transport)]
		if !wired {
			continue
		}
		probeTransports[string(id)] = impl
		probeBaseURLs[string(id)] = base
	}
	probeTransport := newProbeTransportAdapter(probeTransports, probeBaseURLs, credentialRepo, credentialService)

	probeRuns := storage.NewProbeRunRepo(db, now, intelligence.DefaultProbeSafetyPolicy().ContextProbeCooldown)
	probeReserver := newProbeReserverAdapter(storage.NewQuotaReservationRepo(db, now))
	certRepo := storage.NewCertificationRepo(db, now)
	certAuditor := newCertificationAuditorAdapter(newAuditEmitter(db, observability.Default()))
	certDriver, err := intelligence.NewCertificationDriver(certRepo, certAuditor, intelligence.DefaultProbeRetryBudget, now)
	if err != nil {
		return nil, fmt.Errorf("httpapi: build qualification tick: certification driver: %w", err)
	}

	tick := &qualificationTick{
		catalog: storage.NewCatalogRepo(db),
		runs:    storage.NewBenchmarkRunRepo(db, now),
		stream:  stream,
		newID:   newOAuthTransactionID,
		now:     now,
		log:     observability.Default(),

		db:            db,
		probeRuns:     probeRuns,
		probeReserver: probeReserver,
		// DECISION (task-3 brief, step 4 — the two concurrency limits
		// disagree because the usability sweep and the probe stack share no
		// limiter): keep intelligence.DefaultProbeSafetyPolicy()'s
		// MaxInFlightPerProvider at its default of 1, unchanged, rather than
		// raising it to match usabilityProbeMaxConcurrency (4).
		// usabilityProbeMaxConcurrency exists because verifyAccountChatUsability
		// dispatches several chat probes for ONE account IN PARALLEL
		// (usability_account.go's worker-pool wave loop). This tick never does
		// that for capability probes: probeCapabilities above drives
		// dueCapabilityProbes' candidates through a single sequential `for`
		// loop, one attempt at a time, regardless of how many distinct
		// providers or accounts they span. So there is never more than one
		// capability probe in flight at any instant across the whole tick,
		// and a per-provider cap of 1 never actually constrains anything here
		// — it is already looser than what this tick's own dispatch shape
		// produces. Raising it would only matter if this dispatch became
		// concurrent, which it deliberately is not; the day it does, the new
		// number belongs in the policy passed here, never in bypassing
		// ProbeGuard itself.
		probeGuardPolicy: intelligence.DefaultProbeSafetyPolicy(),
		probeTransport:   probeTransport,
		driver:           certDriver,
	}
	return tick.Run, nil
}

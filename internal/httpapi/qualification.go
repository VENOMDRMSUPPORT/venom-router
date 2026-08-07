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

// qualificationCapabilityProbeCooldown bounds how soon a capability probe
// that did NOT succeed (inconclusive, rate-limited, timed out, refused, …)
// may be re-attempted for the SAME offering-operation (fix round 1,
// CRITICAL 2b).
//
// DECISION: 1 hour. dueCapabilityProbes' own selection ("no succeeded run
// yet") is, by itself, re-satisfied by an inconclusive/failed attempt on
// EVERY subsequent scheduler round — 30s later, forever, with no natural
// backoff — because the very fact that made it due this round (nothing
// succeeded) is still true afterward. Measured without this cooldown: 10
// scheduler rounds produced 10 attempts against the same never-resolving
// candidate, and 5 rate-limited rounds alone recorded enough probe spend to
// approach intelligence.DefaultProbeSafetyPolicy's PerAccount cap within
// under an hour. One hour cuts that to roughly 24 attempts/day per stuck
// candidate — cheap enough that a genuine transient condition (a rate limit,
// a momentary outage) clears within the same day, while a persistently
// wrong/unreachable fixture no longer has any path to compounding toward an
// account-wide probe lockout. It is intentionally far shorter than
// ContextProbeCooldown's 7-day window: that window exists because a context
// probe is the single most expensive probe in the system (04 §2); a
// capability probe is one fixed, tiny fixture request, so there is no
// matching cost pressure to justify a week-long wait for a fixture that
// might simply need the provider to fix a transient issue.
const qualificationCapabilityProbeCooldown = time.Hour

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
	// driver is the slice of *intelligence.CertificationDriver
	// probeOneCapability needs: RecordAttempt (usability_verify.go's
	// certRecorder already declares this one method; capabilityCertDriver
	// below widens it by exactly one more) for every outcome except a
	// definitive negative, and Suspend (edge 6, certified -> suspended) for
	// exactly that case — fix round 2, finding 1; see probeOneCapability's
	// own doc comment for why the two read as one policy with Important 4's
	// terminal-failure suspend, not two.
	driver capabilityCertDriver

	// discovery is consulted ONLY by the context-probe write-back
	// (probeOneContextWindow, task 4) — every other pass in this file never
	// touches it. nativeContextWriter (probe.go) is reused verbatim rather
	// than declaring a second identical interface; ControlMux's own
	// ProbeHandler already wires the same *storage.DiscoveryRepo through it.
	discovery nativeContextWriter
	// contextProbeGuardPolicy is the safety policy every context-window probe
	// attempt (task 4) is admitted against. It is a SEPARATE value from
	// probeGuardPolicy above, differing in exactly one field
	// (ExpensiveProbesEnabled) — see BuildQualificationTick's own assignment
	// for why flipping it globally would be a needless widening, and
	// probeOneContextWindow's doc comment for why this is "through the
	// policy, per probe", never a bypass of ProbeGuard itself.
	contextProbeGuardPolicy intelligence.ProbeSafetyPolicy
	// probeContext is the seam probeOneContextWindow calls for each candidate
	// dueContextProbes selects: run the actual measurement and report the
	// extracted limit, or (0, false) when nothing was learned (a refusal, a
	// transport failure, or a definitive/inconclusive result with no
	// positive ladder hit — RungNoSignal). The production implementation
	// (runContextProbe) builds intelligence.ContextProbe through
	// intelligence.ProbeGuard exactly like probeOneCapability builds
	// CapabilityProbe; a test replaces this whole seam (withContextProbe)
	// with a deterministic fake, since dueContextProbes' SELECTION is what
	// this file's own tests exercise — the extraction ladder itself is
	// already unit-tested in intelligence/contextprobe_test.go.
	probeContext func(ctx context.Context, c contextProbeCandidate) (limit int, ok bool)
}

// capabilityCertDriver is the two-method slice of *intelligence.
// CertificationDriver probeOneCapability needs. It is declared here rather
// than widening usability_verify.go's certRecorder in place: that interface
// is also used by the (unrelated) chat-usability path, which never needs
// Suspend, and growing it there would widen what every caller of THAT path
// must satisfy for a method only this file's capability-probe path calls.
type capabilityCertDriver interface {
	RecordAttempt(ctx context.Context, offeringOperationID string, outcome intelligence.ProbeOutcome, attempts int) (models.Certification, error)
	Suspend(ctx context.Context, offeringOperationID string, reason intelligence.SuspensionReason) (models.Certification, error)
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

	// Task 4: measure the context window for offerings no catalog covers.
	// Independent of both passes above (own selection, own guard policy, own
	// write target — models.native_context_tokens, never quality_rating or a
	// certification row) — its own failure is likewise logged, never fatal.
	t.probeContextWindows(ctx)

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
// whose account+offering are LIVE (Important 5, fix round 1: aligned with
// dueModels' own basis — CatalogRepo.ListOfferings' LiveOnly conditions,
// catalog.go:122-140 — so a disconnected/unhealthy/reauthenticating account
// or a withdrawn offering is never probed, exactly as it is never
// benchmarked), and which has no probe_runs row that SUCCEEDED at or after
// the CURRENT certification was earned — a direct query against
// offering_operations/certifications/account_model_offerings/accounts/
// probe_runs (none of t.catalog's existing methods is shaped for this
// join), in deterministic (offering_operation_id ASC) order.
//
// Deliberately NOT reused from LiveOnly: the certified+supported CHAT
// requirement. dueModels needs it because chat IS the thing it measures
// (there is nothing to benchmark without a working chat path). This pass
// measures tools/structured_output/vision, each independently declared and
// independently certifiable — an account whose chat verification has not
// landed yet (or never will, on a chat-less offering) can still genuinely
// support tools, and there is no reason to block probing it on an
// unrelated capability's own state.
//
// The `pr.finished_at >= c.certified_at` clause is Important 3's fix, fix
// round 1: it is the EXACT negation of the fact
// intelligence/readmodel.go's provenance derivation
// (storage.ProbeRunRepo.SucceededOfferingOperationIDs,
// models.go:collectOfferingOperationThresholds) reads to decide "probed".
// Without it, a certification that EXPIRES and is re-certified from a bare
// declaration (probe_recertify's DefaultRecertifyTTL, or
// ListNonChatOperationsToCertify's own re-certify-from-declaration path)
// keeps its OLD succeeded run forever satisfying "a succeeded run exists",
// so this tick would never select it again — even though the read model
// itself has already stopped counting that stale run and silently fallen
// back to rendering "declared". Matching the read model's own threshold
// exactly is what makes this tick eventually heal that gap instead of
// leaving it unresolvable.
//
// Every matching row is returned unbounded here; probeCapabilities applies
// qualificationCapabilityProbeCap and logs what it defers, mirroring
// dueModels/Run's own cap-after-select shape.
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
		JOIN account_model_offerings amo
		  ON amo.account_id = oo.account_id AND amo.provider_model_id = oo.provider_model_id
		WHERE c.status = 'certified' AND c.capability_truth = 'supported'
		  AND oo.operation IN (` + strings.Join(placeholders, ",") + `)
		  AND amo.availability = 'available'
		  AND EXISTS (
		    SELECT 1 FROM accounts a
		    WHERE a.id = oo.account_id
		      AND a.connection_state = 'connected'
		      AND a.health_state = 'healthy'
		      AND a.reauth_in_progress = 0
		  )
		  AND NOT EXISTS (
		    SELECT 1 FROM probe_runs pr
		    WHERE pr.offering_operation_id = oo.id AND pr.execution = 'succeeded'
		      AND pr.finished_at >= c.certified_at
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
// records its outcome.
//
// FIX ROUND 1 — CRITICAL 2(a): guard.Admit runs BEFORE any probe_runs/
// probe_run_costs row is written, by calling cp.Run (which calls Admit
// internally) FIRST and only starting bookkeeping once it returns
// successfully. The earlier version of this method called
// t.probeRuns.Start (which also inserts probe_run_costs) before the guard
// even existed, so a REFUSED attempt still permanently consumed the
// account's rolling probe-spend budget — measured, over repeated refused/
// rate-limited rounds, to compound toward intelligence.ProbeGuard's
// PerAccount cap and lock out the ENTIRE probe subsystem for that account
// (context probes and the manual endpoint included). A refused attempt
// never reaches the transport and therefore learns nothing, so recording
// it as spend — or as evidence at all — would be dishonest; this mirrors
// the usability sweep's own existing rule that a pacer refusal is SKIPPED,
// never verdicted (usability_account.go).
//
// Because no row exists for this attempt until AFTER cp.Run returns, the
// guard's in-flight reader is t.probeRuns directly — no self-counting risk,
// so the inFlightExcluding wrapper probe.go's ProbeHandler needs (its OWN
// row exists ACROSS the transport call) is not needed here. The other side
// of that trade (fix round 2, item 3's follow-up): because the row is
// written AFTER the transport call rather than across it, THIS tick's own
// in-flight attempt is invisible to MaxInFlightPerProvider for the whole
// duration it is actually in flight. probeCapabilities' own sequential
// `for` loop and the per-round cap already bound how far that can go from
// THIS tick's side (never more than one at a time, never more than
// qualificationCapabilityProbeCap per round) — but a CONCURRENT manual probe
// (POST /offerings/{id}/probe, probe.go) against the same provider would not
// see this one and would not be held back by it.
//
// FIX ROUND 1 — CRITICAL 2(b): the guard is also given a capability-probe
// cooldown (ProbeGuard.WithCapabilityCooldown, backed by
// t.probeRuns.CapabilityProbeCooldownUntil). Without it, dueCapabilityProbes'
// "no succeeded run yet" selection re-picks the SAME inconclusive/failed
// candidate every 30s scheduler round forever — measured at 10 attempts
// over 10 rounds with zero backoff. qualificationCapabilityProbeCooldown's
// own doc comment states the chosen duration and why.
//
// FIX ROUND 1 — CRITICAL 1: runExecution, the value handed to
// probeRuns.Finish, is gated on report.Outcome.Truth, not on
// report.Outcome.Execution. intelligence.ClassifyProbeSignal marks a
// semantic rejection (a provider explicitly disowning the capability via
// one of the three reliableUnsupportedCodes) as Execution=ProbeSucceeded —
// correct for THAT package's own execution/truth separation, since the
// ATTEMPT itself completed legitimately. But this tick's OWN probe_runs
// bookkeeping is exactly what intelligence/readmodel.go's provenance
// derivation (ProbeRunRepo.SucceededOfferingOperationIDs) reads as "a
// succeeded run exists ⇒ probed", with zero visibility into Truth.
// Recording a semantic rejection as 'succeeded' would render a
// PROVEN-ABSENT capability as "measured supported" — strictly worse than
// the pre-task-3 "declared" hedge it replaces, because the owner would then
// route real traffic into a capability the provider just rejected. So a
// definitive-but-unsupported outcome is recorded as 'inconclusive' instead:
// never proof of presence. (The certification's own truth stays stuck at
// "supported" regardless — see the RecordAttempt discussion below; this
// local bookkeeping decision only stops the READ MODEL from lying about
// it, it cannot fix the certification-lifecycle gap by itself.)
//
// c is selected by dueCapabilityProbes ONLY when its certification is
// already certified/supported — declared, in this task's own vocabulary,
// on a LIVE account/offering (Important 5, fix round 1). That is exactly
// why calling t.driver.RecordAttempt(outcome) for the outcomes below is
// safe, and not a second interpretation of the outcome:
// models.Certification.Transition's frozen legal-transition table
// (models/certification.go) has no certified -> certified edge and no
// certified -> probing edge. So:
//   - a genuine capability_response (Definitive, Truth=Supported) targets
//     certified -> certified, which Transition rejects as illegal;
//   - any non-definitive/rate-limited/timeout/malformed outcome targets
//     certified -> probing, which Transition rejects the same way;
//   - either way RecordAttempt returns a wrapped
//     models.ErrIllegalCertificationTransition and the certification is
//     returned byte-for-byte unchanged (CompareAndSwap is never reached) —
//     the exact "never downgrade on missing evidence" invariant this task
//     exists to prove, enforced by the SAME frozen table every other
//     certification edge already goes through, not a bespoke check added
//     here.
//
// Two outcomes are deliberately NOT handed to RecordAttempt at all, because
// certified -> certified is a dead end for them and leaving it at that would
// be worse than doing nothing — this tick calls t.driver.Suspend instead,
// which drives the ONE other edge (6, certified -> suspended) that IS legal
// from the state dueCapabilityProbes' own selection guarantees c is in.
// Read together, both are one policy — "certified/supported is not a fact
// this tick may quietly keep asserting once evidence contradicts it, and
// the only lever available is suspend, never a truth this probe did not
// earn" — not two unrelated accidents:
//   - a genuine terminal failure (401/403, credential-blocked): Important 4,
//     fix round 1, pinned by its own test. Transition's default branch
//     leaves Truth untouched on this edge.
//   - a genuine DEFINITIVE NEGATIVE (Truth=Unsupported — one of the three
//     reliableUnsupportedCodes fired): fix round 2, finding 1, pinned by
//     its own test. Left as a plain RecordAttempt call, this is the
//     TREADMILL fix round 2 measured directly: certified -> certified is
//     illegal regardless of outcome.Truth, so the certification stays
//     certified/supported (wrong routing — the provider just told us this
//     capability is absent) AND the row is never removed from
//     dueCapabilityProbes' selection (no succeeded run can ever land), so
//     it is re-attempted, re-paid, every cooldown window, forever. Suspending
//     it fixes BOTH: models.Routable(state, truth) requires state=certified,
//     so a suspended row stops being routable immediately, and
//     dueCapabilityProbes' own `c.status = 'certified'` filter stops
//     selecting it, so the treadmill terminates. Uses
//     intelligence.SuspensionCapabilityContradiction — the vocabulary's own
//     name for exactly "evidence contradicted what was certified".
//
// What this does NOT do: SuspensionCapabilityContradiction and
// SuspensionCredentialBlocked (used by the terminal-failure branch above)
// both land on the IDENTICAL certified -> suspended STATE — "certified" only
// distinguishes the reason in the audit trail, never in the routing-facing
// (state, truth) pair a client actually reads. A suspended capability's
// chip cannot tell "the provider disowned it" apart from "the credential
// was rejected" without reading the audit log. That is a limitation of the
// frozen six-state/seven-reason vocabulary this tick inherits
// (models/certification.go, certdriver.go's SuspensionReason set) — it is
// not something to work around here.
//
// A rejection from RecordAttempt (or a failure from Suspend) is logged,
// never returned as this function's own error — the probe attempt itself
// already succeeded (the run row is real evidence), so there is nothing to
// retry or warn the caller about beyond what the log line already says.
func (t *qualificationTick) probeOneCapability(ctx context.Context, c capabilityProbeCandidate) error {
	guard, err := intelligence.NewProbeGuard(t.probeGuardPolicy, t.probeReserver, t.probeRuns, t.probeRuns, t.probeRuns, t.now)
	if err != nil {
		return fmt.Errorf("build probe guard: %w", err)
	}
	guard = guard.WithCapabilityCooldown(t.probeRuns)

	cp, err := intelligence.NewCapabilityProbe(t.probeTransport, guard, t.now)
	if err != nil {
		return fmt.Errorf("build capability probe: %w", err)
	}

	startedAt := t.now()
	report, err := cp.Run(ctx, intelligence.ProbeRequest{
		AccountID: c.AccountID, ProviderID: c.ProviderID, ProviderModelID: c.ProviderModelID,
		OfferingOperationID: c.OfferingOperationID, Operation: c.Operation,
	})
	if err != nil {
		// A guard refusal (cap/cooldown/concurrency) or a transport error:
		// nothing was learned and nothing was spent — see CRITICAL 2(a)
		// above. Neither a probe_runs row nor a probe_run_costs row is ever
		// written for this attempt.
		return fmt.Errorf("run capability probe: %w", err)
	}

	// CRITICAL 1 — see this method's own doc comment.
	runExecution := report.Outcome.Execution
	if runExecution == intelligence.ProbeSucceeded && report.Outcome.Truth != models.TruthSupported {
		runExecution = intelligence.ProbeInconclusive
	}

	attempts, err := t.probeRuns.CountAttempts(ctx, c.OfferingOperationID)
	if err != nil {
		return fmt.Errorf("count probe attempts: %w", err)
	}
	attempts++ // this attempt

	runID := t.newID()
	if err := t.probeRuns.Start(ctx, storage.ProbeRunParams{
		ID: runID, OfferingOperationID: c.OfferingOperationID, AccountID: c.AccountID, ProviderID: c.ProviderID,
		Operation: string(c.Operation), Class: intelligence.ProbeStandard,
		Allocations: probeEstimateAllocations(c.Operation), ReservationID: report.ReservationID, StartedAt: startedAt,
	}); err != nil {
		return fmt.Errorf("start probe run: %w", err)
	}
	// FIX ROUND 2, item 3 (non-blocking, addressed): Finish is deferred
	// under a WithoutCancel context and its error swallowed — mirroring
	// ProbeHandler.runProbe's own discipline exactly (probe.go). Start has
	// already claimed the row; a cancellation between here and Finish must
	// not leave it stuck at 'running' until ReclaimStale eventually sweeps
	// it. The window this closes is narrow (a couple of local DB
	// statements, not a whole transport call — Start now runs AFTER the
	// transport call, per CRITICAL 2(a) above), but matching the manual
	// path's own choice costs nothing.
	defer func() {
		_ = t.probeRuns.Finish(context.WithoutCancel(ctx), runID, runExecution, t.now())
	}()

	// FIX ROUND 2, finding 1 — see this method's own doc comment for why
	// this is the SAME policy as Important 4's terminal-failure suspend,
	// not a second one.
	if report.Outcome.Definitive && report.Outcome.Truth == models.TruthUnsupported {
		if _, err := t.driver.Suspend(ctx, c.OfferingOperationID, intelligence.SuspensionCapabilityContradiction); err != nil {
			t.log.Warn("model qualification: suspending a capability after a definitive negative failed",
				observability.String("offering_operation_id", c.OfferingOperationID),
				observability.String("operation", string(c.Operation)),
				observability.Err(err),
			)
		}
		return nil
	}

	if _, err := t.driver.RecordAttempt(ctx, c.OfferingOperationID, report.Outcome, attempts); err != nil {
		t.log.Info("model qualification: capability probe recorded a run, but the certification transition was rejected (expected for an already-certified capability unless the outcome is a genuine terminal failure — see probeOneCapability's own doc comment)",
			observability.String("offering_operation_id", c.OfferingOperationID),
			observability.String("operation", string(c.Operation)),
			observability.Err(err),
		)
	}
	return nil
}

// --- Task 4: measure the context window for offerings no catalog covers ---

// qualificationContextProbeCap bounds how many context-window probes ONE
// tick round will run.
//
// DECISION: 1. This is the single most expensive probe in the system —
// intelligence.ContextProbeInputTokens declares 3,000,000 input tokens by
// construction (it works by sending a deliberately oversized request and
// reading the real limit out of the provider's rejection), against a
// DefaultProbeSafetyPolicy PerAccount rolling cap of 20,000,000 input
// tokens per 24h window. qualificationPerRoundCap/
// qualificationCapabilityProbeCap both use 5 because THEIR fixtures are
// tiny; reusing 5 here would let a single 30-second round alone reserve up
// to 15,000,000 of that account's entire daily probe-input-token
// allowance, crowding out every other probe (capability AND any other
// context probe) for the rest of the day. 1 keeps one round's fan-out to
// exactly the bounded catch-up this project's other caps already document,
// while a large backlog of never-measured, genuinely-uncatalogued models
// still drains — one per round — rather than never draining (cap 0) or
// never protecting the account's daily budget (an unbounded cap).
const qualificationContextProbeCap = 1

// qualificationContextProbeCooldown bounds how soon THIS tick may re-select
// the SAME offering for a context probe, regardless of the previous
// attempt's outcome — mirroring qualificationCapabilityProbeCooldown's own
// "keyed off the last ATTEMPT, not the last SUCCESS" reasoning (see that
// constant's doc comment for why a succeeded-only cooldown never fires for
// the case it exists to bound).
//
// This is deliberately NOT the same protection as
// intelligence.ProbeGuard's own context-probe cooldown
// (ProbeCooldownReader.ProbeCooldownUntil). That gate is keyed off the most
// recent SUCCEEDED context-window run ONLY (storage.ProbeRunRepo.
// ProbeCooldownUntil's own doc comment: "only a succeeded context probe
// ever sets the cooldown... an infra failure must remain re-attemptable
// under the probe's own retry budget rather than being locked out for a
// week"). A model that never resolves (rate-limited, inconclusive,
// terminal failure) sets it NEVER, so dueContextProbes' own "effective
// context is nil" selection would otherwise re-pick the identical
// candidate on every subsequent 30s round forever — exactly task-3's own
// CRITICAL 2(b) gap (a capability that never succeeds, re-probed with no
// backoff), reproduced here for a probe an order of magnitude more
// expensive.
//
// Nor can ProbeGuard.WithCapabilityCooldown (task-3's fix for that exact
// gap) be reused for this: its OWN gate inside Admit is scoped to
// RequiredWitness's three capability operations only
// (intelligence/probesafety.go: "if _, err := RequiredWitness(req.
// Operation); err == nil") — RequiredWitness(OperationContextWindow)
// returns ErrNoCapabilityFixture (capabilityprobe.go:110-112), so wiring
// that guard method for a context-window request would compile, run, and
// silently do NOTHING. This was verified by reading ProbeGuard.Admit's own
// source rather than assumed — the task-4 brief explicitly calls out that
// the previous task's assumption ("the capability path already has a
// cooldown") turned out to be false, and the same discipline applies here.
//
// dueContextProbes therefore enforces this backoff itself, directly in its
// own selection query, against ANY prior probe_runs attempt regardless of
// execution. The DURATION reused is the SAME
// intelligence.DefaultProbeSafetyPolicy().ContextProbeCooldown value
// Admit's own succeeded-only gate already uses for the resolved case (04
// §2's "7-day cooldown") — one canonical 7-day duration, never a second,
// independently-chosen magic number.
var qualificationContextProbeCooldown = intelligence.DefaultProbeSafetyPolicy().ContextProbeCooldown

// contextProbeCandidate is one live-offering model whose EFFECTIVE context
// (models.EffectiveContext: the canonical native fact merged with the
// offering's provider-declared cap) is unknown — there is no catalog fact
// and no prior verified probe result to read a context badge from.
//
// ChatOfferingOperationID anchors this probe's admission/bookkeeping
// identity. context_window has no offering_operations row of its own for a
// genuinely uncatalogued model: providers.OperationsFromFacts only emits
// "context_window" when models.dev declared a Context value, so an
// uncatalogued model (the cline-pass/qwen3.8-max case this task exists for)
// never gets that row from discovery, and probe_runs.offering_operation_id
// is a REAL foreign key (00010_probe_runs.sql: "REFERENCES
// offering_operations(id)", enforced — this project runs with
// foreign_keys=ON, storage.go:35) that cannot reference a row that does not
// exist. Every LiveOnly candidate is, BY CONSTRUCTION, guaranteed to have a
// certified+supported "chat" offering_operations row (that is exactly what
// LiveOnly's own EXISTS clause requires, catalog.go:131-140) — reusing that
// row's id as the context probe's admission/bookkeeping anchor is therefore
// a deliberate, always-satisfiable choice, not a workaround: the "operation"
// COLUMN on every probe_runs/cooldown row this probe ever writes is still
// the honest "context_window" literal throughout, only the FK anchor is
// borrowed from the offering's other, guaranteed-to-exist operation row.
type contextProbeCandidate struct {
	ModelID                 string
	AccountID               string
	ProviderID              string
	ProviderModelID         string
	ChatOfferingOperationID string
}

// chatOfferingOperationIDOf finds row's own "chat" CatalogOperationRow id —
// see contextProbeCandidate's own doc comment for why this is always
// present for a LiveOnly row (defensive only: this is never expected to
// return false in production).
func chatOfferingOperationIDOf(row storage.CatalogOfferingRow) (string, bool) {
	for _, op := range row.Operations {
		if op.Operation == string(models.OperationChat) {
			return op.ID, true
		}
	}
	return "", false
}

// hasRecentContextProbeAttempt reports whether ANY probe_runs row exists
// for (offeringOperationID, operation=context_window) started at or after
// since, regardless of execution — the raw query backing
// qualificationContextProbeCooldown's own selection-level backoff (see that
// constant's doc comment for why this cannot be expressed through
// ProbeGuard.WithCapabilityCooldown).
func (t *qualificationTick) hasRecentContextProbeAttempt(ctx context.Context, offeringOperationID string, since time.Time) (bool, error) {
	var n int
	if err := t.db.Conn().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM probe_runs WHERE offering_operation_id = ? AND operation = ? AND started_at >= ?`,
		offeringOperationID, string(models.OperationContextWindow), since.Unix(),
	).Scan(&n); err != nil {
		return false, fmt.Errorf("httpapi: model qualification: check recent context probe attempt: %w", err)
	}
	return n > 0, nil
}

// dueContextProbes resolves every live-chat-offering model (the SAME
// LiveOnly basis dueModels uses, walking ListOfferings' pagination exactly
// like dueModels does — never assuming the whole fleet fits in memory or
// one page) whose EFFECTIVE context is unknown
// (models.EffectiveContext(row.NativeContextTokens, row.ContextLength)
// returns a nil limit) and which has no recent context-probe attempt of any
// kind (qualificationContextProbeCooldown).
//
// A model already covered by EITHER source — models.dev's provider-declared
// cap (ContextLength) OR a prior verified probe (NativeContextTokens) — is
// never selected: re-probing it would spend this probe's declared
// 3,000,000 input tokens to learn a number the catalog, or an earlier
// attempt, already gave for free. This is the exact rule the task-4 brief's
// own test pins: a catalogued model must never be re-measured.
func (t *qualificationTick) dueContextProbes(ctx context.Context) ([]contextProbeCandidate, error) {
	seen := make(map[string]bool)
	var due []contextProbeCandidate
	since := t.now().Add(-qualificationContextProbeCooldown)

	cursor := ""
	for {
		rows, next, err := t.catalog.ListOfferings(ctx, storage.CatalogListParams{LiveOnly: true, Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if seen[row.ModelID] {
				continue
			}
			seen[row.ModelID] = true

			if limit, _ := models.EffectiveContext(row.NativeContextTokens, row.ContextLength); limit != nil {
				continue // the catalog, or an earlier probe, already answered this
			}

			chatOpID, ok := chatOfferingOperationIDOf(row)
			if !ok {
				continue // defensive only — see contextProbeCandidate's own doc comment
			}

			recent, err := t.hasRecentContextProbeAttempt(ctx, chatOpID, since)
			if err != nil {
				return nil, err
			}
			if recent {
				continue
			}

			due = append(due, contextProbeCandidate{
				ModelID:                 row.ModelID,
				AccountID:               row.AccountID,
				ProviderID:              row.ProviderID,
				ProviderModelID:         row.ProviderModelID,
				ChatOfferingOperationID: chatOpID,
			})
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return due, nil
}

// probeContextWindows runs one round of task 4: for every candidate
// dueContextProbes selects (up to qualificationContextProbeCap, deferring
// and logging the rest — mirroring qualificationPerRoundCap/
// qualificationCapabilityProbeCap's own "a cap nobody can see reads as
// everything was measured" discipline), run the context probe and persist
// a positive extraction.
//
// A LIST-phase failure is logged and this round's context probing is
// simply skipped — never fatal to the tick as a whole, exactly like
// probeCapabilities/dueModels' own failures abort only their own pass. A
// per-candidate failure is likewise logged and the round continues with
// the rest.
func (t *qualificationTick) probeContextWindows(ctx context.Context) {
	due, err := t.dueContextProbes(ctx)
	if err != nil {
		t.log.Warn("model qualification: listing due context probes failed, skipping this round's context probing",
			observability.Err(err))
		return
	}
	if len(due) == 0 {
		return
	}

	probe := due
	if len(probe) > qualificationContextProbeCap {
		probe = due[:qualificationContextProbeCap]
	}
	if skipped := len(due) - len(probe); skipped > 0 {
		skippedIDs := make([]string, 0, skipped)
		for _, c := range due[len(probe):] {
			skippedIDs = append(skippedIDs, c.ModelID)
		}
		// Logged, never silent — see qualificationPerRoundCap's own doc
		// comment for why a cap nobody can see is a dishonesty this project
		// keeps fixing.
		t.log.Info("model qualification: context-probe per-round cap reached, deferring the rest to a later round",
			observability.Int("cap", qualificationContextProbeCap),
			observability.Int("due", len(due)),
			observability.Int("deferred", skipped),
			observability.String("deferred_model_ids", fmt.Sprintf("%v", skippedIDs)),
		)
	}

	for _, c := range probe {
		if err := t.probeOneContextWindow(ctx, c); err != nil {
			t.log.Warn("model qualification: probing one context window failed, continuing with the rest of the round",
				observability.String("model_id", c.ModelID),
				observability.Err(err),
			)
		}
	}
}

// probeOneContextWindow runs the context probe (via t.probeContext, the
// seam production wires to runContextProbe below and tests replace via
// withContextProbe) and, ONLY on a positive extraction, persists it through
// the existing DiscoveryRepo.SetNativeContextTokens write-back — the SAME
// write probe.go's manual endpoint already performs after a genuine
// extraction, never a second interpretation of what counts as "learned
// something": (0, false) from t.probeContext (a refusal, a transport
// failure, or RungNoSignal) writes nothing at all.
func (t *qualificationTick) probeOneContextWindow(ctx context.Context, c contextProbeCandidate) error {
	limit, ok := t.probeContext(ctx, c)
	if !ok {
		return nil
	}
	if err := t.discovery.SetNativeContextTokens(ctx, c.ModelID, limit); err != nil {
		return fmt.Errorf("set native context tokens: %w", err)
	}
	return nil
}

// runContextProbe is probeContext's production implementation: build a
// fresh intelligence.ContextProbe (guard + probe) per attempt — mirroring
// probeOneCapability's own per-call construction exactly — admit it through
// intelligence.ProbeGuard, and report the extracted limit.
//
// t.contextProbeGuardPolicy is used here instead of t.probeGuardPolicy:
// this is the ONE place in the whole tick that ever admits a
// Class=ProbeExpensive request (intelligence.ContextProbe.Run sets Class:
// ProbeExpensive internally), and ProbeGuard.Admit refuses every expensive
// probe unless policy.ExpensiveProbesEnabled is true
// (intelligence/probesafety.go: "expensive probe class is disabled").
// Flipping that ONE field, on a policy used by NOTHING else in this file,
// is 04 §2's "enable expensive probes for this narrow path through the
// policy, per probe" — every OTHER gate Admit runs (the per-probe/
// per-account cost caps sized exactly for this probe's 3,000,000 declared
// input tokens, DefaultProbeSafetyPolicy's own MaxInFlightPerProvider=1
// concurrency cap, and CRITICALLY the unmodified 7-day ContextProbeCooldown)
// still applies in full — this is never a bypass of ProbeGuard, only a
// widening of what CLASS of probe its existing opt-in gate lets through.
// probeGuardPolicy (capability probes) is left an entirely separate value
// so this opt-in can never leak onto a probe class that has no business
// needing it — capability probes always run Class=ProbeStandard
// (probeEstimateAllocations' fixed tiny fixture estimate), so
// ExpensiveProbesEnabled genuinely never matters to them today, but a
// future capability-probe change should have to decide that for itself
// rather than inherit it silently from this one.
//
// Start/Finish bookkeeping follows the SAME "Start strictly after a
// successful Admit" ordering task-3's CRITICAL 2(a) fix established (see
// probeOneCapability's own doc comment): cp.Run calls guard.Admit
// internally, and probeRuns.Start is only ever reached once cp.Run has
// already returned without error — a refused attempt (missing opt-in, a
// cap, the cooldown, concurrency) never writes a probe_runs row and never
// records spend.
//
// rules=nil (NewContextProbe's third argument) is deliberate, not a
// forgotten parameter: a repo-wide search of this codebase — and, for
// additional evidence, of its retired TypeScript predecessor's own
// context-probe.server.ts — found no intelligence.ContextLimitRule value
// defined anywhere. Every provider this project has ever observed rejects
// an oversized request in a shape rung 2 (the OpenAI phrase, gated on
// provider_code=="context_length_exceeded") or rung 4 (the generic
// keyword-proximity search) already reads; inventing a provider-specific
// regex for a rejection shape nobody has actually observed would be
// exactly the kind of guessed fact 04 §2 forbids. Rung 3 (provider regex)
// therefore stays a documented no-op, exactly as it already was at
// probe.go:526 — this task revives the CALLER, not a rung with nothing to
// revive.
func (t *qualificationTick) runContextProbe(ctx context.Context, c contextProbeCandidate) (int, bool) {
	guard, err := intelligence.NewProbeGuard(t.contextProbeGuardPolicy, t.probeReserver, t.probeRuns, t.probeRuns, t.probeRuns, t.now)
	if err != nil {
		t.log.Warn("model qualification: context probe guard construction failed", observability.Err(err))
		return 0, false
	}

	cp, err := intelligence.NewContextProbe(t.probeTransport, guard, nil, t.now)
	if err != nil {
		t.log.Warn("model qualification: context probe construction failed", observability.Err(err))
		return 0, false
	}

	startedAt := t.now()
	report, err := cp.Run(ctx, intelligence.ProbeRequest{
		AccountID: c.AccountID, ProviderID: c.ProviderID, ProviderModelID: c.ProviderModelID,
		OfferingOperationID: c.ChatOfferingOperationID, Operation: models.OperationContextWindow,
	})
	if err != nil {
		// A guard refusal (opt-in/cap/cooldown/concurrency) or a transport
		// error: nothing was learned and nothing was spent. Neither a
		// probe_runs row nor a probe_run_costs row is ever written for this
		// attempt — see this method's own doc comment.
		return 0, false
	}

	runID := t.newID()
	if err := t.probeRuns.Start(ctx, storage.ProbeRunParams{
		ID: runID, OfferingOperationID: c.ChatOfferingOperationID, AccountID: c.AccountID, ProviderID: c.ProviderID,
		Operation: string(models.OperationContextWindow), Class: intelligence.ProbeExpensive,
		Allocations: probeEstimateAllocations(models.OperationContextWindow), ReservationID: report.ReservationID, StartedAt: startedAt,
	}); err != nil {
		t.log.Warn("model qualification: starting context probe run failed", observability.Err(err))
		return 0, false
	}
	// Finish is deferred under a WithoutCancel context and its error
	// swallowed — mirroring probeOneCapability's/ProbeHandler.runProbe's own
	// discipline exactly: Start has already claimed the row, and a
	// cancellation before Finish must not leave it stuck at 'running' until
	// ReclaimStale eventually sweeps it.
	defer func() {
		_ = t.probeRuns.Finish(context.WithoutCancel(ctx), runID, report.Outcome.Execution, t.now())
	}()

	if report.Limit == nil {
		// Refused nothing, spent the reservation, learned nothing definite —
		// RungNoSignal, or a definitive/inconclusive outcome with no ladder
		// hit. Never a guessed number.
		return 0, false
	}
	return *report.Limit, true
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

	probeRuns := storage.NewProbeRunRepo(db, now, intelligence.DefaultProbeSafetyPolicy().ContextProbeCooldown).
		WithCapabilityProbeCooldown(qualificationCapabilityProbeCooldown)
	probeReserver := newProbeReserverAdapter(storage.NewQuotaReservationRepo(db, now))
	certRepo := storage.NewCertificationRepo(db, now)
	certAuditor := newCertificationAuditorAdapter(newAuditEmitter(db, observability.Default()))
	certDriver, err := intelligence.NewCertificationDriver(certRepo, certAuditor, intelligence.DefaultProbeRetryBudget, now)
	if err != nil {
		return nil, fmt.Errorf("httpapi: build qualification tick: certification driver: %w", err)
	}

	// contextProbeGuardPolicy: DefaultProbeSafetyPolicy() with ONLY
	// ExpensiveProbesEnabled flipped true — see runContextProbe's own doc
	// comment for why this single field, on a policy value used by nothing
	// else in this file, is 04 §2's "enable expensive probes for this
	// narrow path through the policy, per probe" and not a bypass of
	// ProbeGuard: every other gate (cost caps, concurrency, and the
	// 7-day ContextProbeCooldown) is left at its conservative default.
	contextProbeGuardPolicy := intelligence.DefaultProbeSafetyPolicy()
	contextProbeGuardPolicy.ExpensiveProbesEnabled = true

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

		discovery:               storage.NewDiscoveryRepo(db, newOAuthTransactionID),
		contextProbeGuardPolicy: contextProbeGuardPolicy,
	}
	// Task 4: probeContext is a bound method value, assigned after
	// construction (it needs tick itself as its receiver) rather than
	// inline in the struct literal above — the SAME reason
	// buildQualificationTickForTest's own default wiring below does the
	// identical assignment for its test builds.
	tick.probeContext = tick.runContextProbe
	return tick.Run, nil
}

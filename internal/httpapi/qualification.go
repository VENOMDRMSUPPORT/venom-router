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
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/application"
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

// BuildQualificationTick constructs the automatic-qualification sweep the
// boot scheduler runs. Its own composition root, mirroring
// BuildTokenRefreshTick/BuildAccountMaintenanceTick: it builds the SAME
// production benchmarkStreamFn NewBenchmarkHandler builds
// (buildBenchmarkStreamFn, composed from the shared provider registry +
// credential repo/service), so the tick executes real streamed inference
// through the identical dispatch path the owner-triggered endpoint used to,
// never a second dispatcher. now defaults to time.Now.
func BuildQualificationTick(db *storage.DB, kr *secrets.Keyring, now func() time.Time) (func(context.Context) error, error) {
	if now == nil {
		now = time.Now
	}

	reg := newProviderRegistry()
	credentialRepo := storage.NewAccountCredentialRepo(db)
	credentialService := application.NewCredentialService(credentialRepo, kr, now)
	stream := buildBenchmarkStreamFn(reg, credentialRepo, credentialService)

	tick := &qualificationTick{
		catalog: storage.NewCatalogRepo(db),
		runs:    storage.NewBenchmarkRunRepo(db, now),
		stream:  stream,
		newID:   newOAuthTransactionID,
		now:     now,
		log:     observability.Default(),
	}
	return tick.Run, nil
}

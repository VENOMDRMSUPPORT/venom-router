package httpapi

// benchmark_engine.go is the local-benchmark MEASUREMENT ENGINE (Plan 3 of
// the local-benchmark-rating design, Task 3): pure suite logic — sequencing,
// medians, and the documented rating formula — behind an injected stream
// seam (benchmarkStreamFn). It runs no inference itself and touches no
// database; the production stream implementation is Task 4
// (benchmark_stream.go) and the job wiring is Task 5 (benchmark.go).
//
// --- Step 0: the production seam decision -----------------------------------
//
// The brief requires reading chatcompletions.go end-to-end first and picking
// the narrowest production seam that (a) resolves a route for a model and
// (b) executes a STREAMED completion through the wire codecs, then shaping
// benchmarkStreamFn so Task 4 can implement it against that seam.
//
// DECISION: benchmarkStreamFn's production implementation (Task 4) drives
// execution.Dispatcher.Stream directly — the same *execution.Dispatcher built
// by BuildInferenceDispatcher(reg, impls) in buildChatCompletionsHandler
// (chatcompletions.go:87/102) and composed from liveTransportImpls
// (chatcompletions.go:36-42). It is NOT an httptest.Server loopback over
// ChatCompletionsHandler.
//
// Why the dispatcher and not the handler loopback:
//   - The benchmark already knows its exact target: accountID, providerID,
//     providerModelID are given directly (this file's signature). Nothing
//     needs re-deciding.
//   - ChatCompletionsHandler.ServeChat/run (chatcompletions.go:201-375) exists
//     to pick ONE candidate out of many for a TIER via the full fallback
//     loop: it resolves a *snapshot* of every eligible account/offering
//     (h.engine.Snapshot.Build, chatcompletions.go:288) and runs
//     routing.RunFallbackLoop (chatcompletions.go:323) to choose a
//     provider — it could legitimately choose a DIFFERENT account or
//     provider than the one asked for, or fail over across several. That is
//     the OPPOSITE of what a benchmark of one specific offering needs: a
//     benchmark that silently fell back to another account/provider would
//     measure the wrong thing while reporting it as the requested one.
//   - execution.Dispatcher.Stream(ctx, route, req) (dispatcher.go) is exactly
//     the point past candidate selection where a SINGLE resolved route
//     (execution.ResolvedRoute — Provider, AccountID, Credential, ModelID,
//     BaseURL, WireSchema; types.go:33-46) is executed through the
//     catalog-declared transport and its wire codec
//     (execution.InferenceTransport.Stream, dispatched via
//     liveTransportImpls' per-transport-type table). That is the narrowest
//     point that still satisfies (a) — the route is "resolved" by the
//     benchmark caller constructing a ResolvedRoute from the given
//     account/provider/model plus a credential lookup (the same
//     credentialLeaser seam the usability verifier already uses,
//     usability_assembler.go:26-28) — and (b) — Dispatcher.Stream is the
//     real streamed dispatch, through the real transport, through the real
//     wire codec, never a hand-rolled parallel HTTP client.
//   - Task 4's mutation proof is therefore: swap the production
//     *execution.Dispatcher wired into the benchmark's constructor for a nil
//     or no-op one and show the composition-root-driven test goes red — the
//     same mutation discipline already used for BuildInferenceDispatcher
//     elsewhere in this package.
//
// This file only needs benchmarkStreamFn's SHAPE to support that plan: a
// closure over (ctx, accountID, providerID, providerModelID, prompt,
// maxTokens) returning one (benchmarkSample, error) — which is exactly what
// Task 4 will implement by resolving credentials for accountID, building an
// execution.ResolvedRoute, calling dispatcher.Stream, and measuring the
// resulting execution.Chunk stream (first non-empty Delta = TTFT; chunk-rate
// after that = tokens/sec). Nothing about that shape is httptest-specific,
// so the decision does not foreclose Task 4 falling back to a loopback
// server IF a future obstacle makes the dispatcher route unreachable — but
// today's reading of chatcompletions.go shows no such obstacle, so the
// dispatcher is the documented plan.

import (
	"context"
	"math"
	"sort"
	"time"
)

// benchmarkFixturePrompt and benchmarkFixtureMaxTokens are the FIXED
// measurement fixture (task brief): every request in a suite run — for every
// offering ever benchmarked — sends the identical prompt and max_tokens, so
// runs are comparable to each other and to themselves over time. Neither is
// ever taken from caller input.
const (
	benchmarkFixturePrompt    = "Write the numbers one to twenty as words, separated by spaces."
	benchmarkFixtureMaxTokens = 64
)

// benchmarkDefaultRequests is the suite size Task 5's job wiring uses when
// the owner does not override it. runBenchmarkSuite itself takes requests as
// a parameter (never assumes it) — this constant exists so the one call site
// that decides "how many" has a single named default rather than a bare 3.
const benchmarkDefaultRequests = 3

// benchmarkSample is one streamed completion's measurement.
type benchmarkSample struct {
	OK           bool
	TTFT         time.Duration // first content token latency
	TokensPerSec float64       // output tokens / (last-token time - first-token time); 0 if <2 tokens
}

// benchmarkStreamFn runs ONE streamed completion for the offering and reports
// the sample. Injected; the production impl (Task 4) drives the real
// dispatch path documented above. A non-nil error means the call could not
// be completed at all (transport/network failure) — distinct from
// benchmarkSample.OK==false, which means the call completed but the provider
// reported (or the wire codec observed) a failed completion.
type benchmarkStreamFn func(ctx context.Context, accountID, providerID, providerModelID, prompt string, maxTokens int) (benchmarkSample, error)

// benchmarkAggregate is one suite run's result.
type benchmarkAggregate struct {
	Requests, Successes int
	TTFTMillis          *int64   // median of successful samples
	TokensPerSec        *float64 // median of successful samples
	Rating              *float64 // nil unless every request succeeded
}

// runBenchmarkSuite runs `requests` sequential streamed completions against
// the fixed fixture (benchmarkFixturePrompt / benchmarkFixtureMaxTokens),
// aggregates the successful samples, and derives a rating.
//
// Sequential, never parallel: latency measurement integrity would be
// destroyed by contending requests sharing the account's connection/rate
// limits, so this loop calls `stream` and waits for its result before
// starting the next one. There is no goroutine here — the sequencing is the
// simple fact that each call blocks the next.
//
// A request counts as successful only when stream returns (sample, nil) AND
// sample.OK is true. A transport error (non-nil err) is a failed request —
// the sample's own fields are ignored in that case, and it never panics: an
// injected stream returning an error is exactly the case this suite exists
// to survive (one flaky provider round trip must not lose the other N-1
// measurements). requests<=0 is honored as "run nothing" — Requests and
// Successes are both 0 and every pointer field stays nil, never a
// divide-by-zero.
//
// Rating is populated ONLY when every request in the suite succeeded
// (Successes == Requests, and Requests > 0) — a benchmark with any failure
// measures reliability as much as speed, and reporting a rating anyway would
// hide that. When populated, it is localBenchmarkRating evaluated at the
// SAME medians reported on the aggregate (never recomputed from raw samples
// a second time), so the two fields are always consistent with each other.
func runBenchmarkSuite(ctx context.Context, stream benchmarkStreamFn, accountID, providerID, providerModelID string, requests int) benchmarkAggregate {
	agg := benchmarkAggregate{Requests: requests}
	if requests <= 0 {
		agg.Requests = 0
		return agg
	}

	ttfts := make([]int64, 0, requests)
	tps := make([]float64, 0, requests)

	for i := 0; i < requests; i++ {
		sample, err := stream(ctx, accountID, providerID, providerModelID, benchmarkFixturePrompt, benchmarkFixtureMaxTokens)
		if err != nil || !sample.OK {
			continue
		}
		agg.Successes++
		ttfts = append(ttfts, sample.TTFT.Milliseconds())
		tps = append(tps, sample.TokensPerSec)
	}

	if len(ttfts) == 0 {
		return agg
	}

	medianTTFT := medianInt64(ttfts)
	medianTPS := medianFloat64(tps)
	agg.TTFTMillis = &medianTTFT
	agg.TokensPerSec = &medianTPS

	if agg.Successes == agg.Requests {
		rating := localBenchmarkRating(time.Duration(medianTTFT)*time.Millisecond, medianTPS)
		agg.Rating = &rating
	}
	return agg
}

// localBenchmarkRating maps measured performance to a 0..1 score. It is a
// LOCAL heuristic (relative performance on the owner's own account), not a
// universal quality metric: 50% generation speed (saturating at 80 tok/s),
// 50% first-token latency (1.0 at 0ms, 0.0 at >=2000ms). Only defined when
// every request in the suite succeeded (enforced by runBenchmarkSuite, not
// by this function — this function is a pure map with no knowledge of
// suite-level success).
func localBenchmarkRating(ttft time.Duration, tokensPerSec float64) float64 {
	speed := math.Min(tokensPerSec/80.0, 1.0)
	latency := math.Max(0, 1.0-float64(ttft.Milliseconds())/2000.0)
	return 0.5*speed + 0.5*latency
}

// medianInt64 returns the median of values, which is mutated into sorted
// order by this call (callers here always pass a private slice built for
// this one use, so that is never observable).
//
// Median definition (documented once, applies to medianFloat64 too): for an
// ODD count, the middle element after sorting. For an EVEN count, the MEAN
// of the two middle elements — not the lower of the two — which is the
// standard statistical definition and the one TestMedianInt64/
// TestMedianFloat64 pin. Called only with a non-empty slice; an empty slice
// is a caller bug (runBenchmarkSuite never calls it with zero successes).
func medianInt64(values []int64) int64 {
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	n := len(values)
	mid := n / 2
	if n%2 == 1 {
		return values[mid]
	}
	// Even count: mean of the two middle values. Using float64 division and
	// rounding to the nearest millisecond (rather than truncating integer
	// division) so e.g. two samples of 100ms and 301ms report 201ms, not
	// 200ms — the rounding never systematically biases the reported latency
	// downward.
	return int64(math.Round(float64(values[mid-1]+values[mid]) / 2.0))
}

// medianFloat64 is medianInt64's float64 counterpart (tokens/sec). See
// medianInt64's doc comment for the shared median definition.
func medianFloat64(values []float64) float64 {
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	n := len(values)
	mid := n / 2
	if n%2 == 1 {
		return values[mid]
	}
	return (values[mid-1] + values[mid]) / 2.0
}

package httpapi

// benchmark_engine_test.go exercises the local-benchmark measurement engine
// (Plan 3 of the local-benchmark-rating design, Task 3): the pure suite
// runner (runBenchmarkSuite) behind the injected benchmarkStreamFn seam, and
// the documented localBenchmarkRating formula. No production dispatch runs
// here — the production benchmarkStreamFn implementation is Task 4's concern
// (see benchmark_engine.go's Step-0 seam doc comment).

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// --- formula table tests ----------------------------------------------------

// TestLocalBenchmarkRating pins the formula's literal test cases from the
// task brief. These are NOT tautological: the expected values are computed
// by hand from the documented weights (50% speed saturating at 80tok/s, 50%
// latency reaching 0 at >=2000ms), not copied from the implementation.
func TestLocalBenchmarkRating(t *testing.T) {
	cases := []struct {
		name         string
		ttft         time.Duration
		tokensPerSec float64
		want         float64
	}{
		{"perfect: instant TTFT, saturating speed", 0, 80, 1.0},
		{"worst: 2s TTFT, zero speed", 2000 * time.Millisecond, 0, 0.0},
		{"midpoint: 1s TTFT, half speed", 1000 * time.Millisecond, 40, 0.5},
		{"fast + speed above saturation clamps to 1.0 for the speed half", 500 * time.Millisecond, 160, 0.875},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := localBenchmarkRating(c.ttft, c.tokensPerSec)
			if !floatsClose(got, c.want, 1e-9) {
				t.Fatalf("localBenchmarkRating(%v, %v) = %v, want %v", c.ttft, c.tokensPerSec, got, c.want)
			}
		})
	}
}

// TestLocalBenchmarkRating_SpeedSaturatesBeyond80 proves the saturation
// clamp is real: tokensPerSec far beyond 80 must not push the score past
// what 80tok/s itself produces at the same TTFT.
func TestLocalBenchmarkRating_SpeedSaturatesBeyond80(t *testing.T) {
	at80 := localBenchmarkRating(0, 80)
	at800 := localBenchmarkRating(0, 800)
	if !floatsClose(at80, at800, 1e-9) {
		t.Fatalf("speed did not saturate: rating(80tps)=%v rating(800tps)=%v", at80, at800)
	}
}

// TestLocalBenchmarkRating_LatencyFloorsAtZeroBeyond2000ms proves the
// latency term cannot go negative for a TTFT far past the 2000ms floor.
func TestLocalBenchmarkRating_LatencyFloorsAtZeroBeyond2000ms(t *testing.T) {
	at2s := localBenchmarkRating(2000*time.Millisecond, 0)
	at20s := localBenchmarkRating(20000*time.Millisecond, 0)
	if !floatsClose(at2s, at20s, 1e-9) {
		t.Fatalf("latency did not floor at zero: rating(2000ms)=%v rating(20000ms)=%v", at2s, at20s)
	}
	if at2s != 0.0 {
		t.Fatalf("rating(2000ms, 0tps) = %v, want 0.0", at2s)
	}
}

func floatsClose(a, b, eps float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= eps
}

// --- median helper tests -----------------------------------------------------
//
// Median definition (documented on the helpers in benchmark_engine.go): for
// an EVEN count of samples, the median is the MEAN of the two middle values
// after sorting (not the lower of the two) — the common statistical
// definition. These tests pin that choice explicitly, both for the int64
// (TTFT millis) and float64 (tokens/sec) helpers.

func TestMedianInt64(t *testing.T) {
	cases := []struct {
		name   string
		values []int64
		want   int64
	}{
		{"single value", []int64{42}, 42},
		{"odd count, unsorted input", []int64{300, 100, 200}, 200},
		{"even count, mean of middle two", []int64{100, 300}, 200},
		{"even count, four values, unsorted", []int64{400, 100, 300, 200}, 250},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := medianInt64(c.values)
			if got != c.want {
				t.Fatalf("medianInt64(%v) = %v, want %v", c.values, got, c.want)
			}
		})
	}
}

func TestMedianFloat64(t *testing.T) {
	cases := []struct {
		name   string
		values []float64
		want   float64
	}{
		{"single value", []float64{42.5}, 42.5},
		{"odd count, unsorted input", []float64{30, 10, 20}, 20},
		{"even count, mean of middle two", []float64{10, 30}, 20},
		{"even count, four values, unsorted", []float64{40, 10, 30, 20}, 25},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := medianFloat64(c.values)
			if !floatsClose(got, c.want, 1e-9) {
				t.Fatalf("medianFloat64(%v) = %v, want %v", c.values, got, c.want)
			}
		})
	}
}

// --- suite tests -------------------------------------------------------------

const (
	suiteAccountID       = "acct-bench-1"
	suiteProviderID      = "prov-bench-1"
	suiteProviderModelID = "vendor/great-model"
)

// scriptedCall records the arguments one stream invocation was made with.
type scriptedCall struct {
	accountID, providerID, providerModelID, prompt string
	maxTokens                                      int
}

// scriptedStep is one scripted response: either a sample or a transport
// error (never both — mirrors the real seam's (sample, error) contract).
type scriptedStep struct {
	sample benchmarkSample
	err    error
}

// scriptedStream builds a benchmarkStreamFn that replays steps in order and
// asserts NO overlapping invocation ever occurs (an in-flight flag guarded by
// a mutex) — this is what proves runBenchmarkSuite drives requests
// sequentially rather than fanning them out. It also records every call's
// arguments so a test can pin the fixed fixture (prompt/maxTokens) and the
// pass-through identifiers.
func scriptedStream(t *testing.T, steps []scriptedStep) (fn benchmarkStreamFn, calls *[]scriptedCall) {
	t.Helper()
	var mu sync.Mutex
	inFlight := false
	idx := 0
	recorded := make([]scriptedCall, 0, len(steps))

	fn = func(ctx context.Context, accountID, providerID, providerModelID, prompt string, maxTokens int) (benchmarkSample, error) {
		mu.Lock()
		if inFlight {
			mu.Unlock()
			t.Fatal("runBenchmarkSuite invoked the stream seam concurrently — requests must be sequential")
		}
		inFlight = true
		if idx >= len(steps) {
			inFlight = false
			mu.Unlock()
			t.Fatalf("scriptedStream invoked more times (%d) than scripted (%d)", idx+1, len(steps))
			return benchmarkSample{}, nil
		}
		step := steps[idx]
		idx++
		recorded = append(recorded, scriptedCall{accountID, providerID, providerModelID, prompt, maxTokens})
		mu.Unlock()

		defer func() {
			mu.Lock()
			inFlight = false
			mu.Unlock()
		}()
		return step.sample, step.err
	}
	return fn, &recorded
}

// TestRunBenchmarkSuite_AllSuccess: 3 OK samples with known TTFT/tokens-per-sec
// values must produce the exact medians and a non-nil rating computed from
// those medians via localBenchmarkRating. It also pins the fixed fixture
// (prompt + maxTokens) and the pass-through account/provider/model ids.
func TestRunBenchmarkSuite_AllSuccess(t *testing.T) {
	steps := []scriptedStep{
		{sample: benchmarkSample{OK: true, TTFT: 100 * time.Millisecond, TokensPerSec: 10}},
		{sample: benchmarkSample{OK: true, TTFT: 200 * time.Millisecond, TokensPerSec: 20}},
		{sample: benchmarkSample{OK: true, TTFT: 300 * time.Millisecond, TokensPerSec: 30}},
	}
	stream, calls := scriptedStream(t, steps)

	got := runBenchmarkSuite(context.Background(), stream, suiteAccountID, suiteProviderID, suiteProviderModelID, 3)

	if got.Requests != 3 {
		t.Fatalf("Requests = %d, want 3", got.Requests)
	}
	if got.Successes != 3 {
		t.Fatalf("Successes = %d, want 3", got.Successes)
	}
	if got.TTFTMillis == nil || *got.TTFTMillis != 200 {
		t.Fatalf("TTFTMillis = %v, want 200", got.TTFTMillis)
	}
	if got.TokensPerSec == nil || !floatsClose(*got.TokensPerSec, 20, 1e-9) {
		t.Fatalf("TokensPerSec = %v, want 20", got.TokensPerSec)
	}
	wantRating := localBenchmarkRating(200*time.Millisecond, 20)
	if got.Rating == nil {
		t.Fatal("Rating = nil, want non-nil (every request succeeded)")
	}
	if !floatsClose(*got.Rating, wantRating, 1e-9) {
		t.Fatalf("Rating = %v, want %v", *got.Rating, wantRating)
	}

	if len(*calls) != 3 {
		t.Fatalf("stream invoked %d times, want 3", len(*calls))
	}
	for i, c := range *calls {
		if c.accountID != suiteAccountID || c.providerID != suiteProviderID || c.providerModelID != suiteProviderModelID {
			t.Fatalf("call %d: identifiers = %+v, want account=%s provider=%s model=%s", i, c, suiteAccountID, suiteProviderID, suiteProviderModelID)
		}
		if c.prompt != "Write the numbers one to twenty as words, separated by spaces." {
			t.Fatalf("call %d: prompt = %q, want the fixed fixture prompt", i, c.prompt)
		}
		if c.maxTokens != 64 {
			t.Fatalf("call %d: maxTokens = %d, want 64", i, c.maxTokens)
		}
	}
}

// TestRunBenchmarkSuite_OneFailedSample: 2 OK + 1 failed (OK:false, err:nil —
// a provider-reported failure, not a transport error) must report
// Successes:2, Rating:nil (NOT every request succeeded), and medians derived
// from ONLY the 2 successful samples.
func TestRunBenchmarkSuite_OneFailedSample(t *testing.T) {
	steps := []scriptedStep{
		{sample: benchmarkSample{OK: true, TTFT: 100 * time.Millisecond, TokensPerSec: 10}},
		{sample: benchmarkSample{OK: false}},
		{sample: benchmarkSample{OK: true, TTFT: 300 * time.Millisecond, TokensPerSec: 30}},
	}
	stream, _ := scriptedStream(t, steps)

	got := runBenchmarkSuite(context.Background(), stream, suiteAccountID, suiteProviderID, suiteProviderModelID, 3)

	if got.Requests != 3 {
		t.Fatalf("Requests = %d, want 3", got.Requests)
	}
	if got.Successes != 2 {
		t.Fatalf("Successes = %d, want 2", got.Successes)
	}
	if got.Rating != nil {
		t.Fatalf("Rating = %v, want nil (one request failed)", *got.Rating)
	}
	if got.TTFTMillis == nil || *got.TTFTMillis != 200 {
		t.Fatalf("TTFTMillis = %v, want 200 (mean of the two successful samples' 100ms/300ms)", got.TTFTMillis)
	}
	if got.TokensPerSec == nil || !floatsClose(*got.TokensPerSec, 20, 1e-9) {
		t.Fatalf("TokensPerSec = %v, want 20 (mean of the two successful samples' 10/30)", got.TokensPerSec)
	}
}

// TestRunBenchmarkSuite_TransportError: a transport error from the fake
// (err != nil) counts as a failed request — never a panic, never included in
// the medians, and the sample's own OK/fields are ignored when err != nil.
func TestRunBenchmarkSuite_TransportError(t *testing.T) {
	steps := []scriptedStep{
		{sample: benchmarkSample{OK: true, TTFT: 100 * time.Millisecond, TokensPerSec: 10}},
		// A transport error: even though the zero-value sample happens to have
		// OK:false already, the point is the engine must key off the error,
		// never the sample contents, once err != nil.
		{sample: benchmarkSample{}, err: errors.New("dial tcp: connection reset")},
		{sample: benchmarkSample{OK: true, TTFT: 300 * time.Millisecond, TokensPerSec: 30}},
	}
	stream, _ := scriptedStream(t, steps)

	var got benchmarkAggregate
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("runBenchmarkSuite panicked on a transport error: %v", r)
			}
		}()
		got = runBenchmarkSuite(context.Background(), stream, suiteAccountID, suiteProviderID, suiteProviderModelID, 3)
	}()

	if got.Requests != 3 {
		t.Fatalf("Requests = %d, want 3", got.Requests)
	}
	if got.Successes != 2 {
		t.Fatalf("Successes = %d, want 2", got.Successes)
	}
	if got.Rating != nil {
		t.Fatalf("Rating = %v, want nil (one request errored)", *got.Rating)
	}
	if got.TTFTMillis == nil || *got.TTFTMillis != 200 {
		t.Fatalf("TTFTMillis = %v, want 200", got.TTFTMillis)
	}
}

// TestRunBenchmarkSuite_AllFailed: every request fails (mix of OK:false and
// transport error) -> Successes:0, both medians nil (nothing to take a
// median of), Rating nil.
func TestRunBenchmarkSuite_AllFailed(t *testing.T) {
	steps := []scriptedStep{
		{sample: benchmarkSample{OK: false}},
		{err: errors.New("boom")},
		{sample: benchmarkSample{OK: false}},
	}
	stream, _ := scriptedStream(t, steps)

	got := runBenchmarkSuite(context.Background(), stream, suiteAccountID, suiteProviderID, suiteProviderModelID, 3)

	if got.Requests != 3 || got.Successes != 0 {
		t.Fatalf("Requests/Successes = %d/%d, want 3/0", got.Requests, got.Successes)
	}
	if got.TTFTMillis != nil {
		t.Fatalf("TTFTMillis = %v, want nil", *got.TTFTMillis)
	}
	if got.TokensPerSec != nil {
		t.Fatalf("TokensPerSec = %v, want nil", *got.TokensPerSec)
	}
	if got.Rating != nil {
		t.Fatalf("Rating = %v, want nil", *got.Rating)
	}
}

// TestRunBenchmarkSuite_Sequential drives 5 scripted calls through a fake
// that fatals on any overlapping invocation, proving the loop never launches
// concurrent stream calls. It also pins call ORDER by having each step
// return a distinguishable sample and checking they arrive in order.
func TestRunBenchmarkSuite_Sequential(t *testing.T) {
	steps := []scriptedStep{
		{sample: benchmarkSample{OK: true, TTFT: 1 * time.Millisecond, TokensPerSec: 1}},
		{sample: benchmarkSample{OK: true, TTFT: 2 * time.Millisecond, TokensPerSec: 2}},
		{sample: benchmarkSample{OK: true, TTFT: 3 * time.Millisecond, TokensPerSec: 3}},
		{sample: benchmarkSample{OK: true, TTFT: 4 * time.Millisecond, TokensPerSec: 4}},
		{sample: benchmarkSample{OK: true, TTFT: 5 * time.Millisecond, TokensPerSec: 5}},
	}
	stream, calls := scriptedStream(t, steps)

	got := runBenchmarkSuite(context.Background(), stream, suiteAccountID, suiteProviderID, suiteProviderModelID, 5)

	if got.Requests != 5 || got.Successes != 5 {
		t.Fatalf("Requests/Successes = %d/%d, want 5/5", got.Requests, got.Successes)
	}
	if len(*calls) != 5 {
		t.Fatalf("stream invoked %d times, want 5 (sequential, exactly once per request)", len(*calls))
	}
}

// TestRunBenchmarkSuite_ZeroRequests: requests<=0 must not panic or divide by
// zero; it reports an empty, honest aggregate.
func TestRunBenchmarkSuite_ZeroRequests(t *testing.T) {
	stream, calls := scriptedStream(t, nil)

	got := runBenchmarkSuite(context.Background(), stream, suiteAccountID, suiteProviderID, suiteProviderModelID, 0)

	if got.Requests != 0 || got.Successes != 0 {
		t.Fatalf("Requests/Successes = %d/%d, want 0/0", got.Requests, got.Successes)
	}
	if got.TTFTMillis != nil || got.TokensPerSec != nil || got.Rating != nil {
		t.Fatalf("expected all-nil aggregate for zero requests, got %+v", got)
	}
	if len(*calls) != 0 {
		t.Fatalf("stream invoked %d times, want 0", len(*calls))
	}
}

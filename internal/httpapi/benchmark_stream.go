package httpapi

// benchmark_stream.go is Task 4 of the local-benchmark-rating plan: the
// PRODUCTION implementation of benchmarkStreamFn (benchmark_engine.go),
// wired directly onto execution.Dispatcher.Stream per Task 3's Step-0
// decision (benchmark_engine.go's doc comment) — the post-selection seam
// where a single already-resolved execution.ResolvedRoute is executed
// through the catalog-declared transport and its real wire codec, never a
// hand-rolled parallel HTTP client and never the candidate-selecting
// ChatCompletionsHandler.ServeChat/run path (which could legitimately choose
// a DIFFERENT account/provider than the one the benchmark was asked to
// measure).
//
// --- Route + request construction: reused helpers, not hand-rolled -------
//
// This mirrors probeTransportAdapter.Probe (probeadapters.go:179-236) — the
// existing production code that already does exactly this shape of work
// (lease a credential, build a ResolvedRoute + NormalizedRequest for a given
// account/provider/model, call the transport once) for the certification
// probe path:
//   - ResolvedRoute{Provider, AccountID, Credential, ModelID, BaseURL} is
//     built the same way probeadapters.go:199-205 builds it — the plaintext
//     key populates Credential.Value ONLY inside the credentialLeaser.Use
//     callback (probeadapters.go:198/212; usability_assembler.go:26-28's same
//     seam), so it never escapes that scope.
//   - WireSchema is deliberately left unset here, exactly as probeadapters.go
//     leaves it unset: schemaStampingTransport (transportresolver.go:59-95)
//     stamps it from the registry on every call for native_oauth routes
//     before the inner transport ever sees the route, and it is already
//     wrapped into liveTransportImpls' native_oauth entry
//     (chatcompletions.go:39) — the SAME table both this benchmark's
//     dispatcher and the request path are composed from. Duplicating that
//     stamping here would be exactly the "hand-rolled wire knowledge" the
//     task brief forbids; the *execution.Dispatcher passed into
//     newBenchmarkStreamFn already carries it.
//   - The active credential lookup mirrors activeCredentialIDFor
//     (discovery.go:141-157) exactly, redeclared here as an interface
//     (benchmarkCredentialLister) so a test fake can drive it without a real
//     *storage.AccountCredentialRepo/DB — the SAME "duplicated here rather
//     than exported" precedent discovery.go's own comment already documents
//     for AccountsHandler.activeCredentialID.
//
// --- TTFT / TokensPerSec mechanics -----------------------------------------
//
// TTFT: elapsed time from the call's own start to the first execution.Chunk
// carrying a non-empty Delta.
//
// KNOWN GAP (documented, not hidden): the brief's "reasoning_content counts
// as content" instruction (the big-pickle rule) cannot be honored at this
// seam. execution.Chunk (types.go:178-182) carries exactly {Delta, Done,
// Err} — nothing else — and EVERY Stream implementation in internal/execution
// (openaicompat.go, nativeoauth.go, geminiwire.go, anthropicwire.go) was read
// before writing this file: none of them parses a reasoning_content field
// into Chunk, or into any field, at all. Reasoning tokens are invisible past
// the wire-codec boundary today, not merely uncounted here. Plumbing that
// through would mean adding a field to execution.Chunk and updating every
// codec's parser — production wire-parsing work this task does not own (the
// brief's "do NOT hand-roll wire knowledge" applies with equal force to
// inventing a field that doesn't exist as to inventing a parser). Delta is
// therefore the only content this function can observe; TTFT for a
// reasoning-heavy model will read slower than the model's true first token
// until execution.Chunk grows that field. Flagged in the Task 4 report for
// visibility, not silently absorbed.
//
// TokensPerSec: for the identical reason, no Chunk in this codebase carries a
// terminal usage/completion_tokens block (also confirmed by reading every
// Stream implementation). This is therefore ALWAYS the chunk-count
// approximation the brief allows as the fallback: treat each content-bearing
// chunk as one token, and divide (content chunks - 1) by the wall-clock time
// between the first and the last content chunk (the "-1" because the first
// chunk defines the zero point the interval is measured from — there is no
// elapsed time before it to attribute a token-rate to). 0 when fewer than 2
// content chunks arrived, per benchmarkSample's own doc comment.
//
// --- OK / err semantics (binding, from the brief) --------------------------
//
// Every transport in this codebase documents the same pre/post-first-byte
// error contract (see e.g. openaicompat.go:319-321): a non-2xx response, or a
// failure to even establish the connection, is returned DIRECTLY from
// Stream() before any channel exists; any failure AFTER the first byte (a
// malformed frame, an idle-gap/first-byte timeout, a truncated stream missing
// its completion marker, or an abrupt connection drop) arrives as
// Chunk{Err: ...} through the channel, never a silent close. That existing
// contract maps directly onto the brief's required semantics:
//   - dispatcher.Stream itself returning an error (pre-first-byte: non-2xx
//     provider rejection, or a connect failure) => benchmarkSample{OK:false},
//     err=nil — a failed sample, not a transport error the caller must
//     retry-classify.
//   - A Chunk.Err arriving through the channel (post-first-byte: a mid-stream
//     drop, timeout, or truncation) => err != nil, sample discarded.
//   - A credential lookup/lease failure is an infrastructure fault, not a
//     provider verdict, so it is always returned as a non-nil err.

import (
	"context"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/execution"
)

// benchmarkDispatcher is the narrow seam newBenchmarkStreamFn drives: exactly
// execution.Dispatcher.Stream's signature (dispatcher.go:77). Declared as an
// interface so tests can substitute a fake (or a real
// *execution.OpenAICompatibleTransport, whose Stream method has this exact
// shape) without needing a full provider registry; *execution.Dispatcher
// satisfies this structurally, with no adapter required.
type benchmarkDispatcher interface {
	Stream(ctx context.Context, route execution.ResolvedRoute, req execution.NormalizedRequest) (<-chan execution.Chunk, error)
}

// benchmarkCredentialLister is the narrow read newBenchmarkStreamFn needs to
// turn an accountID into the credential id credentialLeaser.Use
// (usability_assembler.go:47-49) expects. It mirrors activeCredentialIDFor
// (discovery.go:141-157) exactly — duplicated as an interface here (that
// function's own doc comment already establishes this precedent: "duplicated
// here rather than exported... since both are small, independent lookups
// over the same repo") so a fake can drive it in tests without a real
// *storage.AccountCredentialRepo/DB. *storage.AccountCredentialRepo satisfies
// this structurally via its existing ListForAccount method.
type benchmarkCredentialLister interface {
	ListForAccount(ctx context.Context, accountID string) ([]domain.Credential, error)
}

// benchmarkActiveCredentialID returns the id of accountID's one active
// credential, or ok=false if it has none or the lookup itself failed — the
// same logic as activeCredentialIDFor (discovery.go:146-157), redeclared
// against the benchmarkCredentialLister interface above.
func benchmarkActiveCredentialID(ctx context.Context, credentials benchmarkCredentialLister, accountID string) (string, bool) {
	creds, err := credentials.ListForAccount(ctx, accountID)
	if err != nil {
		return "", false
	}
	for _, c := range creds {
		if c.State == domain.CredentialActive {
			return c.ID, true
		}
	}
	return "", false
}

// newBenchmarkStreamFn builds the production benchmarkStreamFn.
//
//   - dispatcher drives the real streamed dispatch (production: an
//     *execution.Dispatcher composed from the same production tables the
//     chat-completions request path composes its own from —
//     BuildInferenceDispatcher(reg, liveTransportImpls(...)), per Task 3's
//     Step-0 decision. buildBenchmarkStreamFn builds its own dispatcher
//     value, and benchmark_composition_test.go proves the COMPOSITION is the
//     production one; it does not prove pointer identity with the handler's,
//     so this comment must not claim the same instance).
//   - baseURLFor resolves a provider id to its fully-resolved base URL
//     (production: the same liveProviderBaseURLs()-backed closure
//     buildChatCompletionsHandler builds for EngineDeps.BaseURLFor,
//     chatcompletions.go:106).
//   - credentials looks up an account's active credential id (production:
//     *storage.AccountCredentialRepo).
//   - creds leases the plaintext key for exactly the duration of one call
//     (production: application.CredentialService, via its existing Use
//     method — the same credentialLeaser seam usability_assembler.go uses).
func newBenchmarkStreamFn(
	dispatcher benchmarkDispatcher,
	baseURLFor func(providerID string) string,
	credentials benchmarkCredentialLister,
	creds credentialLeaser,
) benchmarkStreamFn {
	return func(ctx context.Context, accountID, providerID, providerModelID, prompt string, maxTokens int) (benchmarkSample, error) {
		credentialID, ok := benchmarkActiveCredentialID(ctx, credentials, accountID)
		if !ok {
			return benchmarkSample{}, fmt.Errorf("httpapi: benchmark: account %q has no active credential", accountID)
		}

		baseURL := baseURLFor(providerID)
		reqMaxTokens := maxTokens

		var sample benchmarkSample
		var sampleErr error
		leaseErr := creds.Use(ctx, credentialID, func(key []byte) error {
			route := execution.ResolvedRoute{
				Provider:   execution.ProviderID(providerID),
				AccountID:  accountID,
				Credential: execution.StoredCredentials{Value: string(key)},
				ModelID:    providerModelID,
				BaseURL:    baseURL,
			}
			normReq := execution.NormalizedRequest{
				Operation: execution.OperationChat,
				Messages:  []execution.Message{{Role: "user", Content: prompt}},
				Stream:    true,
				MaxTokens: &reqMaxTokens,
			}

			// TTFT is measured from HERE — before dispatch, not after the
			// channel is returned. dispatcher.Stream (e.g.
			// OpenAICompatibleTransport.Stream) blocks on the HTTP round
			// trip up through the response headers before it ever returns a
			// channel, so starting the clock after that call would silently
			// exclude connection setup and any header-flush delay from
			// TTFT — undercounting exactly the latency a real caller
			// experiences.
			start := time.Now()
			ch, dispatchErr := dispatcher.Stream(ctx, route, normReq)
			if dispatchErr != nil {
				// Pre-first-byte boundary: a non-2xx response or a connect
				// failure. Per the brief this is a PROVIDER REJECTION, a
				// failed sample, never a transport error the caller must
				// retry-classify.
				sample = benchmarkSample{OK: false}
				return nil
			}
			sample, sampleErr = measureBenchmarkStream(start, ch)
			return nil
		})
		if leaseErr != nil {
			// A credential lookup/decrypt failure is an infrastructure
			// fault, not a provider verdict — always a transport error, and
			// the sample is discarded (zero value) rather than reused.
			return benchmarkSample{}, leaseErr
		}
		return sample, sampleErr
	}
}

// measureBenchmarkStream drains one benchmark stream to completion, timing
// TTFT from start (captured by the caller BEFORE dispatcher.Stream was
// invoked, so the HTTP round trip up to the response headers counts too) and
// approximating TokensPerSec from content-chunk arrival times. See this
// file's header comment for the documented gap (no reasoning_content, no
// usage block reachable through execution.Chunk) and the chunk-count
// approximation this falls back to.
//
// A Chunk.Err arriving at any point (mid-stream drop, timeout, truncation —
// every transport's own documented contract, never a silent close) is
// returned as a non-nil error immediately, discarding whatever was measured
// so far. A stream that closes having delivered content but never a
// Done=true chunk (which, per that same contract, can only happen alongside
// an Err chunk today) is treated as OK=false rather than a panic or a
// fabricated success.
func measureBenchmarkStream(start time.Time, ch <-chan execution.Chunk) (benchmarkSample, error) {
	var ttft time.Duration
	var firstContentAt, lastContentAt time.Time
	contentChunks := 0
	done := false

	for c := range ch {
		if c.Err != nil {
			return benchmarkSample{}, c.Err
		}
		if c.Delta != "" {
			now := time.Now()
			if contentChunks == 0 {
				ttft = now.Sub(start)
				firstContentAt = now
			}
			lastContentAt = now
			contentChunks++
		}
		if c.Done {
			done = true
		}
	}

	if contentChunks == 0 || !done {
		return benchmarkSample{OK: false}, nil
	}

	var tokensPerSec float64
	if contentChunks >= 2 {
		if elapsed := lastContentAt.Sub(firstContentAt).Seconds(); elapsed > 0 {
			tokensPerSec = float64(contentChunks-1) / elapsed
		}
	}

	return benchmarkSample{OK: true, TTFT: ttft, TokensPerSec: tokensPerSec}, nil
}

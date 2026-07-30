package httpapi

import (
	"context"
	"errors"
	"strings"
	"testing"

	accountsdomain "github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/execution"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/routing"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// --- W3-M4: Reserver translation ------------------------------------------

type fakeReserver struct {
	err error
	res storage.ReserveResult
}

func (f fakeReserver) Reserve(_ context.Context, _ storage.ReserveParams) (storage.ReserveResult, error) {
	return f.res, f.err
}

// TestReserverAdapter_TranslatesRejection proves a storage.ErrReservationRejected
// is wrapped so errors.Is(err, routing.ErrReservationRejected) holds — the
// loop's re-evaluate branch depends on it.
//
// Mutation W3-M4: return the rejection unwrapped → errors.Is fails → RED.
func TestReserverAdapter_TranslatesRejection(t *testing.T) {
	a := &reserverAdapter{repo: fakeReserver{err: storage.ErrReservationRejected}}
	_, err := a.Reserve(context.Background(), routing.ReserveParams{AccountID: "a", RequestID: "r", AttemptID: "att", Allocations: nil})
	if !errors.Is(err, routing.ErrReservationRejected) {
		t.Fatalf("Reserve error = %v, want it to wrap routing.ErrReservationRejected", err)
	}

	// A success passes through with the reservation id.
	ok := &reserverAdapter{repo: fakeReserver{res: storage.ReserveResult{ReservationID: "res-1"}}}
	got, err := ok.Reserve(context.Background(), routing.ReserveParams{AccountID: "a", RequestID: "r", AttemptID: "att"})
	if err != nil || got.ReservationID != "res-1" {
		t.Fatalf("success Reserve = (%+v, %v), want res-1/nil", got, err)
	}
}

// --- W3-M5: streaming StreamStarted boundary ------------------------------

// fakeStreamTransport is an InferenceTransport whose Stream replays a scripted
// chunk sequence (or a pre-first-byte error).
type fakeStreamTransport struct {
	chunks   []execution.Chunk
	preErr   error
	lastAuth string // the credential value it received (Bearer stripped)
	execResp *execution.NormalizedResponse
	execErr  error
}

func (f *fakeStreamTransport) Execute(_ context.Context, route execution.ResolvedRoute, _ execution.NormalizedRequest) (*execution.NormalizedResponse, error) {
	f.lastAuth = route.Credential.Value
	return f.execResp, f.execErr
}

func (f *fakeStreamTransport) Stream(_ context.Context, route execution.ResolvedRoute, _ execution.NormalizedRequest) (<-chan execution.Chunk, error) {
	f.lastAuth = route.Credential.Value
	if f.preErr != nil {
		return nil, f.preErr
	}
	ch := make(chan execution.Chunk, len(f.chunks))
	for _, c := range f.chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

func (f *fakeStreamTransport) Cancel(_ context.Context, _ execution.ResolvedRoute, _ string) error {
	return nil
}
func (f *fakeStreamTransport) NormalizeError(_ error, _ execution.ResolvedRoute) execution.VenomError {
	return execution.VenomError{}
}
func (f *fakeStreamTransport) Failure(_ error, _ execution.ResolvedRoute) execution.TypedFailure {
	return execution.TypedFailure{FailureClass: execution.FailureClassServer, Scope: execution.FailureScopeProvider}
}
func (f *fakeStreamTransport) SupportedCapabilities(_ execution.ResolvedRoute) []execution.Operation {
	return []execution.Operation{execution.OperationChat, execution.OperationStreaming}
}

type fixedResolver struct{ tt execution.TransportType }

func (r fixedResolver) TransportTypeFor(_ execution.ResolvedRoute) (execution.TransportType, error) {
	return r.tt, nil
}

type collectingSink struct{ deltas []string }

func (s *collectingSink) Emit(delta string) error { s.deltas = append(s.deltas, delta); return nil }

func newStreamExecutor(t *testing.T, transport *fakeStreamTransport, sink StreamSink) *executorAdapter {
	t.Helper()
	const tt = execution.TransportTypeOpenAICompatible
	disp := execution.NewDispatcher(fixedResolver{tt: tt}, map[execution.TransportType]execution.InferenceTransport{tt: transport})
	return &executorAdapter{
		dispatcher: disp,
		classify: func(_ execution.ResolvedRoute, err error) execution.TypedFailure {
			return transport.Failure(err, execution.ResolvedRoute{})
		},
		stream:      true,
		sink:        sink,
		inflight:    newInflightCounter(),
		routeHolder: &execution.ResolvedRoute{},
	}
}

// TestExecutor_StreamStartedAfterFirstByte proves StreamStarted is TRUE when a
// failure arrives after a chunk reached the client, and FALSE when the failure
// is pre-first-byte — the P4-WIRE-002 producer.
//
// Mutation W3-M5: never set started=true in runStream → the after-first-byte
// case returns StreamStarted:false → RED.
func TestExecutor_StreamStartedAfterFirstByte(t *testing.T) {
	// A delta reaches the client, THEN the stream errors → StreamStarted true.
	afterErr := errors.New("mid-stream boom")
	sink := &collectingSink{}
	execAfter := newStreamExecutor(t, &fakeStreamTransport{chunks: []execution.Chunk{{Delta: "hello"}, {Err: afterErr}}}, sink)
	out := execAfter.runStream(context.Background(), execution.ResolvedRoute{}, execution.NormalizedRequest{})
	if !out.StreamStarted {
		t.Fatalf("StreamStarted = false after a chunk reached the client, want true")
	}
	if len(sink.deltas) != 1 || sink.deltas[0] != "hello" {
		t.Fatalf("sink got %v, want [hello]", sink.deltas)
	}

	// A pre-first-byte failure → StreamStarted false.
	execBefore := newStreamExecutor(t, &fakeStreamTransport{preErr: errors.New("connect refused")}, &collectingSink{})
	outBefore := execBefore.runStream(context.Background(), execution.ResolvedRoute{}, execution.NormalizedRequest{})
	if outBefore.StreamStarted {
		t.Fatalf("StreamStarted = true on a pre-first-byte failure, want false")
	}
}

// --- W3-M6: credential canary ---------------------------------------------

type fakeCredLister struct{ id string }

func (f fakeCredLister) ListForAccount(_ context.Context, _ string) ([]accountsdomain.Credential, error) {
	return []accountsdomain.Credential{{ID: f.id, State: accountsdomain.CredentialActive}}, nil
}

type fakeCredUser struct{ plaintext string }

func (f fakeCredUser) Use(_ context.Context, _ string, fn func(plaintext []byte) error) error {
	return fn([]byte(f.plaintext))
}

// TestExecutor_CredentialNeverEscapes proves the decrypted credential reaches
// the transport (exactly the place it must) but never leaks onto the shared
// route holder the classifier reads, nor into the returned outcome's error.
//
// Mutation W3-M6: set the shared routeHolder to the route WITH the credential
// (instead of the credential-free base) → routeHolder.Credential.Value carries
// the plaintext → RED.
func TestExecutor_CredentialNeverEscapes(t *testing.T) {
	const plaintext = "sk-live-CANARY-cred-9f3a"
	transport := &fakeStreamTransport{execResp: &execution.NormalizedResponse{Message: execution.Message{Role: "assistant", Content: "ok"}}}
	const tt = execution.TransportTypeOpenAICompatible
	disp := execution.NewDispatcher(fixedResolver{tt: tt}, map[execution.TransportType]execution.InferenceTransport{tt: transport})
	holder := &execution.ResolvedRoute{}
	ex := &executorAdapter{
		dispatcher:  disp,
		classify:    func(_ execution.ResolvedRoute, err error) execution.TypedFailure { return execution.TypedFailure{} },
		creds:       fakeCredLister{id: "cred-1"},
		credService: fakeCredUser{plaintext: plaintext},
		baseURLFor:  func(string) string { return "https://upstream.example" },
		inflight:    newInflightCounter(),
		routeHolder: holder,
	}

	out := ex.Execute(context.Background(), routing.ResolvedAttempt{
		RequestID: "req-1",
		Candidate: routing.CandidateOffering{ProviderID: "opencode-zen", AccountID: "acct-1", ProviderModelID: "m1"},
	})
	if out.Err != nil {
		t.Fatalf("Execute error = %v", out.Err)
	}
	// The credential DID reach the transport (its rightful destination).
	if transport.lastAuth != plaintext {
		t.Fatalf("transport received credential %q, want the plaintext (it must reach the transport exactly once)", transport.lastAuth)
	}
	// But it NEVER leaked onto the shared route holder the classifier reads.
	if strings.Contains(holder.Credential.Value, plaintext) || holder.Credential.Value != "" {
		t.Fatalf("route holder leaked the credential: %q", holder.Credential.Value)
	}
	// Nor into the outcome error text.
	if out.Err != nil && strings.Contains(out.Err.Error(), plaintext) {
		t.Fatalf("outcome error leaked the credential: %v", out.Err)
	}
}

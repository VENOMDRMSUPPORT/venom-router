package httpapi

// usability_assembler_test.go pins usabilityVerifier.verifyAccount — the piece
// that assembles the live dependencies (offering lister + credential lease) and
// runs the per-account verification loop with the leased key. The credential is
// leased ONLY when there is work to do, and lister/lease errors surface rather
// than silently producing an empty verdict set.

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

type fakeOfferingLister struct {
	rows []storage.ChatOfferingToVerify
	err  error
}

func (f fakeOfferingLister) ListChatOfferingsToVerify(context.Context, string) ([]storage.ChatOfferingToVerify, error) {
	return f.rows, f.err
}

type fakeDeclaredLister struct {
	rows []storage.NonChatOperationToCertify
	err  error
}

func (f fakeDeclaredLister) ListNonChatOperationsToCertify(context.Context, string) ([]storage.NonChatOperationToCertify, error) {
	return f.rows, f.err
}

type fakeLeaser struct {
	key    string
	err    error
	called bool
}

func (f *fakeLeaser) Use(_ context.Context, _ string, fn func([]byte) error) error {
	f.called = true
	if f.err != nil {
		return f.err
	}
	return fn([]byte(f.key))
}

func TestUsabilityVerifier_LeasesAndRunsWhenThereIsWork(t *testing.T) {
	lc := &fakeCertLifecycle{}
	leaser := &fakeLeaser{key: "leased-secret"}
	v := &usabilityVerifier{
		offerings: fakeOfferingLister{rows: []storage.ChatOfferingToVerify{
			{OfferingOperationID: "op-a", ProviderModelID: "big-pickle"},
			{OfferingOperationID: "op-b", ProviderModelID: "deepseek-v4-flash-free"},
		}},
		creds:   leaser,
		driver:  lc,
		probe:   probeByModel(map[string]zenChatUsability{"big-pickle": zenChatUsable, "deepseek-v4-flash-free": zenChatUsable}),
		baseURL: "http://x",
	}

	got, err := v.verifyAccount(context.Background(), "acct-1", "cred-1")
	if err != nil {
		t.Fatalf("verifyAccount() error = %v", err)
	}
	if !leaser.called {
		t.Fatal("credential was not leased despite there being work")
	}
	if got.Probed != 2 || got.Usable != 2 {
		t.Fatalf("summary = %+v, want Probed 2 Usable 2", got)
	}
	if len(lc.records) != 2 {
		t.Fatalf("recorded %d attempts, want 2", len(lc.records))
	}
}

func TestUsabilityVerifier_CertifiesDeclaredCapabilitiesWithoutLeasing(t *testing.T) {
	lc := &fakeCertLifecycle{}
	leaser := &fakeLeaser{key: "unused"}
	v := &usabilityVerifier{
		offerings: fakeOfferingLister{rows: nil}, // no chat runtime work
		declared: fakeDeclaredLister{rows: []storage.NonChatOperationToCertify{
			{OfferingOperationID: "op-tools", Operation: "tools"},
			{OfferingOperationID: "op-vision", Operation: "vision"},
		}},
		creds:   leaser,
		driver:  lc,
		probe:   probeByModel(nil),
		baseURL: "http://x",
	}

	got, err := v.verifyAccount(context.Background(), "acct-1", "cred-1")
	if err != nil {
		t.Fatalf("verifyAccount() error = %v", err)
	}
	// Declaration-certification runs a live probe for NOTHING, so it must never
	// decrypt the credential — even though there is real (declared) work.
	if leaser.called {
		t.Fatal("credential was leased to certify declared capabilities")
	}
	if got.CertifiedDeclared != 2 {
		t.Fatalf("CertifiedDeclared = %d, want 2", got.CertifiedDeclared)
	}
	if len(lc.records) != 2 {
		t.Fatalf("recorded %d attempts, want 2", len(lc.records))
	}
}

func TestUsabilityVerifier_NoOfferingsNeverLeases(t *testing.T) {
	leaser := &fakeLeaser{key: "unused"}
	v := &usabilityVerifier{
		offerings: fakeOfferingLister{rows: nil},
		creds:     leaser,
		driver:    &fakeCertLifecycle{},
		probe:     probeByModel(nil),
		baseURL:   "http://x",
	}

	got, err := v.verifyAccount(context.Background(), "acct-1", "cred-1")
	if err != nil {
		t.Fatalf("verifyAccount() error = %v", err)
	}
	if leaser.called {
		t.Fatal("credential was leased with no work to do")
	}
	if got.Probed != 0 {
		t.Fatalf("summary = %+v, want zero", got)
	}
}

func TestUsabilityVerifier_ListerErrorSurfacesWithoutLeasing(t *testing.T) {
	leaser := &fakeLeaser{key: "unused"}
	wantErr := errors.New("catalog down")
	v := &usabilityVerifier{
		offerings: fakeOfferingLister{err: wantErr},
		creds:     leaser,
		driver:    &fakeCertLifecycle{},
		probe:     probeByModel(nil),
		baseURL:   "http://x",
	}

	if _, err := v.verifyAccount(context.Background(), "acct-1", "cred-1"); !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want the lister error", err)
	}
	if leaser.called {
		t.Fatal("credential was leased despite a lister failure")
	}
}

func TestUsabilityVerifier_LeaseErrorSurfaces(t *testing.T) {
	wantErr := errors.New("decrypt failed")
	v := &usabilityVerifier{
		offerings: fakeOfferingLister{rows: []storage.ChatOfferingToVerify{{OfferingOperationID: "op-a", ProviderModelID: "big-pickle"}}},
		creds:     &fakeLeaser{err: wantErr},
		driver:    &fakeCertLifecycle{},
		probe:     probeByModel(map[string]zenChatUsability{"big-pickle": zenChatUsable}),
		baseURL:   "http://x",
	}

	if _, err := v.verifyAccount(context.Background(), "acct-1", "cred-1"); !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want the lease error", err)
	}
}

// --- the observed -> probing edge: fast lane drives it, the sweep does not ---

// fakeCatalogStates is a stateful stand-in for the real catalog: it holds each
// chat offering-operation's certification STATE and answers the two listers the
// way the SQL does — `observed` rows only from ListObservedChatOfferings,
// `probing` rows only from ListChatOfferingsToVerify. StartProbe is the real
// observed -> probing edge, so a test cannot get a fresh row probed without
// actually driving that edge first (no fake can hand the verifier a `probing`
// row it never advanced).
type fakeCatalogStates struct {
	// state maps offering-operation id -> certification status.
	state map[string]string
	// model maps offering-operation id -> provider model id.
	model map[string]string
	// started records every StartProbe call, in order.
	started []string
	// startErr, when set for an id, makes that row's StartProbe fail.
	startErr map[string]error
}

func (f *fakeCatalogStates) rowsInState(status string) []storage.ChatOfferingToVerify {
	var out []storage.ChatOfferingToVerify
	for id, s := range f.state {
		if s == status {
			out = append(out, storage.ChatOfferingToVerify{OfferingOperationID: id, ProviderModelID: f.model[id]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OfferingOperationID < out[j].OfferingOperationID })
	return out
}

func (f *fakeCatalogStates) ListObservedChatOfferings(context.Context, string) ([]storage.ChatOfferingToVerify, error) {
	return f.rowsInState("observed"), nil
}

func (f *fakeCatalogStates) ListChatOfferingsToVerify(context.Context, string) ([]storage.ChatOfferingToVerify, error) {
	return f.rowsInState("probing"), nil
}

func (f *fakeCatalogStates) StartProbe(_ context.Context, offeringOperationID string) (models.Certification, error) {
	f.started = append(f.started, offeringOperationID)
	if err := f.startErr[offeringOperationID]; err != nil {
		return models.Certification{}, err
	}
	if f.state[offeringOperationID] == "observed" {
		f.state[offeringOperationID] = "probing"
	}
	return models.Certification{}, nil
}

func newFastLaneCatalog() *fakeCatalogStates {
	return &fakeCatalogStates{
		state: map[string]string{"op-fresh": "observed"},
		model: map[string]string{"op-fresh": "brand-new-free"},
	}
}

// TestUsabilityVerifier_FastLaneDrivesObservedChatOpsToProbing is finding 1's
// unit pin: on a FRESH connect every chat row discovery just seeded is still
// `observed`, so the fast lane must drive the observed -> probing edge itself
// (the same CertificationDriver.StartProbe edge the drainer uses) before it can
// list and probe anything. Without that drive the row is never probed and the
// account waits a whole scheduler round.
func TestUsabilityVerifier_FastLaneDrivesObservedChatOpsToProbing(t *testing.T) {
	catalog := newFastLaneCatalog()
	lc := &fakeCertLifecycle{}
	v := &usabilityVerifier{
		offerings: catalog,
		observed:  catalog,
		starter:   catalog,
		creds:     &fakeLeaser{key: "leased-secret"},
		driver:    lc,
		probe:     probeByModel(map[string]zenChatUsability{"brand-new-free": zenChatUsable}),
		baseURL:   "http://x",
	}

	got, err := v.verifyAccountFastLane(context.Background(), "acct-1", "cred-1")
	if err != nil {
		t.Fatalf("verifyAccountFastLane() error = %v", err)
	}
	if len(catalog.started) != 1 || catalog.started[0] != "op-fresh" {
		t.Fatalf("StartProbe calls = %v, want exactly [op-fresh]", catalog.started)
	}
	if got.StartedProbing != 1 {
		t.Fatalf("StartedProbing = %d, want 1", got.StartedProbing)
	}
	if got.Probed != 1 || got.Usable != 1 {
		t.Fatalf("summary = %+v, want Probed 1 Usable 1 — the freshly observed row must actually get probed", got)
	}
	records := lc.recorded()
	if len(records) != 1 || records[0].op != "op-fresh" {
		t.Fatalf("recorded attempts = %+v, want one against op-fresh", records)
	}
}

// TestUsabilityVerifier_ScheduledSweepIgnoresObservedChatOps pins the OTHER
// half of the contract split: the SCHEDULED sweep keeps its probing-only
// contract. The drainer owns the observed -> probing edge in the steady state,
// so the sweep must never double-drive it — given the exact same freshly
// observed catalog the fast-lane test uses, the sweep does nothing at all.
func TestUsabilityVerifier_ScheduledSweepIgnoresObservedChatOps(t *testing.T) {
	catalog := newFastLaneCatalog()
	lc := &fakeCertLifecycle{}
	leaser := &fakeLeaser{key: "leased-secret"}
	v := &usabilityVerifier{
		offerings: catalog,
		observed:  catalog,
		starter:   catalog,
		creds:     leaser,
		driver:    lc,
		probe:     probeByModel(map[string]zenChatUsability{"brand-new-free": zenChatUsable}),
		baseURL:   "http://x",
	}

	got, err := v.verifyAccount(context.Background(), "acct-1", "cred-1")
	if err != nil {
		t.Fatalf("verifyAccount() error = %v", err)
	}
	if len(catalog.started) != 0 {
		t.Fatalf("the scheduled sweep drove the observed -> probing edge (%v) — that is the drainer's", catalog.started)
	}
	if got.Probed != 0 || got.StartedProbing != 0 {
		t.Fatalf("summary = %+v, want zero — an `observed` row is not the sweep's work", got)
	}
	if leaser.called {
		t.Fatal("credential was leased with no `probing` work to do")
	}
	if len(lc.recorded()) != 0 {
		t.Fatalf("recorded %d attempts, want 0", len(lc.recorded()))
	}
}

// TestUsabilityVerifier_FastLaneSkipsRowsWhoseEdgeFails proves a per-row
// StartProbe failure is SKIPPED, never fatal: the sibling row still gets its
// edge driven, probed and recorded — mirroring the drainer's own
// count-and-continue posture.
func TestUsabilityVerifier_FastLaneSkipsRowsWhoseEdgeFails(t *testing.T) {
	catalog := &fakeCatalogStates{
		state:    map[string]string{"op-bad": "observed", "op-good": "observed"},
		model:    map[string]string{"op-bad": "cas-conflict-model", "op-good": "brand-new-free"},
		startErr: map[string]error{"op-bad": errors.New("compare-and-swap conflict")},
	}
	lc := &fakeCertLifecycle{}
	v := &usabilityVerifier{
		offerings: catalog,
		observed:  catalog,
		starter:   catalog,
		creds:     &fakeLeaser{key: "leased-secret"},
		driver:    lc,
		probe:     probeByModel(map[string]zenChatUsability{"brand-new-free": zenChatUsable}),
		baseURL:   "http://x",
	}

	got, err := v.verifyAccountFastLane(context.Background(), "acct-1", "cred-1")
	if err != nil {
		t.Fatalf("verifyAccountFastLane() error = %v, want the failed row skipped rather than surfaced", err)
	}
	if got.StartedProbing != 1 {
		t.Fatalf("StartedProbing = %d, want 1 (only op-good advanced)", got.StartedProbing)
	}
	if got.Probed != 1 || got.Usable != 1 {
		t.Fatalf("summary = %+v, want Probed 1 Usable 1", got)
	}
	records := lc.recorded()
	if len(records) != 1 || records[0].op != "op-good" {
		t.Fatalf("recorded attempts = %+v, want one against op-good only", records)
	}
}

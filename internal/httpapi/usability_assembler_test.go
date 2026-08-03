package httpapi

// usability_assembler_test.go pins usabilityVerifier.verifyAccount — the piece
// that assembles the live dependencies (offering lister + credential lease) and
// runs the per-account verification loop with the leased key. The credential is
// leased ONLY when there is work to do, and lister/lease errors surface rather
// than silently producing an empty verdict set.

import (
	"context"
	"errors"
	"testing"

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

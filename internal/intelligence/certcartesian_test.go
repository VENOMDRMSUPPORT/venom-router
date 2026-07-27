package intelligence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
)

// TestP3cGate_CartesianCertificationMatrix is docs/06's P3c phase gate:
// the deterministic 6-state × 3-truth Cartesian acceptance test (04 §5).
// Both sets are derived from the packages that own them
// (models.CertificationStates(), and the fixed three-value
// models.CapabilityTruth vocabulary) — never hardcoded as a second,
// parallel list that could silently drift from the real one.
func TestP3cGate_CartesianCertificationMatrix(t *testing.T) {
	states := models.CertificationStates()
	truths := []models.CapabilityTruth{models.TruthUnknown, models.TruthSupported, models.TruthUnsupported}

	if len(states) != 6 {
		t.Fatalf("len(states) = %d, want 6", len(states))
	}
	if len(truths) != 3 {
		t.Fatalf("len(truths) = %d, want 3", len(truths))
	}

	visited := 0
	routableCount := 0

	for _, s := range states {
		for _, tr := range truths {
			visited++

			in := AdmissionInput{
				State: s, Truth: tr,
				IdentityResolved: true, ContextVerified: true, FundingKnown: true, HealthyAccount: true,
			}
			verdict := Admit(in)

			wantRoutable := s == models.CertCertified && tr == models.TruthSupported
			if verdict.Routable != wantRoutable {
				t.Errorf("state=%q truth=%q Routable=%v, want %v", s, tr, verdict.Routable, wantRoutable)
			}
			if verdict.Routable {
				routableCount++
			}
		}
	}

	if visited != 18 {
		t.Fatalf("visited %d combinations, want exactly 18 (6 states x 3 truths)", visited)
	}
	if routableCount != 1 {
		t.Fatalf("routableCount = %d, want exactly 1 — (certified, supported) must be the ONLY routable combination", routableCount)
	}
}

// TestP3cGate_InvalidTransitionsRejectedAndAudited drives every invalid
// transition 04 §5 names through CertificationDriver: rejected with its
// typed error, stored state unchanged, exactly one audit record with
// Accepted:false.
func TestP3cGate_InvalidTransitionsRejectedAndAudited(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		state models.CertificationState
		call  func(d *CertificationDriver, id string) (models.Certification, error)
	}{
		{"discovered -> certified (no probe verdict)", models.CertDiscovered, func(d *CertificationDriver, id string) (models.Certification, error) {
			outcome, _ := ClassifyProbeSignal(SignalCapabilityResponse)
			return d.RecordAttempt(context.Background(), id, outcome, 1)
		}},
		{"observed -> certified (no probe verdict)", models.CertObserved, func(d *CertificationDriver, id string) (models.Certification, error) {
			outcome, _ := ClassifyProbeSignal(SignalCapabilityResponse)
			return d.RecordAttempt(context.Background(), id, outcome, 1)
		}},
		{"expired -> certified (a stale verdict must be re-proven via probing)", models.CertExpired, func(d *CertificationDriver, id string) (models.Certification, error) {
			return d.Resume(context.Background(), id)
		}},
		{"certified -> probing (must pass through expired or suspended)", models.CertCertified, func(d *CertificationDriver, id string) (models.Certification, error) {
			return d.ReProbe(context.Background(), id)
		}},
		{"suspended -> expired (not a legal edge)", models.CertSuspended, func(d *CertificationDriver, id string) (models.Certification, error) {
			return d.Expire(context.Background(), id)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := "oo-gate-invalid"
			cert := models.Certification{OfferingOperationID: id, State: tt.state, Truth: models.TruthUnknown}
			store := &trapCertStore{t: t, loadCert: cert}
			auditor := &fakeAuditor{}
			d, err := NewCertificationDriver(store, auditor, DefaultProbeRetryBudget, clockAt(now))
			if err != nil {
				t.Fatalf("NewCertificationDriver error = %v", err)
			}

			got, err := tt.call(d, id)
			if !errors.Is(err, models.ErrIllegalCertificationTransition) {
				t.Fatalf("err = %v, want ErrIllegalCertificationTransition", err)
			}
			if got.State != tt.state {
				t.Fatalf("state = %q, want unchanged %q", got.State, tt.state)
			}
			if len(auditor.records) != 1 {
				t.Fatalf("audit records = %d, want exactly 1", len(auditor.records))
			}
			if auditor.records[0].Accepted {
				t.Fatalf("audit Accepted = true, want false")
			}
		})
	}
}

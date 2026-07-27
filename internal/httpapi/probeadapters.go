package httpapi

import (
	"context"
	"errors"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// auditActionOfferingProbe is P3c-CAPI-001's audit action code (mirrors
// discovery.go's AuditActionAccountDiscover / audit.go's own convention).
// It lives here rather than in audit.go — audit.go is not one of this
// batch's touchable files — but it is the SAME const-block pattern audit.go
// already uses; nothing about its shape is special-cased.
const auditActionOfferingProbe = "offering_probe" // POST /offerings/{id}/probe

// auditActionCertificationTransitioned is the audit action code
// certificationAuditorAdapter emits under (04 §5: "each [transition]
// emits an audit_event").
const auditActionCertificationTransitioned = "certification_transitioned"

// probeReserverAdapter implements intelligence.ProbeReserver over the
// frozen P3b reservation engine (storage.QuotaReservationRepo). It never
// re-derives the reservation contract itself — Reserve's own BEGIN
// IMMEDIATE / single-connection discipline is untouched here.
type probeReserverAdapter struct {
	reservations *storage.QuotaReservationRepo
}

func newProbeReserverAdapter(reservations *storage.QuotaReservationRepo) *probeReserverAdapter {
	return &probeReserverAdapter{reservations: reservations}
}

// ReserveProbe implements intelligence.ProbeReserver: maps the port's flat
// signature onto storage.ReserveParams and returns the reservation id.
// Any failure (including storage.ErrReservationRejected) is returned
// unwrapped — intelligence.ProbeGuard.Admit already translates ANY
// non-nil error from this port into the single RefusalQuotaRejected
// reason (probesafety.go), so no translation belongs here.
func (a *probeReserverAdapter) ReserveProbe(ctx context.Context, accountID, requestID, attemptID string, allocations []quota.Allocation) (string, error) {
	result, err := a.reservations.Reserve(ctx, storage.ReserveParams{
		AccountID:   accountID,
		RequestID:   requestID,
		AttemptID:   attemptID,
		Allocations: allocations,
	})
	if err != nil {
		return "", err
	}
	return result.ReservationID, nil
}

// certificationAuditorAdapter implements intelligence.CertificationAuditor
// over the shared auditEmitter. It writes ids/typed codes ONLY — the
// record's own fields (OfferingOperationID, typed state/reason/suspension
// codes) are the entire payload; there is no free-text field on
// intelligence.CertificationAuditRecord this adapter could leak a secret
// through even if it tried.
type certificationAuditorAdapter struct {
	audit *auditEmitter
}

func newCertificationAuditorAdapter(audit *auditEmitter) *certificationAuditorAdapter {
	return &certificationAuditorAdapter{audit: audit}
}

// CertificationTransitioned implements intelligence.CertificationAuditor.
// auditEmitter.Emit never returns an error (a write failure is logged and
// swallowed there, by design — see audit.go), so this always returns nil.
func (a *certificationAuditorAdapter) CertificationTransitioned(ctx context.Context, rec intelligence.CertificationAuditRecord) error {
	result := AuditResultFailure
	if rec.Accepted {
		result = AuditResultSuccess
	}
	a.audit.Emit(ctx, auditActionCertificationTransitioned, result, AuditResourceOffering, rec.OfferingOperationID, string(rec.Reason))
	return nil
}

// ErrProbeTransportUnavailable is returned by every probeTransportAdapter
// call. RESOLVED AMBIGUITY (see this batch's final report): internal/
// execution's NormalizedRequest carries no max_tokens/declared-input-
// tokens field, NormalizedResponse exposes no HTTP status, provider error
// code, structured context-limit field, or witness classification, and
// InferenceTransport's error path (NormalizeError) does not surface any
// of those raw fields either — the frozen seam cannot express a probe
// request or classify a probe response today. Per this batch's explicit
// instruction, that seam is NOT reshaped; this adapter is the honest,
// clearly-marked stub described there: it never fabricates a witness or
// an invented context limit, it simply reports "unavailable" for every
// provider.
var ErrProbeTransportUnavailable = errors.New("httpapi: no probe transport is wired for any provider yet (internal/execution cannot express a probe request)")

// probeTransportAdapter is the production (stub) intelligence.ProbeTransport.
type probeTransportAdapter struct{}

func newProbeTransportAdapter() *probeTransportAdapter {
	return &probeTransportAdapter{}
}

// Available always reports false in production. ServeProbe consults this
// SYNCHRONOUSLY, before creating any job row, so a probe request for any
// provider is refused (409 probe_unsupported) before any side effect.
func (probeTransportAdapter) Available(_ string) bool { return false }

// Probe never actually calls a provider — see ErrProbeTransportUnavailable's
// doc comment. This method exists only to satisfy intelligence.ProbeTransport
// structurally; production code never reaches it, since Available always
// refuses first.
func (probeTransportAdapter) Probe(_ context.Context, _ intelligence.ProbeRequest) (intelligence.ProbeResult, error) {
	return intelligence.ProbeResult{}, ErrProbeTransportUnavailable
}

// Compile-time proof the production adapters structurally satisfy their
// intelligence ports.
var (
	_ intelligence.ProbeReserver        = (*probeReserverAdapter)(nil)
	_ intelligence.CertificationAuditor = (*certificationAuditorAdapter)(nil)
	_ intelligence.ProbeTransport       = (*probeTransportAdapter)(nil)
)

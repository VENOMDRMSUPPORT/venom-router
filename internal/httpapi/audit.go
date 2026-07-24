package httpapi

import (
	"context"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/sanitize"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// Audit action codes (P2b-OBS-001) — the fixed vocabulary every audit
// emit call site uses. Never a free-form string.
const (
	AuditActionOAuthBegin     = "oauth_begin"
	AuditActionOAuthComplete  = "oauth_complete"
	AuditActionReauthBegin    = "reauth_begin"
	AuditActionAccountConnect = "account_connect"

	// P2b-CAPI-004 account-lifecycle action codes. Each is the fixed verb a
	// mutating account endpoint (or the reveal/health/sync endpoints) emit
	// under — the resource_id is always the account/provider id, NEVER any
	// credential, key, or token (see auditEmitter's sanitize backstop).
	AuditActionAccountReveal     = "account_reveal"     // POST /accounts/{id}/reveal
	AuditActionAccountFunding    = "account_funding"    // PUT /accounts/{id}/funding
	AuditActionAccountStop       = "account_stop"       // POST /accounts/{id}/stop
	AuditActionAccountResume     = "account_resume"     // POST /accounts/{id}/resume
	AuditActionAccountDisconnect = "account_disconnect" // DELETE /accounts/{id} (soft-disconnect)
	AuditActionAccountHealth     = "account_health"     // POST /accounts/{id}/health
	AuditActionProviderSync      = "provider_sync"      // POST /providers/{id}/sync
	AuditActionSettingsUpdate    = "settings_update"    // PUT /settings
)

// Audit result codes.
const (
	AuditResultSuccess = "success"
	AuditResultFailure = "failure"
)

// Audit resource-type codes.
const (
	AuditResourceProvider = "provider"
	AuditResourceAccount  = "account"
	AuditResourceSettings = "settings"
)

// auditEmitter records one audit_events row per mutating control call
// (P2b-OBS-001, migration 00004's general-purpose audit_events table —
// distinct from M1's auth-only auth_events/AuthEventRepo). It is a
// small httpapi-layer wrapper over storage.AuditEventRepo: httpapi
// already imports storage, so no new application-layer port is needed
// for this. Every argument is a typed action/result/resource code;
// resourceID and reasonCode additionally pass through sanitize.Text
// (content-based redaction of Authorization headers, Bearer tokens, and
// key=value secret-shaped parameters) before being persisted, as a
// defense-in-depth backstop for a call site that mistakenly threads
// free text through this path — the canonical prevention is that every
// real call site only ever passes ids/fixed codes here, never a
// credential.
type auditEmitter struct {
	repo *storage.AuditEventRepo
	log  *observability.Logger
	now  func() time.Time
}

// newAuditEmitter builds the emitter over db's existing connection. log
// defaults to observability.Default() when nil.
func newAuditEmitter(db *storage.DB, log *observability.Logger) *auditEmitter {
	if log == nil {
		log = observability.Default()
	}
	return &auditEmitter{repo: storage.NewAuditEventRepo(db), log: log, now: time.Now}
}

// Emit appends one audit_events row. A write failure is logged via the
// observability boundary and otherwise swallowed: log-and-continue,
// never fail (or roll back) the primary mutation this call is auditing
// just because the audit write itself failed.
func (e *auditEmitter) Emit(ctx context.Context, action, result, resourceType, resourceID, reasonCode string) {
	row := storage.AuditEventRow{
		Action:     action,
		EntityType: resourceType,
		EntityID:   sanitize.Text(resourceID),
		Result:     result,
		ReasonCode: sanitize.Text(reasonCode),
		At:         e.now(),
	}
	if err := e.repo.Append(ctx, row); err != nil {
		e.log.Error("audit event write failed",
			observability.String("action", action),
			observability.String("result", result),
			observability.Err(err),
		)
	}
}

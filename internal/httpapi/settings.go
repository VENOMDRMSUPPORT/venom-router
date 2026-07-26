package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// allowedThemes and allowedDensities are the EXACT frozen
// @venom/design-system vocabularies (Design_System/src/themes.ts -> THEMES,
// Design_System/src/density.ts -> DENSITIES): venom-{dark,light,hc} for
// theme (default venom-dark) and {comfortable,compact} for density
// (default comfortable). There is no "system" theme, and the real values
// are venom-*-prefixed, so the browser sends these exact names — any other
// set here would reject the real client payload. These match the
// owner_settings CHECK constraint verbatim (00005_owner_settings.sql); the
// DB CHECK is the defense-in-depth backstop behind this httpapi-side
// validation.
var (
	allowedThemes = map[string]bool{
		"venom-dark":  true,
		"venom-light": true,
		"venom-hc":    true,
	}
	allowedDensities = map[string]bool{
		"comfortable": true,
		"compact":     true,
	}
)

// SettingsHandler serves the single owner-settings surface (P2b-CAPI-005,
// 07 §2.3/§3): GET /settings and PUT /settings on the same route. Config
// only — theme/density are non-secret UI enums; no credential/token/key
// ever passes through this handler. Owner-session + CSRF gated via
// ControlMux's `gated` (the handler never re-validates the session or CSRF
// itself); PUT emits exactly one secret-free audit_event through the shared
// auditEmitter (GET emits none — reads are not audited, like GET /accounts).
type SettingsHandler struct {
	settings *storage.SettingsRepo
	audit    *auditEmitter
	now      func() time.Time
}

// NewSettingsHandler builds the handler over the settings repo, the shared
// audit emitter, and an injectable clock. now defaults to time.Now when
// nil, exactly like every other injectable clock in this package.
func NewSettingsHandler(settings *storage.SettingsRepo, audit *auditEmitter, now func() time.Time) *SettingsHandler {
	if now == nil {
		now = time.Now
	}
	return &SettingsHandler{settings: settings, audit: audit, now: now}
}

// settingsJSON is the GET/PUT success payload (09 §1 envelope under "data"):
// {"data":{"theme":...,"density":...,"enrichment_enabled":...}}. No secret
// ever appears here.
type settingsJSON struct {
	Theme             string `json:"theme"`
	Density           string `json:"density"`
	EnrichmentEnabled bool   `json:"enrichment_enabled"`
}

func toSettingsJSON(row storage.SettingsRow) settingsJSON {
	return settingsJSON{Theme: row.Theme, Density: row.Density, EnrichmentEnabled: row.EnrichmentEnabled}
}

// settingsUpdateRequest is PUT /settings' body.
type settingsUpdateRequest struct {
	Theme   string `json:"theme"`
	Density string `json:"density"`
}

// enrichmentUpdateRequest is PUT /settings/enrichment's body (P3a-CAPI-003).
// Enabled is a pointer so a missing field, a JSON null, and a non-boolean
// value are all distinguishable from an explicit true/false — every one of
// those three fails validation, never silently coerced.
type enrichmentUpdateRequest struct {
	Enabled *bool `json:"enabled"`
}

// ServeSettings implements GET and PUT /api/control/v1/settings. ONE
// handler serves both methods on the same route (ControlMux registers it
// once under /settings); any other method is 405 method_not_allowed.
func (h *SettingsHandler) ServeSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.serveGet(w, r)
	case http.MethodPut:
		h.servePut(w, r)
	default:
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
	}
}

// serveGet implements GET /settings: the persisted theme/density, or the
// frozen defaults if no row exists yet (a fresh DB). No audit — reads are
// not audited, like GET /accounts.
func (h *SettingsHandler) serveGet(w http.ResponseWriter, r *http.Request) {
	row, err := h.settings.Get(r.Context())
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	writeData(w, http.StatusOK, toSettingsJSON(row))
}

// servePut implements PUT /settings: validate theme/density against the
// frozen design-system vocabulary, then UPSERT. Emits exactly one
// audit_event (settings_update) on validation failure and on write
// success — never on a failed write (the write error path returns 500
// without an audit-success row, mirroring the funding handler's shape).
func (h *SettingsHandler) servePut(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req settingsUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.audit.Emit(ctx, AuditActionSettingsUpdate, AuditResultFailure, AuditResourceSettings, "", "validation_error")
		writeAuthError(w, http.StatusBadRequest, "validation_error", "invalid request body", false)
		return
	}

	// Validate the enum against the frozen design-system vocabulary. The
	// owner_settings CHECK constraint is the DB-level backstop behind this;
	// this httpapi-side validation is the primary gate, giving the client
	// a precise 400 naming the allowed values rather than a raw DB error.
	if !allowedThemes[req.Theme] {
		h.audit.Emit(ctx, AuditActionSettingsUpdate, AuditResultFailure, AuditResourceSettings, "", "validation_error")
		writeAuthError(w, http.StatusBadRequest, "validation_error", "theme must be one of venom-dark, venom-light, venom-hc", false)
		return
	}
	if !allowedDensities[req.Density] {
		h.audit.Emit(ctx, AuditActionSettingsUpdate, AuditResultFailure, AuditResourceSettings, "", "validation_error")
		writeAuthError(w, http.StatusBadRequest, "validation_error", "density must be one of comfortable, compact", false)
		return
	}

	if err := h.settings.Put(ctx, req.Theme, req.Density, h.now()); err != nil {
		// A failed write returns 500 with NO audit-success row — the
		// audit trail records the write's outcome, and a failed write has
		// no success to record. (A validation failure above already
		// recorded its own audit row.)
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	h.audit.Emit(ctx, AuditActionSettingsUpdate, AuditResultSuccess, AuditResourceSettings, "", "")
	// Re-read rather than echo req.Theme/req.Density directly: this PUT
	// never touches enrichment_enabled (P3a-CAPI-003 owns that field via
	// its own PUT /settings/enrichment), so the response must reflect
	// whatever that field currently holds, not a zero value.
	row, err := h.settings.Get(ctx)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	writeData(w, http.StatusOK, toSettingsJSON(row))
}

// ServeEnrichment implements PUT /api/control/v1/settings/enrichment
// (P3a-CAPI-003, 04 §2b): the owner toggle for optional, off-by-default
// metadata enrichment — a routing-NON-critical pipeline entirely separate
// from the always-on free-safety resolution (internal/intelligence's
// FreeSafetyResolver never reads this setting). Any method other than PUT
// is 405. Emits exactly one audit_event on success; none on a validation
// failure or a failed write.
func (h *SettingsHandler) ServeEnrichment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	ctx := r.Context()

	var req enrichmentUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAuthError(w, http.StatusBadRequest, "validation_error", "invalid request body", false)
		return
	}
	if req.Enabled == nil {
		writeAuthError(w, http.StatusBadRequest, "validation_error", "enabled is required and must be a boolean", false)
		return
	}

	if err := h.settings.PutEnrichment(ctx, *req.Enabled, h.now()); err != nil {
		// A failed write emits no success audit row, mirroring servePut's
		// own failure-write posture.
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	h.audit.Emit(ctx, AuditActionSettingsUpdate, AuditResultSuccess, AuditResourceSettings, "", "")

	row, err := h.settings.Get(ctx)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	writeData(w, http.StatusOK, toSettingsJSON(row))
}

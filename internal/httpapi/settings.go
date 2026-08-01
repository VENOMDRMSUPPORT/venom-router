package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"reflect"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
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
	// allowedAccents is the EXACT frozen @venom/design-system customizer
	// vocabulary (Design_System/src/customizer.ts -> ACCENTS, default
	// "mono"). Matches the owner_settings accent CHECK constraint verbatim
	// (00013_owner_settings_customizer.sql); the DB CHECK is the
	// defense-in-depth backstop behind this httpapi-side validation.
	allowedAccents = map[string]bool{
		"mono":    true,
		"blue":    true,
		"violet":  true,
		"amber":   true,
		"emerald": true,
		"rose":    true,
	}
)

// radiusPxMin/Max and spacingScaleMin/Max are the frozen customizer ranges
// (Design_System/src/customizer.ts -> RADIUS_MIN_PX/RADIUS_MAX_PX and
// SPACING_SCALE_MIN/SPACING_SCALE_MAX), mirrored by the 00013 CHECK
// constraints. Server-side validation is step-free: any float inside the
// spacing range is accepted (the UI's 0.05 step is a client affordance,
// not a contract).
const (
	radiusPxMin     = 0
	radiusPxMax     = 16
	spacingScaleMin = 0.75
	spacingScaleMax = 1.25
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
	// effective is the boot-resolved, READ-ONLY runtime configuration this
	// handler reports but never writes. See effectiveConfigJSON.
	effective effectiveConfigJSON
}

// NewSettingsHandler builds the handler over the settings repo, the shared
// audit emitter, an injectable clock, and the boot-resolved effective
// configuration. now defaults to time.Now when nil, exactly like every other
// injectable clock in this package.
func NewSettingsHandler(settings *storage.SettingsRepo, audit *auditEmitter, now func() time.Time, effective effectiveConfigJSON) *SettingsHandler {
	if now == nil {
		now = time.Now
	}
	return &SettingsHandler{settings: settings, audit: audit, now: now, effective: effective}
}

// settingsJSON is the GET/PUT success payload (09 §1 envelope under "data").
// No secret ever appears here.
type settingsJSON struct {
	Theme             string  `json:"theme"`
	Density           string  `json:"density"`
	Accent            string  `json:"accent"`
	RadiusPx          int     `json:"radius_px"`
	SpacingScale      float64 `json:"spacing_scale"`
	EnrichmentEnabled bool    `json:"enrichment_enabled"`

	// Operational settings (P6-CAPI-001): the knobs that used to be Go
	// constants at their consumers (05 §4 staleness, 04 §2 probe safety).
	QuotaStalenessSeconds        int  `json:"quota_staleness_seconds"`
	ProbeMaxInFlightPerProvider  int  `json:"probe_max_in_flight_per_provider"`
	ProbeExpensiveEnabled        bool `json:"probe_expensive_enabled"`
	ProbePerAccountWindowSeconds int  `json:"probe_per_account_window_seconds"`

	EffectiveConfig effectiveConfigJSON `json:"effective_config"`
}

// effectiveConfigJSON is the READ-ONLY runtime configuration (01 §6b).
//
// WHY READ-ONLY. The listen binds are resolved once at boot with
// default -> env -> flag precedence (internal/config.Load) and handed to
// net.Listen. A writable DB copy could not take effect without a process
// restart, so the running process and the stored value would disagree for an
// unbounded time — two sources of truth for the same fact, which is a
// correctness bug, not a missing feature. They are therefore REPORTED here and
// REJECTED on PUT (never silently ignored, which would let an owner believe
// they had changed a bind).
//
// DataPlaneBind is a pointer: empty means "the public /v1 API shares the
// control listener" (config.Config.DataPlaneBind's documented default), which
// is reported as null rather than as an empty string that reads like a
// misconfiguration.
type effectiveConfigJSON struct {
	Bind          string  `json:"bind"`
	DataPlaneBind *string `json:"data_plane_bind"`
}

// newEffectiveConfig builds the read-only projection from boot-resolved values.
func newEffectiveConfig(bind, dataPlaneBind string) effectiveConfigJSON {
	out := effectiveConfigJSON{Bind: bind}
	if dataPlaneBind != "" {
		out.DataPlaneBind = &dataPlaneBind
	}
	return out
}

func (h *SettingsHandler) toSettingsJSON(row storage.SettingsRow) settingsJSON {
	return settingsJSON{
		Theme:                        row.Theme,
		Density:                      row.Density,
		Accent:                       row.Accent,
		RadiusPx:                     row.RadiusPx,
		SpacingScale:                 row.SpacingScale,
		EnrichmentEnabled:            row.EnrichmentEnabled,
		QuotaStalenessSeconds:        row.QuotaStalenessSeconds,
		ProbeMaxInFlightPerProvider:  row.ProbeMaxInFlightPerProvider,
		ProbeExpensiveEnabled:        row.ProbeExpensiveEnabled,
		ProbePerAccountWindowSeconds: row.ProbePerAccountWindowSeconds,
		EffectiveConfig:              h.effective,
	}
}

// settingsUpdateRequest is PUT /settings' body. RadiusPx/SpacingScale are
// pointers so a missing field and a JSON null are distinguishable from an
// explicit value — both fail validation, never silently coerced to a zero
// value (the enrichmentUpdateRequest precedent).
type settingsUpdateRequest struct {
	Theme        string   `json:"theme"`
	Density      string   `json:"density"`
	Accent       string   `json:"accent"`
	RadiusPx     *float64 `json:"radius_px"`
	SpacingScale *float64 `json:"spacing_scale"`

	// The operational fields and the enrichment toggle are INDEPENDENT
	// switches: absent means "leave unchanged", never "reset to the default".
	// A present-but-invalid value (out of range, wrong type, or JSON null)
	// is a 400 naming the field — never silently coerced to a zero.
	EnrichmentEnabled            *bool    `json:"enrichment_enabled"`
	QuotaStalenessSeconds        *float64 `json:"quota_staleness_seconds"`
	ProbeMaxInFlightPerProvider  *float64 `json:"probe_max_in_flight_per_provider"`
	ProbeExpensiveEnabled        *bool    `json:"probe_expensive_enabled"`
	ProbePerAccountWindowSeconds *float64 `json:"probe_per_account_window_seconds"`
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

// serveGet implements GET /settings: the persisted settings, or the frozen
// defaults if no row exists yet (a fresh DB). No audit — reads are not
// audited, like GET /accounts.
func (h *SettingsHandler) serveGet(w http.ResponseWriter, r *http.Request) {
	row, err := h.settings.Get(r.Context())
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	writeData(w, http.StatusOK, h.toSettingsJSON(row))
}

// servePut implements PUT /settings. The appearance quintet keeps its
// P2b-CAPI-005 contract exactly (required, validated against the frozen
// design-system vocabulary); the operational knobs and the enrichment toggle
// are OPTIONAL, where absent means "leave unchanged". Every field is
// validated before any write, so a request rejected on its last field leaves
// the persisted row untouched. Emits exactly one audit_event on validation
// failure and on write success — never on a failed write.
func (h *SettingsHandler) servePut(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// The body is read once and decoded TWICE: into the typed request, and
	// into a raw field map. The second decode is what makes an explicit JSON
	// null distinguishable from an omitted field — both leave a pointer nil,
	// but they mean opposite things here ("client error" vs "leave alone").
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxSettingsBodyBytes))
	if err != nil {
		h.rejectPut(ctx, w, "invalid request body")
		return
	}

	var present map[string]json.RawMessage
	if err := json.Unmarshal(raw, &present); err != nil {
		h.rejectPut(ctx, w, "invalid request body")
		return
	}
	var req settingsUpdateRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		// A wrong JSON type for a known field is named; malformed JSON is not.
		h.rejectPut(ctx, w, decodeFieldMessage(err))
		return
	}

	// A PUT carrying effective_config is REJECTED, never ignored: those values
	// are resolved at boot, and silently dropping the field would report
	// success for a change that did not happen.
	if _, carried := present["effective_config"]; carried {
		h.rejectPut(ctx, w, "effective_config is read-only: bind and data_plane_bind are resolved at startup from default -> env -> flag and cannot be changed through this endpoint")
		return
	}

	// An explicit null on an optional field is a client error. (On the
	// REQUIRED appearance fields a null is already caught below by the same
	// checks that catch an omission.)
	for _, field := range optionalSettingsFields {
		if v, carried := present[field]; carried && string(v) == "null" {
			h.rejectPut(ctx, w, field+" must not be null; omit it entirely to leave it unchanged")
			return
		}
	}

	// --- Appearance: required, unchanged contract ---------------------------
	//
	// The owner_settings CHECK constraints are the DB-level backstop behind
	// these; this httpapi-side validation is the primary gate, giving the
	// client a precise 400 naming the allowed values rather than a raw DB
	// error.
	if !allowedThemes[req.Theme] {
		h.rejectPut(ctx, w, "theme must be one of venom-dark, venom-light, venom-hc")
		return
	}
	if !allowedDensities[req.Density] {
		h.rejectPut(ctx, w, "density must be one of comfortable, compact")
		return
	}
	if !allowedAccents[req.Accent] {
		h.rejectPut(ctx, w, "accent must be one of mono, blue, violet, amber, emerald, rose")
		return
	}
	// radius_px must be present, an integer, and within [0, 16] —
	// fail-closed: a missing field, a JSON null, a fractional value, and an
	// out-of-range value are all rejected naming the field.
	if req.RadiusPx == nil || *req.RadiusPx != math.Trunc(*req.RadiusPx) ||
		*req.RadiusPx < radiusPxMin || *req.RadiusPx > radiusPxMax {
		h.rejectPut(ctx, w, "radius_px must be an integer between 0 and 16")
		return
	}
	// spacing_scale must be present and within [0.75, 1.25] (step-free
	// server side — the UI's 0.05 step is a client affordance).
	if req.SpacingScale == nil || *req.SpacingScale < spacingScaleMin || *req.SpacingScale > spacingScaleMax {
		h.rejectPut(ctx, w, "spacing_scale must be a number between 0.75 and 1.25")
		return
	}

	// --- Operational: optional, absent = unchanged --------------------------
	//
	// The minimums mirror 00014's CHECK constraints exactly, so this gate and
	// the DB backstop can never disagree about what is legal.
	staleness, msg := optionalIntField("quota_staleness_seconds", req.QuotaStalenessSeconds, 1)
	if msg != "" {
		h.rejectPut(ctx, w, msg)
		return
	}
	maxInFlight, msg := optionalIntField("probe_max_in_flight_per_provider", req.ProbeMaxInFlightPerProvider, 1)
	if msg != "" {
		h.rejectPut(ctx, w, msg)
		return
	}
	accountWindow, msg := optionalIntField("probe_per_account_window_seconds", req.ProbePerAccountWindowSeconds, 1)
	if msg != "" {
		h.rejectPut(ctx, w, msg)
		return
	}

	update := storage.SettingsUpdate{
		Theme:                        req.Theme,
		Density:                      req.Density,
		Accent:                       req.Accent,
		RadiusPx:                     int(*req.RadiusPx),
		SpacingScale:                 *req.SpacingScale,
		EnrichmentEnabled:            req.EnrichmentEnabled,
		QuotaStalenessSeconds:        staleness,
		ProbeMaxInFlightPerProvider:  maxInFlight,
		ProbeExpensiveEnabled:        req.ProbeExpensiveEnabled,
		ProbePerAccountWindowSeconds: accountWindow,
	}
	if err := h.settings.PutSettings(ctx, update, h.now()); err != nil {
		// A failed write returns 500 with NO audit-success row — the
		// audit trail records the write's outcome, and a failed write has
		// no success to record. (A validation failure above already
		// recorded its own audit row.)
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	h.audit.Emit(ctx, AuditActionSettingsUpdate, AuditResultSuccess, AuditResourceSettings, "", "")
	// Re-read rather than echo the request: every field the client omitted
	// keeps whatever it already held, and the response must report those
	// persisted values, not a zero value.
	row, err := h.settings.Get(ctx)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	writeData(w, http.StatusOK, h.toSettingsJSON(row))
}

// maxSettingsBodyBytes bounds the settings body so reading it twice cannot be
// turned into a memory-exhaustion lever. Settings are a dozen scalars.
const maxSettingsBodyBytes = 64 << 10

// optionalSettingsFields is the closed set of fields whose explicit JSON null
// is a client error rather than an omission.
var optionalSettingsFields = []string{
	"enrichment_enabled",
	"quota_staleness_seconds",
	"probe_max_in_flight_per_provider",
	"probe_expensive_enabled",
	"probe_per_account_window_seconds",
}

// rejectPut emits the one validation-failure audit row AND the 400 together,
// so no call site can record one without the other.
func (h *SettingsHandler) rejectPut(ctx context.Context, w http.ResponseWriter, message string) {
	h.audit.Emit(ctx, AuditActionSettingsUpdate, AuditResultFailure, AuditResourceSettings, "", "validation_error")
	writeAuthError(w, http.StatusBadRequest, "validation_error", message, false)
}

// decodeFieldMessage NAMES the offending field when the decoder knows it (a
// wrong JSON type for a known field), and falls back to a generic message for
// JSON that is simply malformed.
func decodeFieldMessage(err error) string {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Field != "" {
		return fmt.Sprintf("%s must be a %s", typeErr.Field, expectedJSONType(typeErr.Type.Kind()))
	}
	return "invalid request body"
}

func expectedJSONType(k reflect.Kind) string {
	switch k {
	case reflect.Bool:
		return "boolean"
	case reflect.Float64, reflect.Float32, reflect.Int:
		return "number"
	case reflect.String:
		return "string"
	default:
		return k.String()
	}
}

// optionalIntField validates one optional integer knob. An absent field
// returns a nil *int, which storage.SettingsUpdate reads as "leave unchanged".
// A fractional or below-minimum value is a 400 naming the field — never
// rounded, never clamped.
func optionalIntField(name string, v *float64, min int) (*int, string) {
	if v == nil {
		return nil, ""
	}
	if *v != math.Trunc(*v) {
		return nil, fmt.Sprintf("%s must be a whole number", name)
	}
	if *v < float64(min) || *v > math.MaxInt32 {
		return nil, fmt.Sprintf("%s must be an integer between %d and %d", name, min, math.MaxInt32)
	}
	n := int(*v)
	return &n, ""
}

// ServeEnrichment implements PUT /api/control/v1/settings/enrichment
// (P3a-CAPI-003, 04 §2b): the owner toggle for optional, off-by-default
// metadata enrichment — a routing-NON-critical pipeline entirely separate
// from the always-on free-safety resolution (internal/intelligence's
// FreeSafetyResolver never reads this setting). Any method other than PUT
// is 405. Emits exactly one audit_event on success; none on a validation
// failure or a failed write.
//
// This route is UNCHANGED by P6-CAPI-001. enrichment_enabled became settable
// through PUT /settings as well, and both routes write the same column — the
// dedicated route remains the narrow, single-field way to flip it.
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
	writeData(w, http.StatusOK, h.toSettingsJSON(row))
}

// ---------------------------------------------------------------------------
// operationalSettings: the ONE resolver every consumer of an owner-configurable
// operational knob goes through (P6-CAPI-001).
//
// It exists so that "read the owner's current value" is a single, testable
// call rather than a hardcoded constant repeated at four call sites. Each
// method performs exactly ONE settings read and is invoked once per
// request/tick at the existing call boundary — never per candidate, per row,
// or per window, and never cached in a package-level variable (which would
// make a settings change invisible until restart, exactly the failure the
// stored setting is meant to remove).
//
// Every method FAILS SOFT to the frozen default on a read error: a transient
// database problem must degrade to today's behaviour, not refuse to route.
// ---------------------------------------------------------------------------

type operationalSettings struct {
	repo *storage.SettingsRepo
}

func newOperationalSettings(repo *storage.SettingsRepo) *operationalSettings {
	return &operationalSettings{repo: repo}
}

// row reads the persisted settings, falling back to the frozen defaults.
func (o *operationalSettings) row(ctx context.Context) storage.SettingsRow {
	if o == nil || o.repo == nil {
		return storage.DefaultSettingsRow()
	}
	row, err := o.repo.Get(ctx)
	if err != nil {
		return storage.DefaultSettingsRow()
	}
	return row
}

// stalenessWindow is the owner's quota-staleness window (05 §4) — the value
// quota.Window.State must be given instead of quota.DefaultStalenessWindow.
func (o *operationalSettings) stalenessWindow(ctx context.Context) time.Duration {
	w := o.row(ctx).StalenessWindow()
	if w <= 0 {
		return quota.DefaultStalenessWindow
	}
	return w
}

// probePolicy is DefaultProbeSafetyPolicy with the owner's three configurable
// probe fields overridden (04 §2). The cost caps and the context-probe
// cooldown are NOT owner-configurable in this batch and keep their frozen
// values, so this deliberately starts from the default rather than building a
// policy from scratch.
func (o *operationalSettings) probePolicy(ctx context.Context) intelligence.ProbeSafetyPolicy {
	row := o.row(ctx)
	policy := intelligence.DefaultProbeSafetyPolicy()
	if row.ProbeMaxInFlightPerProvider >= 1 {
		policy.MaxInFlightPerProvider = row.ProbeMaxInFlightPerProvider
	}
	policy.ExpensiveProbesEnabled = row.ProbeExpensiveEnabled
	if w := row.ProbeAccountWindow(); w > 0 {
		policy.PerAccountWindow = w
	}
	return policy
}

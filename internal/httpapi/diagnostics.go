package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// DiagnosticsHandler serves the P3b-CAPI-002 reconciliation diagnostics
// surface: GET /diagnostics/reconciliation (read model) and POST
// /diagnostics/reconciliation/{reservation_id} (manual recovery: resync
// or accept_estimate). Owner-session + CSRF gated via ControlMux's
// `gated`.
type DiagnosticsHandler struct {
	reconciliation *storage.ReconciliationRepo
	lifecycle      *storage.QuotaLifecycleRepo
	audit          *auditEmitter
	// routes is the P6-CAPI-001 route-explanation read model. It is attached
	// by WithRoutes rather than taken as a NewDiagnosticsHandler parameter,
	// mirroring DiscoveryHandler.WithProbeRuns: the reconciliation routes
	// existed first and their call sites stay untouched.
	routes *observability.RouteReader
}

// NewDiagnosticsHandler builds the handler over the shared
// ReconciliationRepo/QuotaLifecycleRepo instances ControlMux also wires
// for the quota-refresh route.
func NewDiagnosticsHandler(reconciliation *storage.ReconciliationRepo, lifecycle *storage.QuotaLifecycleRepo, audit *auditEmitter) *DiagnosticsHandler {
	return &DiagnosticsHandler{reconciliation: reconciliation, lifecycle: lifecycle, audit: audit}
}

// WithRoutes returns a COPY of h that also serves the route-explanation
// routes over reader. It is copy-returning (like
// DiscoveryHandler.WithProbeRuns) so the reconciliation-only construction
// path stays valid and no existing call site changes.
func (h *DiagnosticsHandler) WithRoutes(reader *observability.RouteReader) *DiagnosticsHandler {
	out := *h
	out.routes = reader
	return &out
}

// ---------------------------------------------------------------------------
// P6-CAPI-001: route explanations (09 §3.9, 05 §7)
//
// The structs below are this package's OWN explicit projection. The
// observability structs are deliberately NOT marshalled directly: a field
// added to observability.RouteDecision/RouteAttempt by some future unit
// would otherwise appear on this public wire surface automatically, which is
// precisely how a raw provider error or an account external id would leak.
// Every field here is a correlation id, a typed code, a count, a score, a
// clamp flag, or a timestamp — there is no field for a prompt, a response, a
// raw provider error, a credential, or an account external id.
// ---------------------------------------------------------------------------

// routeCandidatesJSON is the secret-free candidate-set summary: counts plus
// provider+model group keys only.
type routeCandidatesJSON struct {
	Total          int      `json:"total"`
	EligibleGroups int      `json:"eligible_groups"`
	GroupKeys      []string `json:"group_keys"`
}

// routeChosenJSON identifies the winning candidate group. Every field is
// nullable because a decision that chose nothing (every candidate excluded)
// records no winner — and "no route was chosen" must never render as an
// empty-string provider.
type routeChosenJSON struct {
	ProviderID      *string `json:"provider_id"`
	ProviderModelID *string `json:"provider_model_id"`
	Funding         *string `json:"funding"`
}

// routeThinkingJSON is the thinking-clamp record (05 §1): what was asked for,
// what was applied, and which clamp(s) fired.
type routeThinkingJSON struct {
	Requested        *string `json:"requested"`
	Applied          *string `json:"applied"`
	TierClamped      bool    `json:"tier_clamped"`
	CertifiedClamped bool    `json:"certified_clamped"`
}

// routeAttemptJSON is one attempt's projection.
//
// NOTE on two fields the P6-CAPI-001 card asks for but that cannot be served
// here: an attempt's failure SCOPE and RETRY-AFTER are not columns on the
// frozen route_attempts table (00011_routing.sql), and the record's WRITE path
// is out of scope for this batch. They are therefore omitted rather than
// fabricated from a proxy value — see this batch's report.
type routeAttemptJSON struct {
	Attempt             int     `json:"attempt"`
	ProviderID          string  `json:"provider_id"`
	AccountID           string  `json:"account_id"`
	OfferingOperationID string  `json:"offering_operation_id"`
	Status              string  `json:"status"`
	LatencyMS           *int    `json:"latency_ms"`
	ThinkingClamped     bool    `json:"thinking_clamped"`
	ReservationID       *string `json:"reservation_id"`
	StartedAt           string  `json:"started_at"`
	FinishedAt          *string `json:"finished_at"`
}

// routeDecisionJSON is one decision's projection: the LIST entry, and (via
// routeExplanationJSON's embedding) the shared head of the explanation
// payload. The list entry carries no attempts key at all — the list query
// never reads attempts, and advertising an empty array would claim it did.
type routeDecisionJSON struct {
	RequestID             string              `json:"request_id"`
	DecisionID            string              `json:"decision_id"`
	Tier                  string              `json:"tier"`
	WorkloadProfileBucket string              `json:"workload_profile_bucket"`
	CreatedAt             string              `json:"created_at"`
	Candidates            routeCandidatesJSON `json:"candidates"`
	// ExclusionReasons maps a typed exclusion reason code to its count,
	// verbatim as routing emitted it (never re-worded here).
	ExclusionReasons map[string]int     `json:"exclusion_reasons"`
	Chosen           routeChosenJSON    `json:"chosen"`
	Scores           map[string]float64 `json:"scores"`
	Thinking         routeThinkingJSON  `json:"thinking"`
}

// routeExplanationJSON is the explanation payload: exactly the list entry's
// fields (embedded, so the two can never drift) plus the attempts array.
// Attempts has NO omitempty — a decision whose attempts are all absent still
// reports `"attempts": []`, so the client never has to distinguish "no
// attempts" from "this response shape lacks the field".
type routeExplanationJSON struct {
	routeDecisionJSON
	Attempts []routeAttemptJSON `json:"attempts"`
}

// nullableString maps "" (this reader's representation of a SQL NULL) to a
// nil pointer, so an absent value serializes as JSON null rather than as an
// empty string that reads like a real, known value.
func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func toRouteDecisionJSON(d observability.RouteDecision) routeDecisionJSON {
	groupKeys := d.CandidateSummary.GroupKeys
	if groupKeys == nil {
		groupKeys = []string{}
	}
	reasons := d.ExclusionReasons
	if reasons == nil {
		reasons = map[string]int{}
	}
	return routeDecisionJSON{
		RequestID:             d.RequestID,
		DecisionID:            d.ID,
		Tier:                  d.Tier,
		WorkloadProfileBucket: d.WorkloadProfileBucket,
		CreatedAt:             d.CreatedAt.UTC().Format(time.RFC3339),
		Candidates: routeCandidatesJSON{
			Total:          d.CandidateSummary.TotalCandidates,
			EligibleGroups: d.CandidateSummary.EligibleGroups,
			GroupKeys:      groupKeys,
		},
		ExclusionReasons: reasons,
		Chosen: routeChosenJSON{
			ProviderID:      nullableString(d.ChosenProviderID),
			ProviderModelID: nullableString(d.ChosenProviderModelID),
			Funding:         nullableString(d.ChosenFunding),
		},
		// A nil Scores map serializes as null: "no scores recorded" is not the
		// same claim as "scored, with no dimensions".
		Scores: d.Scores,
		Thinking: routeThinkingJSON{
			Requested:        nullableString(d.RequestedThinking),
			Applied:          nullableString(d.AppliedThinking),
			TierClamped:      d.TierClamped,
			CertifiedClamped: d.CertifiedClamped,
		},
	}
}

func toRouteAttemptJSON(a observability.RouteAttempt) routeAttemptJSON {
	out := routeAttemptJSON{
		Attempt:             a.AttemptNumber,
		ProviderID:          a.ProviderID,
		AccountID:           a.AccountID,
		OfferingOperationID: a.OfferingOperationID,
		// Status is already normalized to the closed vocabulary by the reader;
		// projecting it as-is can never carry free provider text.
		Status:          string(a.Status),
		LatencyMS:       a.LatencyMS,
		ThinkingClamped: a.ThinkingClamped,
		ReservationID:   nullableString(a.ReservationID),
		StartedAt:       a.StartedAt.UTC().Format(time.RFC3339),
	}
	if a.FinishedAt != nil {
		s := a.FinishedAt.UTC().Format(time.RFC3339)
		out.FinishedAt = &s
	}
	return out
}

// routeListOffset reads the shared pagination cursor as this endpoint's
// offset. GET /accounts' cursor is an account id because its list is keyset-
// ordered by id; a newest-first time-ordered list has no such stable single
// column, so the opaque cursor token here is a decimal offset. The WIRE shape
// is identical either way (`?cursor=` in, `meta.next_cursor` out), so a client
// pages both endpoints with the same loop. An unparsable or negative cursor
// starts from the beginning — pagination inputs are advisory, exactly as
// parsePageParams already treats `limit`.
func routeListOffset(cursor string) int {
	if cursor == "" {
		return 0
	}
	n, err := strconv.Atoi(cursor)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// ServeRoutes implements GET /api/control/v1/diagnostics/routes (09 §3.9):
// the paginated route-decision list, newest first. A read — no audit event
// (mirrors ServeList above).
func (h *DiagnosticsHandler) ServeRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}
	if h.routes == nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	page := parsePageParams(r, defaultPageLimit, maxPageLimit)
	offset := routeListOffset(page.Cursor)

	decisions, err := h.routes.ListDecisions(r.Context(), page.Limit, offset)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	out := make([]routeDecisionJSON, 0, len(decisions))
	for _, d := range decisions {
		out = append(out, toRouteDecisionJSON(d))
	}

	// A full page advertises the next offset; a short page is the last page
	// and omits meta entirely (paginationMeta's own contract).
	nextCursor := ""
	if len(decisions) == page.Limit && page.Limit > 0 {
		nextCursor = strconv.Itoa(offset + page.Limit)
	}
	writeDataMeta(w, http.StatusOK, out, paginationMeta(nextCursor))
}

// ServeRouteExplanation implements GET
// /api/control/v1/diagnostics/routes/{request_id} (09 §3.9): the decision
// plus its attempts. An unknown request id is a typed 404 — never a 500, and
// never an empty 200 (which a dashboard would render as "nothing was
// considered").
func (h *DiagnosticsHandler) ServeRouteExplanation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}
	if h.routes == nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	requestID := r.PathValue("request_id")
	explanation, err := h.routes.GetExplanation(r.Context(), requestID)
	if errors.Is(err, observability.ErrRouteDecisionNotFound) {
		// The reader's error text names the request id and its own package;
		// the client gets a fixed, internal-free message instead.
		writeAuthError(w, http.StatusNotFound, "not_found", "route decision not found", false)
		return
	}
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	out := routeExplanationJSON{
		routeDecisionJSON: toRouteDecisionJSON(explanation.Decision),
		Attempts:          make([]routeAttemptJSON, 0, len(explanation.Attempts)),
	}
	for _, a := range explanation.Attempts {
		out.Attempts = append(out.Attempts, toRouteAttemptJSON(a))
	}
	writeData(w, http.StatusOK, out)
}

// reconciliationAllocationJSON is one allocation's diagnostic projection
// (05 §4: ids/costs/confidence only — no prompt/response content, ever).
type reconciliationAllocationJSON struct {
	WindowID         string   `json:"window_id"`
	Unit             string   `json:"unit"`
	EstimatedCost    float64  `json:"estimated_cost"`
	ActualCost       *float64 `json:"actual_cost"`
	ActualConfidence *string  `json:"actual_confidence"`
	State            string   `json:"state"`
}

// reconciliationItemJSON is one reservation's diagnostic projection.
type reconciliationItemJSON struct {
	ReservationID     string                         `json:"reservation_id"`
	AccountID         string                         `json:"account_id"`
	RequestID         string                         `json:"request_id"`
	AttemptID         string                         `json:"attempt_id"`
	State             string                         `json:"state"`
	Attempts          int                            `json:"attempts"`
	Leased            bool                           `json:"leased"`
	DispatchedAt      *int64                         `json:"dispatched_at"`
	ExpiresAt         int64                          `json:"expires_at"`
	RebaselineFlagged bool                           `json:"rebaseline_flagged"`
	Allocations       []reconciliationAllocationJSON `json:"allocations"`
}

func toReconciliationItemJSON(it storage.ReconciliationItem) reconciliationItemJSON {
	allocs := make([]reconciliationAllocationJSON, 0, len(it.Allocations))
	for _, a := range it.Allocations {
		allocs = append(allocs, reconciliationAllocationJSON{
			WindowID:         a.WindowID,
			Unit:             a.Unit,
			EstimatedCost:    a.EstimatedCost,
			ActualCost:       a.ActualCost,
			ActualConfidence: a.ActualConfidence,
			State:            a.State,
		})
	}
	return reconciliationItemJSON{
		ReservationID:     it.ReservationID,
		AccountID:         it.AccountID,
		RequestID:         it.RequestID,
		AttemptID:         it.AttemptID,
		State:             it.State,
		Attempts:          it.Attempts,
		Leased:            it.Leased,
		DispatchedAt:      it.DispatchedAt,
		ExpiresAt:         it.ExpiresAt,
		RebaselineFlagged: it.RebaselineFlagged,
		Allocations:       allocs,
	}
}

// ServeList implements GET /api/control/v1/diagnostics/reconciliation (09
// §2 / 05 §4): the reconciliation_pending / unknown_consumption read
// model. This is a read — no audit event is emitted (mirrors GET
// /settings, GET /accounts).
func (h *DiagnosticsHandler) ServeList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	page := parsePageParams(r, defaultPageLimit, maxPageLimit)
	items, nextCursor, err := h.reconciliation.ListReconciliationItems(r.Context(), page.Limit, page.Cursor)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	out := make([]reconciliationItemJSON, 0, len(items))
	for _, it := range items {
		out = append(out, toReconciliationItemJSON(it))
	}
	writeDataMeta(w, http.StatusOK, out, paginationMeta(nextCursor))
}

// reconciliationActionRequest is POST .../reconciliation/{id}'s request
// body.
type reconciliationActionRequest struct {
	Action string `json:"action"`
}

// ServeAction implements POST
// /api/control/v1/diagnostics/reconciliation/{reservation_id} (09 §2 /
// 05 §4 "Manual recovery"): {"action":"resync"|"accept_estimate"}.
func (h *DiagnosticsHandler) ServeAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	reservationID := r.PathValue("reservation_id")

	var body reconciliationActionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErrorDetails(w, http.StatusBadRequest, "validation_error", "malformed request body", false, nil)
		return
	}
	if body.Action != "resync" && body.Action != "accept_estimate" {
		writeErrorDetails(w, http.StatusBadRequest, "validation_error", "action must be resync or accept_estimate", false, nil)
		return
	}

	ctx := r.Context()
	state, accountID, ok, err := h.reconciliation.ReservationStateAndAccount(ctx, reservationID)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if !ok {
		writeAuthError(w, http.StatusNotFound, "not_found", "reservation not found", false)
		return
	}

	auditAction := AuditActionReconciliationResync
	if body.Action == "accept_estimate" {
		auditAction = AuditActionReconciliationAcceptEstimate
	}

	if state == string(quota.ReservationUnknownConsumption) {
		h.audit.Emit(ctx, auditAction, AuditResultFailure, AuditResourceReservation, reservationID, "reservation_terminal")
		writeErrorDetails(w, http.StatusConflict, "reservation_terminal",
			"this reservation reached the terminal unknown_consumption state and no manual action can un-terminalize it; use POST /accounts/{id}/quota to re-baseline the account's windows",
			false, nil)
		return
	}
	if state != string(quota.ReservationReconciliationPending) {
		h.audit.Emit(ctx, auditAction, AuditResultFailure, AuditResourceReservation, reservationID, "not_reconciliation_pending")
		writeErrorDetails(w, http.StatusConflict, "reservation_terminal", "this reservation is not reconciliation_pending", false, nil)
		return
	}

	switch body.Action {
	case "resync":
		if err := h.reconciliation.ResetForResync(ctx, reservationID); err != nil {
			writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
			return
		}
	case "accept_estimate":
		if err := h.lifecycle.SettleEstimate(ctx, reservationID); err != nil {
			if errors.Is(err, storage.ErrReservationNotFound) {
				writeAuthError(w, http.StatusNotFound, "not_found", "reservation not found", false)
				return
			}
			writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
			return
		}
	}

	h.audit.Emit(ctx, auditAction, AuditResultSuccess, AuditResourceReservation, reservationID, "")
	writeData(w, http.StatusOK, map[string]any{"reservation_id": reservationID, "account_id": accountID, "action": body.Action})
}

package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

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
}

// NewDiagnosticsHandler builds the handler over the shared
// ReconciliationRepo/QuotaLifecycleRepo instances ControlMux also wires
// for the quota-refresh route.
func NewDiagnosticsHandler(reconciliation *storage.ReconciliationRepo, lifecycle *storage.QuotaLifecycleRepo, audit *auditEmitter) *DiagnosticsHandler {
	return &DiagnosticsHandler{reconciliation: reconciliation, lifecycle: lifecycle, audit: audit}
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

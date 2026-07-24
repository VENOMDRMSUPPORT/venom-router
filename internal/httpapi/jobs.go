package httpapi

import (
	"net/http"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// JobsHandler serves the single canonical async-job status surface
// (P2b-JOBS-001, 09 §1/§3.12): GET /jobs/{job_id}. No per-resource
// status endpoint exists or may be added alongside it — every
// long-running operation reports through this one route.
type JobsHandler struct {
	jobs *storage.JobRepo
}

// NewJobsHandler builds the handler over db's existing connection.
func NewJobsHandler(db *storage.DB) *JobsHandler {
	return &JobsHandler{jobs: storage.NewJobRepo(db)}
}

type jobErrorJSON struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type jobJSON struct {
	JobID      string        `json:"job_id"`
	Kind       string        `json:"kind"`
	Status     string        `json:"status"`
	StartedAt  *string       `json:"started_at,omitempty"`
	FinishedAt *string       `json:"finished_at,omitempty"`
	ResultRef  string        `json:"result_ref,omitempty"`
	Error      *jobErrorJSON `json:"error,omitempty"`
}

func toJobJSON(row storage.JobRow) jobJSON {
	out := jobJSON{
		JobID:     row.ID,
		Kind:      row.Kind,
		Status:    string(row.Status),
		ResultRef: row.ResultRef,
	}
	if row.StartedAt != nil {
		s := row.StartedAt.Format(time.RFC3339)
		out.StartedAt = &s
	}
	if row.FinishedAt != nil {
		s := row.FinishedAt.Format(time.RFC3339)
		out.FinishedAt = &s
	}
	if row.Error != nil {
		out.Error = &jobErrorJSON{Code: row.Error.Code, Message: row.Error.Message}
	}
	return out
}

// ServeGet implements GET /jobs/{job_id} (09 §3.12): the job's current
// status, idempotently — polling never mutates anything. Unknown id ->
// not_found (404). This route sits behind ownerSessionGate
// (P2b-CAPI-001) via ControlMux; it performs no auth of its own.
func (h *JobsHandler) ServeGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}

	id := r.PathValue("job_id")
	row, ok, err := h.jobs.GetByID(r.Context(), id)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}
	if !ok {
		writeAuthError(w, http.StatusNotFound, "not_found", "job not found", false)
		return
	}

	writeData(w, http.StatusOK, toJobJSON(row))
}

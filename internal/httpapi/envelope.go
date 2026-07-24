package httpapi

import "net/http"

// writeData writes the canonical success envelope (09 §1: `{"data": ...}`)
// with no meta. It is an additive, more explicit alias over writeAuthJSON
// for CAPI-002-onward endpoints — existing auth call sites keep using
// writeAuthJSON/their own map literals unchanged, so this does not churn
// any existing response construction.
func writeData(w http.ResponseWriter, status int, data any) {
	writeAuthJSON(w, status, map[string]any{"data": data})
}

// writeDataMeta writes the success envelope with an additional top-level
// "meta" object (09 §1) — e.g. cursor-pagination's next_cursor. meta is
// omitted entirely when nil, rather than serialized as "meta": null.
func writeDataMeta(w http.ResponseWriter, status int, data any, meta any) {
	body := map[string]any{"data": data}
	if meta != nil {
		body["meta"] = meta
	}
	writeAuthJSON(w, status, body)
}

// writeErrorDetails writes the shared error envelope (09 §1) with an
// optional "details" object — e.g. a validation error's per-field
// breakdown. This is additive alongside writeAuthError (unchanged, and
// still the right choice for the common no-details case); details is
// omitted entirely when nil.
func writeErrorDetails(w http.ResponseWriter, status int, code, message string, retryable bool, details any) {
	errBody := map[string]any{
		"code":       code,
		"message":    message,
		"request_id": newRequestID(),
		"retryable":  retryable,
	}
	if details != nil {
		errBody["details"] = details
	}
	writeAuthJSON(w, status, map[string]any{"error": errBody})
}

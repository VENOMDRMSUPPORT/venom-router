package httpapi

import "github.com/VENOMDRMSUPPORT/venom-router/internal/sanitize"

// redactedFields runs every value in fields through internal/sanitize's
// key-based redaction boundary (P1-SEC-005) and returns a JSON-ready
// map — the reusable shape a control-plane endpoint uses whenever a
// response could otherwise echo back submitted or observed content
// (09 §1). This package does not implement a second redactor: it only
// adapts sanitize.Value's (key, value) shape to the map an endpoint
// typically wants to fold into its response body.
func redactedFields(fields map[string]string) map[string]any {
	out := make(map[string]any, len(fields))
	for k, v := range fields {
		out[k] = sanitize.Value(k, v)
	}
	return out
}

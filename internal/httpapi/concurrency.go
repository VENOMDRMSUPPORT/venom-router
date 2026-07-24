package httpapi

import (
	"net/http"
	"strings"
)

// ifMatchVersion reads the request's If-Match header and strips the
// optional surrounding quotes ETags conventionally carry (e.g.
// `"v3"` -> `v3`), so callers compare against their own plain version
// strings without each having to know the quoting convention.
func ifMatchVersion(r *http.Request) string {
	return strings.Trim(r.Header.Get("If-Match"), `"`)
}

// requireMatchingVersion is the reusable optimistic-concurrency gate
// (09 §1): a mutating endpoint for an editable resource (funding,
// settings, keys — wired up by later units) reads the caller-supplied
// version (from If-Match via ifMatchVersion, or an equivalent "version"
// field the endpoint's own request body carries) and compares it
// against the resource's current version. A mismatch is rejected with
// precondition_failed (412) before any write; a match lets the caller
// proceed. An empty provided value (no precondition supplied) is
// treated as a mismatch here — callers that want an optional
// precondition should skip calling this at all when provided == "".
func requireMatchingVersion(w http.ResponseWriter, provided, expected string) bool {
	if provided == "" || provided != expected {
		writeErrorDetails(w, http.StatusPreconditionFailed, "precondition_failed", "resource has changed", false, nil)
		return false
	}
	return true
}

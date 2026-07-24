package httpapi

import (
	"net/http"
	"strconv"
)

// defaultPageLimit and maxPageLimit bound cursor pagination (09 §1)
// when a list endpoint doesn't need a different ceiling.
const (
	defaultPageLimit = 50
	maxPageLimit     = 200
)

// pageParams is one request's resolved pagination inputs.
type pageParams struct {
	Limit  int
	Cursor string
}

// parsePageParams reads `?limit=&cursor=` from r, clamping limit to
// (0, maxLimit] and falling back to defaultLimit for a missing or
// non-positive value. It never errors: an unparsable limit is simply
// ignored in favor of the default, since pagination inputs are
// advisory, not validated request bodies.
func parsePageParams(r *http.Request, defaultLimit, maxLimit int) pageParams {
	q := r.URL.Query()

	limit := defaultLimit
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	return pageParams{Limit: limit, Cursor: q.Get("cursor")}
}

// paginationMeta builds the `meta` object a paginated list response
// carries (09 §1: `meta.next_cursor`). An empty nextCursor means this
// is the last page, and meta is nil (writeDataMeta then omits "meta"
// entirely) rather than an empty next_cursor string.
func paginationMeta(nextCursor string) any {
	if nextCursor == "" {
		return nil
	}
	return map[string]any{"next_cursor": nextCursor}
}

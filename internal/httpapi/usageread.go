package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// usageread.go serves GET /api/control/v1/usage (P6-CAPI-EXTRA-2, enables
// P6-UI-005): the consumption read model over usage_records, per 05 §4/§7.
//
// ─── THE ONE RULE ───────────────────────────────────────────────────────────
//
// A NULL metric is UNKNOWN, and an unknown never becomes a 0 on this wire.
//
// usage_records' numeric columns are all nullable and are legitimately NULL in
// production — a request that failed before the provider answered has no token
// count to record. So every summed dimension is served as FOUR fields, and each
// one exists to prevent a specific lie:
//
//	sum            null when no contributing row reported a value. A 0 would claim
//	               a measured absence of consumption.
//	known_count    how many rows reported one — the ONLY honest denominator.
//	unknown_count  how many did not. Without it the dashboard cannot distinguish
//	               a total from a floor, so it would present a floor as a total.
//	average        sum / known_count, or null. Dividing by the REQUEST count would
//	               drag the average down with rows that measured nothing.
//
// `truncated` says the bounded scan stopped early, which makes every number below
// it a floor as well.
//
// Secret-free by construction: the projection carries ids, counts, tier names and
// epoch timestamps only. api_key_id is deliberately NOT projected — key
// attribution belongs to the keys surface and this aggregate has no need of it.

// UsageHandler serves the consumption aggregate. Owner-session + CSRF gated via
// ControlMux's `gated`; a read, so no audit event (mirrors GET /accounts,
// GET /settings, GET /diagnostics/*).
type UsageHandler struct {
	usage *storage.UsageRecordRepo
}

// NewUsageHandler builds the handler over the usage repository.
func NewUsageHandler(usage *storage.UsageRecordRepo) *UsageHandler {
	return &UsageHandler{usage: usage}
}

// usageMetricJSON is one summed dimension. Sum and Average are POINTERS so an
// unknown serializes as JSON null; neither is ever omitted, because an absent key
// and a null read differently to a client and only one of them is the truth here.
type usageMetricJSON struct {
	Sum          *int     `json:"sum"`
	Average      *float64 `json:"average"`
	KnownCount   int      `json:"known_count"`
	UnknownCount int      `json:"unknown_count"`
}

// toUsageMetricJSON projects one metric, computing the average over
// KnownCount — never over the group's request count.
func toUsageMetricJSON(m storage.UsageMetric) usageMetricJSON {
	out := usageMetricJSON{
		Sum:          m.Sum,
		KnownCount:   m.KnownCount,
		UnknownCount: m.UnknownCount,
	}
	// An average needs both a sum and at least one row that reported a value. With
	// KnownCount == 0 there is no average to state, so it stays null rather than
	// becoming 0 (and the division is never attempted).
	if m.Sum != nil && m.KnownCount > 0 {
		avg := float64(*m.Sum) / float64(m.KnownCount)
		out.Average = &avg
	}
	return out
}

// usageGroupJSON is one grouping bucket.
//
// Key is a POINTER: it is null for the UNATTRIBUTED bucket, because account_id and
// provider_model_id are nullable columns and a row can genuinely belong to no
// account or no model. Folding those rows into a named group would attribute
// consumption to something that did not incur it; dropping them would understate
// the total. Requests is a row count and is therefore always known.
type usageGroupJSON struct {
	Key       *string         `json:"key"`
	Requests  int             `json:"requests"`
	TokensIn  usageMetricJSON `json:"tokens_in"`
	TokensOut usageMetricJSON `json:"tokens_out"`
	LatencyMS usageMetricJSON `json:"latency_ms"`
}

func toUsageGroupJSON(g storage.UsageGroup) usageGroupJSON {
	return usageGroupJSON{
		Key:       g.Key,
		Requests:  g.Requests,
		TokensIn:  toUsageMetricJSON(g.TokensIn),
		TokensOut: toUsageMetricJSON(g.TokensOut),
		LatencyMS: toUsageMetricJSON(g.LatencyMS),
	}
}

// usageWindowJSON echoes the window that was actually applied, as epoch seconds.
// Both ends are nullable: an unbounded request reports null rather than
// substituting "now" or the epoch, either of which would be a window the caller
// never asked for.
type usageWindowJSON struct {
	From *int64 `json:"from"`
	To   *int64 `json:"to"`
}

// usageAggregateJSON is the response body.
type usageAggregateJSON struct {
	Window    usageWindowJSON `json:"window"`
	Scanned   int             `json:"scanned"`
	Limit     int             `json:"limit"`
	Truncated bool            `json:"truncated"`

	Totals    usageGroupJSON   `json:"totals"`
	ByAccount []usageGroupJSON `json:"by_account"`
	ByModel   []usageGroupJSON `json:"by_model"`
	ByTier    []usageGroupJSON `json:"by_tier"`
}

// parseUsageInstant reads an epoch-seconds query parameter. An absent or
// unparsable value is nil (unbounded) — window inputs are advisory, exactly as
// parsePageParams already treats `limit`, rather than a 400 that would block a
// dashboard over a stray character.
func parseUsageInstant(raw string) *time.Time {
	if raw == "" {
		return nil
	}
	secs, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil
	}
	t := time.Unix(secs, 0).UTC()
	return &t
}

func usageGroupsJSON(groups []storage.UsageGroup) []usageGroupJSON {
	// Non-nil so an empty grouping marshals as [] rather than null.
	out := make([]usageGroupJSON, 0, len(groups))
	for _, g := range groups {
		out = append(out, toUsageGroupJSON(g))
	}
	return out
}

// ServeUsage implements GET /api/control/v1/usage.
//
// Optional `?from=` / `?to=` are epoch seconds bounding created_at (from
// inclusive, to exclusive — the half-open window, so adjacent windows neither
// overlap nor gap). `?limit=` bounds how many ROWS may be scanned, through the
// same shared helper every other list uses.
func (h *UsageHandler) ServeUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", false)
		return
	}
	if h.usage == nil {
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	page := parsePageParams(r, defaultPageLimit, maxPageLimit)
	query := storage.UsageQuery{
		From:  parseUsageInstant(r.URL.Query().Get("from")),
		To:    parseUsageInstant(r.URL.Query().Get("to")),
		Limit: page.Limit,
	}

	agg, err := h.usage.Aggregate(r.Context(), query)
	if err != nil {
		// No partial aggregate: an empty one renders as "no usage", which is the
		// worst possible reading of "we could not ask".
		writeAuthError(w, http.StatusInternalServerError, "internal", "internal error", true)
		return
	}

	out := usageAggregateJSON{
		Scanned:   agg.Scanned,
		Limit:     agg.Limit,
		Truncated: agg.Truncated,
		Totals:    toUsageGroupJSON(agg.Totals),
		ByAccount: usageGroupsJSON(agg.ByAccount),
		ByModel:   usageGroupsJSON(agg.ByModel),
		ByTier:    usageGroupsJSON(agg.ByTier),
	}
	if agg.From != nil {
		secs := agg.From.Unix()
		out.Window.From = &secs
	}
	if agg.To != nil {
		secs := agg.To.Unix()
		out.Window.To = &secs
	}

	writeData(w, http.StatusOK, out)
}

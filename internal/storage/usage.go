package storage

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

// UsageRecordRepo writes usage_records rows over M7 (00012_api_keys_usage.sql).
//
// Unlike observability.RouteRecorder — which logs-and-swallows a write error by
// design for route records — a usage write is NEVER swallowed: usage is billing
// truth recorded on every terminal path, and the old build's bug was dropping
// it silently (05 §7, card P5-PAPI-002). Insert returns its error so the caller
// surfaces it.
//
// Every column is a correlation id, tier, typed status, or nullable numeric
// metric — there is NO column for a prompt, response, token content, raw
// provider error, or Authorization header (the M7 table has none by design).
type UsageRecordRepo struct {
	db *DB
}

// NewUsageRecordRepo builds the repository over db's existing connection.
func NewUsageRecordRepo(db *DB) *UsageRecordRepo { return &UsageRecordRepo{db: db} }

// UsageRecord is one usage_records row. Pointer fields are nil ⇒ NULL (unknown),
// never a sentinel zero.
type UsageRecord struct {
	ID               string
	RequestID        string
	APIKeyID         *string
	Tier             string
	ProviderID       *string
	AccountID        *string
	ProviderModelID  *string
	Funding          *string
	Status           string
	LatencyMS        *int
	TokensIn         *int
	TokensOut        *int
	FallbackAttempts *int
	CreatedAt        time.Time
}

// Insert appends one usage_records row. The error is returned (never
// swallowed) so the caller can surface a persistence failure.
func (r *UsageRecordRepo) Insert(ctx context.Context, rec UsageRecord) error {
	_, err := r.db.Conn().ExecContext(ctx,
		`INSERT INTO usage_records
		    (id, request_id, api_key_id, tier, provider_id, account_id, provider_model_id, funding, status, latency_ms, tokens_in, tokens_out, fallback_attempts, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.RequestID, nullString(rec.APIKeyID), rec.Tier,
		nullString(rec.ProviderID), nullString(rec.AccountID), nullString(rec.ProviderModelID), nullString(rec.Funding),
		rec.Status, nullInt(rec.LatencyMS), nullInt(rec.TokensIn), nullInt(rec.TokensOut), nullInt(rec.FallbackAttempts),
		rec.CreatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("storage: insert usage record %q: %w", rec.ID, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Read side (P6-CAPI-EXTRA-2, enables P6-UI-005): the consumption aggregate
// GET /usage serves, per 05 §4/§7.
//
// THE WHOLE DESIGN OF THIS READER IS ONE RULE: a NULL metric is UNKNOWN, and an
// unknown never becomes a zero anywhere along the way.
//
// usage_records' numeric columns are all nullable, and they are legitimately NULL
// in production — a request that failed before the provider answered has no token
// count to record. Summing those rows as 0 would understate consumption while
// looking authoritative, and counting them in an average's denominator would drag
// the average down with rows that measured nothing. So every summed dimension
// carries THREE numbers (see UsageMetric): the sum of the rows that reported a
// value, how many those were, and how many did not report at all. The third is
// what lets the dashboard say "3 of 40 requests report no token count" instead of
// silently presenting a floor as a total.
// ---------------------------------------------------------------------------

// UsageMetric is one summed numeric dimension over a group of usage rows.
//
// Sum is nil when NO contributing row reported a value — "unknown", which is a
// different claim from a measured 0. KnownCount is the correct denominator for an
// average (never the row count); UnknownCount is how many rows reported nothing
// for this dimension, tallied per dimension because a row can report a latency
// and no token count.
type UsageMetric struct {
	Sum          *int
	KnownCount   int
	UnknownCount int
}

// observe folds one row's nullable value into the metric.
func (m *UsageMetric) observe(v *int) {
	if v == nil {
		m.UnknownCount++
		return
	}
	m.KnownCount++
	if m.Sum == nil {
		total := 0
		m.Sum = &total
	}
	*m.Sum += *v
}

// UsageGroup is one grouping bucket's consumption.
//
// Key is nil for the UNATTRIBUTED bucket: account_id and provider_model_id are
// nullable columns, so a row can genuinely belong to no account or no model.
// Folding such a row into a named group would attribute consumption to something
// that did not incur it, and dropping it would understate the total — an explicit
// unattributed bucket is the only honest option. (Tier is NOT NULL, so tier groups
// always have a key.)
//
// Requests is a ROW count and is therefore always known: a row exists whatever its
// metrics say.
type UsageGroup struct {
	Key       *string
	Requests  int
	TokensIn  UsageMetric
	TokensOut UsageMetric
	LatencyMS UsageMetric
}

// observe folds one row into this group.
func (g *UsageGroup) observe(tokensIn, tokensOut, latency *int) {
	g.Requests++
	g.TokensIn.observe(tokensIn)
	g.TokensOut.observe(tokensOut)
	g.LatencyMS.observe(latency)
}

// UsageQuery bounds an Aggregate call. From is inclusive, To is exclusive (the
// standard half-open window, so adjacent windows neither overlap nor gap). Limit
// bounds how many ROWS may be scanned.
type UsageQuery struct {
	From  *time.Time
	To    *time.Time
	Limit int
}

// UsageAggregate is the consumption read model.
//
// Truncated says the scan stopped at Limit, which makes every count and sum below
// a FLOOR rather than a total. A silently-capped aggregate presented as complete
// is the difference between "you used 40k tokens" and "you used at least 40k".
type UsageAggregate struct {
	From, To  *time.Time
	Scanned   int
	Limit     int
	Truncated bool

	Totals    UsageGroup
	ByAccount []UsageGroup
	ByModel   []UsageGroup
	ByTier    []UsageGroup
}

// groupAccumulator collects buckets keyed by a nullable string, preserving a
// deterministic output order (first appearance, then sorted) so repeated calls
// agree.
type groupAccumulator struct {
	byKey        map[string]*UsageGroup
	unattributed *UsageGroup
}

func newGroupAccumulator() *groupAccumulator {
	return &groupAccumulator{byKey: map[string]*UsageGroup{}}
}

func (a *groupAccumulator) observe(key *string, tokensIn, tokensOut, latency *int) {
	if key == nil {
		if a.unattributed == nil {
			a.unattributed = &UsageGroup{}
		}
		a.unattributed.observe(tokensIn, tokensOut, latency)
		return
	}
	g, ok := a.byKey[*key]
	if !ok {
		k := *key
		g = &UsageGroup{Key: &k}
		a.byKey[*key] = g
	}
	g.observe(tokensIn, tokensOut, latency)
}

// groups returns the buckets sorted by key, with the unattributed bucket LAST so
// it never displaces a named row from the top of a table.
func (a *groupAccumulator) groups() []UsageGroup {
	keys := make([]string, 0, len(a.byKey))
	for k := range a.byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]UsageGroup, 0, len(keys)+1)
	for _, k := range keys {
		out = append(out, *a.byKey[k])
	}
	if a.unattributed != nil {
		out = append(out, *a.unattributed)
	}
	return out
}

// Aggregate reads at most Limit usage rows in the given window and returns the
// consumption aggregate: overall totals plus per-account, per-model and per-tier
// groups.
//
// It aggregates in Go rather than with SQL GROUP BY, deliberately. The per-column
// unknown tallies are the point of this read model, and expressing "sum only the
// non-NULL values, count them, and separately count the NULLs, for three columns,
// across four groupings" in SQL would need a dozen conditional aggregates whose
// correctness is far harder to see than the explicit fold below. Row volume for an
// owner console is small and the scan is bounded either way.
func (r *UsageRecordRepo) Aggregate(ctx context.Context, q UsageQuery) (UsageAggregate, error) {
	limit := q.Limit
	if limit < 0 {
		limit = 0
	}

	clauses := ""
	args := []any{}
	if q.From != nil {
		clauses += " AND created_at >= ?"
		args = append(args, q.From.Unix())
	}
	if q.To != nil {
		clauses += " AND created_at < ?"
		args = append(args, q.To.Unix())
	}
	// limit+1 detects the overflow without ever returning the extra row.
	args = append(args, limit+1)

	rows, err := r.db.Conn().QueryContext(ctx,
		`SELECT tier, account_id, provider_model_id, tokens_in, tokens_out, latency_ms
		 FROM usage_records
		 WHERE 1 = 1`+clauses+`
		 ORDER BY created_at ASC, id ASC
		 LIMIT ?`,
		args...,
	)
	if err != nil {
		return UsageAggregate{}, fmt.Errorf("storage: aggregate usage: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := UsageAggregate{From: q.From, To: q.To, Limit: limit}
	accounts, modelsAcc, tiers := newGroupAccumulator(), newGroupAccumulator(), newGroupAccumulator()

	for rows.Next() {
		if out.Scanned == limit {
			// The (limit+1)-th row: proof of overflow, never folded in.
			out.Truncated = true
			break
		}
		var (
			tier                         string
			account, model               sql.NullString
			tokensIn, tokensOut, latency sql.NullInt64
		)
		if err := rows.Scan(&tier, &account, &model, &tokensIn, &tokensOut, &latency); err != nil {
			return UsageAggregate{}, fmt.Errorf("storage: aggregate usage: scan: %w", err)
		}
		out.Scanned++

		in, outTok, lat := usageNullableInt(tokensIn), usageNullableInt(tokensOut), usageNullableInt(latency)
		out.Totals.observe(in, outTok, lat)
		accounts.observe(usageNullableString(account), in, outTok, lat)
		modelsAcc.observe(usageNullableString(model), in, outTok, lat)
		tierKey := tier
		tiers.observe(&tierKey, in, outTok, lat)
	}
	if err := rows.Err(); err != nil {
		return UsageAggregate{}, fmt.Errorf("storage: aggregate usage: %w", err)
	}

	out.ByAccount = accounts.groups()
	out.ByModel = modelsAcc.groups()
	out.ByTier = tiers.groups()
	return out, nil
}

// usageNullableString / usageNullableInt lift a scanned NULL to a nil pointer —
// the representation this file's whole unknown discipline rests on. They are named
// apart from discovery.go's nullableString, which converts in the OPPOSITE
// direction (Go value -> SQL arg) and treats "" as NULL.
func usageNullableString(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func usageNullableInt(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int64)
	return &n
}

func nullString(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

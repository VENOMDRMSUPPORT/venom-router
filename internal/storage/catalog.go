package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// defaultCatalogListLimit bounds a ListOfferings call that did not supply a
// sane limit.
const defaultCatalogListLimit = 50

// CatalogOperationRow is one offering_operations row joined with its 1:1
// certifications row (04 §3/§5). AccountID and ProviderModelID identify the
// parent offering — GetOperationCertification's caller (P3a-CAPI-002's
// certification read) needs them to render the full response without a
// second lookup.
type CatalogOperationRow struct {
	ID                   string
	AccountID            string
	ProviderModelID      string
	Operation            string
	CertificationStatus  string
	CapabilityTruth      string
	CertificationVersion int
	CertifiedAt          *time.Time
	EvidenceRef          string
}

// CatalogOfferingRow is one account_model_offerings row joined with its
// parent models row (04 §3's canonical-vs-offering split) plus every
// offering_operations/certifications row scoped to it. Every nullable
// column decodes to nil/empty-non-nil exactly as persisted — this repo
// never fabricates a 0/false/empty value for an unknown fact (04 §2:
// "absent ≠ false, absent ≠ zero").
type CatalogOfferingRow struct {
	AccountID       string
	ProviderID      string
	ProviderModelID string
	ModelID         string
	Availability    string
	ContextLength   *int
	MaxInputTokens  *int
	MaxOutputTokens *int
	Capabilities    []string
	Pricing         map[string]any
	FirstSeenAt     time.Time
	LastSeenAt      time.Time

	ModelDisplayName    string
	NativeContextTokens *int
	NativeModalities    []string
	QualityRating       *float64

	Operations []CatalogOperationRow
}

// CatalogListParams is ListOfferings' input. AccountID = "" lists every
// account's offerings; a non-empty value restricts to that one account.
// LiveOnly applies the operational owner-console contract: the offering is
// available, its account is connected, healthy and not reauthenticating, AND
// the offering has a certified+supported chat offering_operation (the honest
// gate, universal-probes-and-honest-gate Task 9 — an unverified model's chat
// capability is not "live" just because discovery observed it).
type CatalogListParams struct {
	AccountID string
	LiveOnly  bool
	Limit     int
	Cursor    string
}

// CatalogRepo is a READ-ONLY repository over the frozen M4 catalog tables
// (models, account_model_offerings, offering_operations, certifications).
// It never writes — DiscoveryRepo (internal/storage/discovery.go) owns
// every mutation to these tables. It returns plain storage rows; the
// intelligence.Project projection is built by its httpapi caller
// (P3a-CAPI-001's ModelsHandler), never re-derived here.
type CatalogRepo struct {
	db *DB
}

// NewCatalogRepo builds a repository over db's existing connection.
func NewCatalogRepo(db *DB) *CatalogRepo {
	return &CatalogRepo{db: db}
}

// ListOfferings returns up to params.Limit offerings ordered deterministically
// by (account_id, provider_model_id) — the tie-breaker on provider_model_id
// is what makes the order (and therefore the cursor) unambiguous when
// listing across every account. nextCursor is "" on the last page.
func (r *CatalogRepo) ListOfferings(ctx context.Context, params CatalogListParams) ([]CatalogOfferingRow, string, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = defaultCatalogListLimit
	}

	var (
		query strings.Builder
		args  []any
		conds []string
	)
	query.WriteString(`SELECT amo.account_id, amo.provider_id, amo.provider_model_id, amo.model_id, amo.availability,
		amo.context_length, amo.max_input_tokens, amo.max_output_tokens,
		amo.capabilities_json, amo.pricing_json, amo.first_seen_at, amo.last_seen_at,
		m.display_name, m.native_context_tokens, m.native_modalities_json, m.quality_rating
	FROM account_model_offerings amo
	JOIN models m ON amo.model_id = m.id`)

	if params.AccountID != "" {
		conds = append(conds, "amo.account_id = ?")
		args = append(args, params.AccountID)
	}
	if params.LiveOnly {
		conds = append(conds, `amo.availability = 'available'
			AND EXISTS (
				SELECT 1 FROM accounts a
				WHERE a.id = amo.account_id
				  AND a.connection_state = 'connected'
				  AND a.health_state = 'healthy'
				  AND a.reauth_in_progress = 0
			)
			AND EXISTS (
				SELECT 1
				FROM offering_operations oo
				JOIN certifications c ON c.offering_operation_id = oo.id
				WHERE oo.account_id = amo.account_id
				  AND oo.provider_model_id = amo.provider_model_id
				  AND oo.operation = 'chat'
				  AND c.status = 'certified'
				  AND c.capability_truth = 'supported'
			)`)
	}
	if params.Cursor != "" {
		cursorAccountID, cursorProviderModelID, ok := decodeCatalogCursor(params.Cursor)
		if ok {
			conds = append(conds, "(amo.account_id > ? OR (amo.account_id = ? AND amo.provider_model_id > ?))")
			args = append(args, cursorAccountID, cursorAccountID, cursorProviderModelID)
		}
	}
	if len(conds) > 0 {
		query.WriteString(" WHERE " + strings.Join(conds, " AND "))
	}
	query.WriteString(" ORDER BY amo.account_id ASC, amo.provider_model_id ASC LIMIT ?")
	args = append(args, limit+1) // over-fetch by one to detect a next page

	rows, err := r.db.Conn().QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, "", fmt.Errorf("storage: list offerings: %w", err)
	}

	var out []CatalogOfferingRow
	overFetched := false
	for rows.Next() {
		row, err := scanCatalogOfferingRow(rows)
		if err != nil {
			_ = rows.Close()
			return nil, "", fmt.Errorf("storage: list offerings: scan: %w", err)
		}
		if len(out) == limit {
			// This is the limit+1'th row: it exists only to prove a next
			// page follows. It is deliberately NOT included in out and NOT
			// used as the cursor basis below — the cursor must resume
			// strictly AFTER the last row actually returned (out's last
			// element), not after this one, or the next page would skip it.
			overFetched = true
			break
		}
		out = append(out, row)
	}
	rowsErr := rows.Err()
	// The connection pool is single-connection (storage.Open sets
	// SetMaxOpenConns(1)): rows MUST be closed before operationsForOffering
	// issues its own queries below, or the second query deadlocks waiting
	// for the connection this still-open cursor holds.
	if err := rows.Close(); err != nil {
		return nil, "", fmt.Errorf("storage: list offerings: close: %w", err)
	}
	if rowsErr != nil {
		return nil, "", fmt.Errorf("storage: list offerings: %w", rowsErr)
	}

	for i := range out {
		ops, err := r.operationsForOffering(ctx, out[i].AccountID, out[i].ProviderModelID)
		if err != nil {
			return nil, "", err
		}
		out[i].Operations = ops
	}

	nextCursor := ""
	if overFetched {
		last := out[len(out)-1]
		nextCursor = encodeCatalogCursor(last.AccountID, last.ProviderModelID)
	}
	return out, nextCursor, nil
}

// operationsForOffering reads every offering_operations row (joined with
// its certifications row) for one (accountID, providerModelID) offering.
func (r *CatalogRepo) operationsForOffering(ctx context.Context, accountID, providerModelID string) ([]CatalogOperationRow, error) {
	rows, err := r.db.Conn().QueryContext(ctx,
		`SELECT oo.id, oo.account_id, oo.provider_model_id, oo.operation, c.status, c.capability_truth, c.version, c.certified_at, c.evidence_ref
		 FROM offering_operations oo
		 LEFT JOIN certifications c ON c.offering_operation_id = oo.id
		 WHERE oo.account_id = ? AND oo.provider_model_id = ?
		 ORDER BY oo.operation ASC`,
		accountID, providerModelID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: list offering operations for (%q,%q): %w", accountID, providerModelID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []CatalogOperationRow
	for rows.Next() {
		op, err := scanCatalogOperationRow(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: list offering operations for (%q,%q): scan: %w", accountID, providerModelID, err)
		}
		out = append(out, op)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list offering operations for (%q,%q): %w", accountID, providerModelID, err)
	}
	return out, nil
}

// GetOperationCertification reads one offering_operations row joined with
// its certification, by offering_operation_id — the identity
// P3a-CAPI-002's `GET /offerings/{id}/certification` is keyed on (04 §5:
// certification is per offering-operation, and the frozen M4
// certifications table's own primary key IS offering_operation_id). ok is
// false when no such offering_operations row exists.
func (r *CatalogRepo) GetOperationCertification(ctx context.Context, offeringOperationID string) (CatalogOperationRow, bool, error) {
	row := r.db.Conn().QueryRowContext(ctx,
		`SELECT oo.id, oo.account_id, oo.provider_model_id, oo.operation, c.status, c.capability_truth, c.version, c.certified_at, c.evidence_ref
		 FROM offering_operations oo
		 LEFT JOIN certifications c ON c.offering_operation_id = oo.id
		 WHERE oo.id = ?`,
		offeringOperationID,
	)
	op, err := scanCatalogOperationRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CatalogOperationRow{}, false, nil
	}
	if err != nil {
		return CatalogOperationRow{}, false, fmt.Errorf("storage: get operation certification %q: %w", offeringOperationID, err)
	}
	return op, true, nil
}

// ChatOfferingToVerify is one free chat offering-operation awaiting a
// usability probe: the certification row id to drive and the provider model id
// to probe. It is the storage-side shape the per-account usability run consumes.
type ChatOfferingToVerify struct {
	OfferingOperationID string
	ProviderModelID     string
}

// ListChatOfferingsToVerify returns the account's chat offering-operations
// whose certification is in `probing` — the rows the usability sweep must
// execute. The existing ReviewDrainer already moves `observed` chat rows to
// `probing` (it drains by state, not by operation) and nothing then executes
// them, so `probing` is exactly where chat rows strand and where this sweep
// picks them up; `observed` is deliberately left to the drainer. Non-chat
// operations, chat ops already certified/suspended/expired, and other accounts'
// rows are all excluded by the query.
func (r *CatalogRepo) ListChatOfferingsToVerify(ctx context.Context, accountID string) ([]ChatOfferingToVerify, error) {
	rows, err := r.db.Conn().QueryContext(ctx,
		`SELECT oo.id, oo.provider_model_id
		 FROM offering_operations oo
		 JOIN certifications c ON c.offering_operation_id = oo.id
		 WHERE oo.account_id = ? AND oo.operation = 'chat' AND c.status = 'probing'
		 ORDER BY oo.provider_model_id ASC`,
		accountID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: list chat offerings to verify for %q: %w", accountID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []ChatOfferingToVerify
	for rows.Next() {
		var v ChatOfferingToVerify
		if err := rows.Scan(&v.OfferingOperationID, &v.ProviderModelID); err != nil {
			return nil, fmt.Errorf("storage: list chat offerings to verify for %q: scan: %w", accountID, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list chat offerings to verify for %q: %w", accountID, err)
	}
	return out, nil
}

// ListObservedChatOfferings returns the account's chat offering-operations
// whose certification is still `observed` — the exact complement of
// ListChatOfferingsToVerify, and the rows the FAST LANE must drive across the
// observed -> probing edge itself.
//
// Discovery seeds every freshly discovered chat operation at `observed`
// (DiscoveryRepo.recordEvidenceObserved), and the observed -> probing edge
// belongs to the probe_drain scheduler tick. That is fine for the STEADY-STATE
// sweep — by the time it runs, the drainer has already moved the rows — but the
// fast lane fires within milliseconds of a successful discovery, when every
// fresh row is still `observed` and ListChatOfferingsToVerify therefore returns
// nothing at all. This lister is how the fast lane sees that work; it does NOT
// change the scheduled sweep's probing-only contract.
func (r *CatalogRepo) ListObservedChatOfferings(ctx context.Context, accountID string) ([]ChatOfferingToVerify, error) {
	rows, err := r.db.Conn().QueryContext(ctx,
		`SELECT oo.id, oo.provider_model_id
		 FROM offering_operations oo
		 JOIN certifications c ON c.offering_operation_id = oo.id
		 WHERE oo.account_id = ? AND oo.operation = 'chat' AND c.status = 'observed'
		 ORDER BY oo.provider_model_id ASC`,
		accountID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: list observed chat offerings for %q: %w", accountID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []ChatOfferingToVerify
	for rows.Next() {
		var v ChatOfferingToVerify
		if err := rows.Scan(&v.OfferingOperationID, &v.ProviderModelID); err != nil {
			return nil, fmt.Errorf("storage: list observed chat offerings for %q: scan: %w", accountID, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list observed chat offerings for %q: %w", accountID, err)
	}
	return out, nil
}

// NonChatOperationToCertify is one declared non-chat capability (tools, vision,
// …) awaiting certification-from-declaration.
type NonChatOperationToCertify struct {
	OfferingOperationID string
	Operation           string
}

// ListNonChatOperationsToCertify returns the account's NON-chat offering-
// operations whose certification is in `probing`. Unlike chat — which the
// usability sweep verifies with a live runtime probe — these capabilities have
// no runtime prober, so the drainer strands them in `probing` forever, which is
// what makes every model read as "needs review". An offering-operation's mere
// existence is NOT by itself the provider's declaration: discovery also
// creates CANDIDATE rows (Task 3 / clinepass) for operations the provider
// never declared, purely so they stay probeable. The one honest signal for
// "the provider declared this" is that the operation string appears in the
// parent account_model_offerings.capabilities_json — that column is written
// from DiscoveredModel.Capabilities only (never from CandidateOperations), so
// the EXISTS/json_each clause below is exactly the declared/candidate
// boundary. A candidate row is therefore left stranded in `probing` — read as
// "needs review" — until a REAL probe (POST /offerings/{id}/probe, or Test
// All) certifies it; certifying it here from "declaration" would be
// fabricating evidence that was never given. Same `probing`-only contract as
// ListChatOfferingsToVerify otherwise: `observed` is left to the drainer, and
// already-certified/suspended/expired rows and other accounts are excluded.
//
// JSON1/json_each availability was confirmed against the modernc.org/sqlite
// driver actually in use (a throwaway query test, since deleted — see
// task-3-report.md) before this SQL-side filter was written; had it been
// unavailable, the fallback would be filtering in Go after fetching the
// offering's declared capabilities_json separately.
func (r *CatalogRepo) ListNonChatOperationsToCertify(ctx context.Context, accountID string) ([]NonChatOperationToCertify, error) {
	rows, err := r.db.Conn().QueryContext(ctx,
		`SELECT oo.id, oo.operation
		 FROM offering_operations oo
		 JOIN certifications c ON c.offering_operation_id = oo.id
		 JOIN account_model_offerings amo
		   ON amo.account_id = oo.account_id AND amo.provider_model_id = oo.provider_model_id
		 WHERE oo.account_id = ? AND oo.operation != 'chat' AND c.status = 'probing'
		   AND EXISTS (
		     SELECT 1 FROM json_each(amo.capabilities_json)
		     WHERE json_each.value = oo.operation
		   )
		 ORDER BY oo.provider_model_id ASC, oo.operation ASC`,
		accountID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: list non-chat operations to certify for %q: %w", accountID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []NonChatOperationToCertify
	for rows.Next() {
		var v NonChatOperationToCertify
		if err := rows.Scan(&v.OfferingOperationID, &v.Operation); err != nil {
			return nil, fmt.Errorf("storage: list non-chat operations to certify for %q: scan: %w", accountID, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: list non-chat operations to certify for %q: %w", accountID, err)
	}
	return out, nil
}

// catalogRowScanner is the shared shape of *sql.Row's and *sql.Rows' Scan
// method (mirrors storage.scanner in accounts.go).
type catalogRowScanner interface {
	Scan(dest ...any) error
}

func scanCatalogOfferingRow(s catalogRowScanner) (CatalogOfferingRow, error) {
	var (
		out                                            CatalogOfferingRow
		contextLength, maxInputTokens, maxOutputTokens sql.NullInt64
		capabilitiesJSON, pricingJSON                  sql.NullString
		firstSeenAt, lastSeenAt                        int64
		displayName                                    sql.NullString
		nativeContextTokens                            sql.NullInt64
		nativeModalitiesJSON                           sql.NullString
		qualityRating                                  sql.NullFloat64
	)
	if err := s.Scan(
		&out.AccountID, &out.ProviderID, &out.ProviderModelID, &out.ModelID, &out.Availability,
		&contextLength, &maxInputTokens, &maxOutputTokens,
		&capabilitiesJSON, &pricingJSON, &firstSeenAt, &lastSeenAt,
		&displayName, &nativeContextTokens, &nativeModalitiesJSON, &qualityRating,
	); err != nil {
		return CatalogOfferingRow{}, err
	}

	out.ContextLength = nullIntToPtr(contextLength)
	out.MaxInputTokens = nullIntToPtr(maxInputTokens)
	out.MaxOutputTokens = nullIntToPtr(maxOutputTokens)
	out.Capabilities = decodeJSONStringSlice(capabilitiesJSON)
	out.Pricing = decodeJSONStringMap(pricingJSON)
	out.FirstSeenAt = time.Unix(firstSeenAt, 0).UTC()
	out.LastSeenAt = time.Unix(lastSeenAt, 0).UTC()
	out.ModelDisplayName = displayName.String
	out.NativeContextTokens = nullIntToPtr(nativeContextTokens)
	out.NativeModalities = decodeJSONStringSlice(nativeModalitiesJSON)
	if qualityRating.Valid {
		v := qualityRating.Float64
		out.QualityRating = &v
	}
	return out, nil
}

func scanCatalogOperationRow(s catalogRowScanner) (CatalogOperationRow, error) {
	var (
		out           CatalogOperationRow
		status, truth sql.NullString
		version       sql.NullInt64
		certifiedAt   sql.NullInt64
		evidenceRef   sql.NullString
	)
	if err := s.Scan(&out.ID, &out.AccountID, &out.ProviderModelID, &out.Operation, &status, &truth, &version, &certifiedAt, &evidenceRef); err != nil {
		return CatalogOperationRow{}, err
	}

	// A certifications row exists 1:1 for every offering_operations row
	// this codebase creates (DiscoveryRepo.ensureOfferingOperation always
	// inserts the baseline together with its parent) — the LEFT JOIN and
	// these defaults are a defense-in-depth fallback, never the expected
	// path, and fail closed to the "discovered/unknown" baseline rather
	// than fabricating a certified/supported claim.
	out.CertificationStatus = "discovered"
	out.CapabilityTruth = "unknown"
	out.CertificationVersion = 1
	if status.Valid {
		out.CertificationStatus = status.String
	}
	if truth.Valid {
		out.CapabilityTruth = truth.String
	}
	if version.Valid {
		out.CertificationVersion = int(version.Int64)
	}
	if certifiedAt.Valid {
		t := time.Unix(certifiedAt.Int64, 0).UTC()
		out.CertifiedAt = &t
	}
	out.EvidenceRef = evidenceRef.String
	return out, nil
}

func nullIntToPtr(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int64)
	return &n
}

// decodeJSONStringSlice decodes a nullable JSON TEXT column into a []string,
// resolving NULL and any malformed/non-array content to nil (unknown) —
// never a partial or fabricated result, and never an error that would fail
// the whole list (04 §2: unknown stays unknown).
func decodeJSONStringSlice(s sql.NullString) []string {
	if !s.Valid {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s.String), &out); err != nil {
		return nil
	}
	return out
}

// decodeJSONStringMap decodes a nullable JSON TEXT column into a
// map[string]any, resolving NULL and any malformed/non-object content to
// nil (unknown) — same fail-closed contract as decodeJSONStringSlice.
func decodeJSONStringMap(s sql.NullString) map[string]any {
	if !s.Valid {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(s.String), &out); err != nil {
		return nil
	}
	return out
}

// catalogCursorSeparator joins the cursor's two components before
// base64-encoding. NUL cannot appear in either an account id or a
// provider_model_id in practice, but the encoding is opaque to the client
// either way.
const catalogCursorSeparator = "\x00"

func encodeCatalogCursor(accountID, providerModelID string) string {
	raw := accountID + catalogCursorSeparator + providerModelID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCatalogCursor(cursor string) (accountID, providerModelID string, ok bool) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", "", false
	}
	parts := strings.SplitN(string(raw), catalogCursorSeparator, 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// ---------------------------------------------------------------------------
// Canonical-model identity + quality rating (P6-CAPI-001, 04 §3)
//
// 04 §3: "Canonical model — provider-scoped identity + native facts... The
// identity key = SHA-256(provider_id, provider_model_id)". So one models row
// corresponds to one (provider_id, provider_model_id) pair, recorded in
// provider_model_aliases. The benchmark endpoint needs that pair to look the
// model up in an analysis leaderboard, and needs somewhere to put the
// resolved rating — both live here, next to the models-table reads.
// ---------------------------------------------------------------------------

// CanonicalModelRow is one models row plus the provider identity pair it is
// keyed by. QualityRating is nil when no quality signal has ever been
// resolved for this model — 04 §3: "NULL means 'no quality signal
// available'", never 0.
type CanonicalModelRow struct {
	ModelID         string
	ProviderID      string
	ProviderModelID string
	DisplayName     string
	QualityRating   *float64
}

// GetCanonicalModel resolves modelID to its canonical identity. ok=false
// means either the model does not exist or it has no provider alias yet — in
// both cases there is no (provider_id, provider_model_id) pair to look up, so
// the caller must treat it as absent rather than guessing one.
//
// The ORDER BY makes the answer deterministic if a model somehow carries more
// than one alias (the schema permits it even though 04 §3's identity rule
// implies exactly one).
func (r *CatalogRepo) GetCanonicalModel(ctx context.Context, modelID string) (CanonicalModelRow, bool, error) {
	var (
		out         CanonicalModelRow
		displayName sql.NullString
		rating      sql.NullFloat64
	)
	err := r.db.Conn().QueryRowContext(ctx,
		`SELECT m.id, a.provider_id, a.provider_model_id, m.display_name, m.quality_rating
		 FROM models m
		 JOIN provider_model_aliases a ON a.model_id = m.id
		 WHERE m.id = ?
		 ORDER BY a.provider_id ASC, a.provider_model_id ASC
		 LIMIT 1`,
		modelID,
	).Scan(&out.ModelID, &out.ProviderID, &out.ProviderModelID, &displayName, &rating)
	if errors.Is(err, sql.ErrNoRows) {
		return CanonicalModelRow{}, false, nil
	}
	if err != nil {
		return CanonicalModelRow{}, false, fmt.Errorf("storage: get canonical model: %w", err)
	}

	out.DisplayName = displayName.String
	if rating.Valid {
		v := rating.Float64
		out.QualityRating = &v
	}
	return out, true, nil
}

// ErrModelNotFound is returned by SetQualityRating when modelID matches no
// models row, so a write against a vanished model is a typed error rather
// than a silent no-op.
var ErrModelNotFound = errors.New("storage: model not found")

// SetQualityRating persists a RESOLVED canonical quality rating (04 §3's
// 0-100 scalar) onto modelID, stamping updated_at.
//
// There is deliberately no "clear the rating" path here and no zero-value
// default: this method is only ever called with a value the precedence engine
// actually resolved. A model with no signal keeps whatever it had — 04 §3's
// "NULL means no quality signal available" is a state the caller reaches by
// NOT calling this, never by writing 0.
func (r *CatalogRepo) SetQualityRating(ctx context.Context, modelID string, rating float64, now time.Time) error {
	res, err := r.db.Conn().ExecContext(ctx,
		`UPDATE models SET quality_rating = ?, updated_at = ? WHERE id = ?`,
		rating, now.Unix(), modelID,
	)
	if err != nil {
		return fmt.Errorf("storage: set quality rating: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: set quality rating: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %q", ErrModelNotFound, modelID)
	}
	return nil
}

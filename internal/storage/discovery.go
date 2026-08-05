package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/intelligence"
)

// DiscoveryRepo implements intelligence.GenerationAllocator and
// intelligence.SnapshotApplier over the frozen M4 discovery tables
// (models, provider_model_aliases, account_model_offerings,
// offering_operations, certifications, discovery_runs). It is the ONE
// place a discovery run's generation is allocated and its snapshot is
// atomically applied. storage importing intelligence types is the
// permitted direction (internal/staticgate enforces the reverse never
// holds) — this repo never makes a domain decision itself (which model is
// valid, which run wins on a tie), it only durably records the decision
// intelligence.DiscoveryService already made.
type DiscoveryRepo struct {
	db    *DB
	newID func() string
}

// NewDiscoveryRepo builds a repository over db's existing connection.
// newID mints fresh row ids for the new models/offering_operations rows
// this repo creates; it defaults to a crypto/rand-backed high-entropy
// generator when nil (mirroring httpapi's newOAuthTransactionID), so no
// third-party id package is introduced by this unit.
func NewDiscoveryRepo(db *DB, newID func() string) *DiscoveryRepo {
	if newID == nil {
		newID = randomDiscoveryID
	}
	return &DiscoveryRepo{db: db, newID: newID}
}

func randomDiscoveryID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read is documented to never fail on this module's
		// supported platforms; this fallback only avoids a panic in the
		// theoretical case it ever does.
		return fmt.Sprintf("discovery-fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// BeginRun allocates the next generation for accountID — strictly greater
// than any generation already recorded for that account, derived from the
// table itself (the UNIQUE(account_id, generation) constraint is the
// structural backstop against a concurrent racing allocation) — and
// inserts a 'running' discovery_runs row in the same transaction.
func (r *DiscoveryRepo) BeginRun(ctx context.Context, accountID, runID string, now time.Time) (int64, error) {
	tx, err := r.db.Conn().BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("storage: begin discovery run tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded

	var maxGen sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT MAX(generation) FROM discovery_runs WHERE account_id = ?`, accountID,
	).Scan(&maxGen); err != nil {
		return 0, fmt.Errorf("storage: read max discovery generation for %q: %w", accountID, err)
	}
	generation := int64(1)
	if maxGen.Valid {
		generation = maxGen.Int64 + 1
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO discovery_runs (id, account_id, generation, status, started_at) VALUES (?, ?, ?, 'running', ?)`,
		runID, accountID, generation, now.Unix(),
	); err != nil {
		return 0, fmt.Errorf("storage: insert discovery run %q: %w", runID, err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("storage: begin discovery run %q: commit: %w", runID, err)
	}
	return generation, nil
}

// MarkFailed marks runID's discovery_runs row 'failed' with reasonCode. It
// touches no other table — no offering, alias, model, or certification row
// is ever read or written by this method — which is what makes "keep the
// previous snapshot intact on failure" true by construction rather than by
// careful ordering.
func (r *DiscoveryRepo) MarkFailed(ctx context.Context, runID, reasonCode string, now time.Time) error {
	if _, err := r.db.Conn().ExecContext(ctx,
		`UPDATE discovery_runs SET status = 'failed', reason_code = ?, finished_at = ? WHERE id = ?`,
		reasonCode, now.Unix(), runID,
	); err != nil {
		return fmt.Errorf("storage: mark discovery run %q failed: %w", runID, err)
	}
	return nil
}

// Apply performs 04 §1 step 5's generation-guarded atomic snapshot apply,
// entirely inside one transaction:
//
//  1. Read MAX(generation) for snapshot.AccountID. If a strictly higher
//     generation is already on record (a newer run was allocated, whether
//     or not it has applied yet), this run has lost the race: its
//     discovery_runs row is marked 'superseded', no offering is touched,
//     and (false, nil) is returned.
//  2. Otherwise this run is still the newest: the snapshot is written (see
//     below) and the discovery_runs row is marked 'applied'.
//
// A non-withdraw snapshot is treated as authoritative for the account's
// *complete* current model list, not merely additive: every model in
// snapshot.Models is upserted as 'available', and any offering previously
// 'available' for (AccountID, ProviderID) that this run did NOT report is
// marked 'withdrawn'. This generalizes 04 §1's "explicit empty list is
// authoritative" rule to every successful run — otherwise a model the
// account can no longer see would linger as 'available' indefinitely
// rather than only when the provider happens to report zero models.
func (r *DiscoveryRepo) Apply(ctx context.Context, runID string, snapshot intelligence.DiscoverySnapshot, now time.Time) (bool, error) {
	tx, err := r.db.Conn().BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("storage: begin discovery apply tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded

	var maxGen sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT MAX(generation) FROM discovery_runs WHERE account_id = ?`, snapshot.AccountID,
	).Scan(&maxGen); err != nil {
		return false, fmt.Errorf("storage: read max discovery generation for %q: %w", snapshot.AccountID, err)
	}
	if maxGen.Valid && snapshot.Generation < maxGen.Int64 {
		if _, err := tx.ExecContext(ctx,
			`UPDATE discovery_runs SET status = 'superseded', finished_at = ? WHERE id = ?`,
			now.Unix(), runID,
		); err != nil {
			return false, fmt.Errorf("storage: mark discovery run %q superseded: %w", runID, err)
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("storage: mark discovery run %q superseded: commit: %w", runID, err)
		}
		return false, nil
	}

	epoch := now.Unix()
	if snapshot.Withdraw {
		if _, err := tx.ExecContext(ctx,
			`UPDATE account_model_offerings SET availability = 'withdrawn' WHERE account_id = ? AND provider_id = ?`,
			snapshot.AccountID, snapshot.ProviderID,
		); err != nil {
			return false, fmt.Errorf("storage: withdraw offerings for %q: %w", snapshot.AccountID, err)
		}
	} else {
		seen := make(map[string]bool, len(snapshot.Models))
		for _, m := range snapshot.Models {
			seen[m.ProviderModelID] = true
			if err := r.applyModel(ctx, tx, snapshot.AccountID, snapshot.ProviderID, m, epoch); err != nil {
				return false, err
			}
		}
		if err := r.withdrawMissing(ctx, tx, snapshot.AccountID, snapshot.ProviderID, seen); err != nil {
			return false, err
		}
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE discovery_runs SET status = 'applied', finished_at = ? WHERE id = ?`,
		epoch, runID,
	); err != nil {
		return false, fmt.Errorf("storage: mark discovery run %q applied: %w", runID, err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("storage: apply discovery snapshot: commit: %w", err)
	}
	return true, nil
}

// withdrawMissing marks 'withdrawn' every account_model_offerings row for
// (accountID, providerID) that is not currently 'withdrawn' and whose
// provider_model_id is absent from seen (the snapshot just applied).
func (r *DiscoveryRepo) withdrawMissing(ctx context.Context, tx *sql.Tx, accountID, providerID string, seen map[string]bool) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT provider_model_id FROM account_model_offerings WHERE account_id = ? AND provider_id = ? AND availability != 'withdrawn'`,
		accountID, providerID,
	)
	if err != nil {
		return fmt.Errorf("storage: list existing offerings for %q: %w", accountID, err)
	}
	var stale []string
	for rows.Next() {
		var pmID string
		if err := rows.Scan(&pmID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("storage: scan existing offering provider_model_id: %w", err)
		}
		if !seen[pmID] {
			stale = append(stale, pmID)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("storage: list existing offerings for %q: %w", accountID, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("storage: list existing offerings for %q: close: %w", accountID, err)
	}

	for _, pmID := range stale {
		if _, err := tx.ExecContext(ctx,
			`UPDATE account_model_offerings SET availability = 'withdrawn' WHERE account_id = ? AND provider_model_id = ?`,
			accountID, pmID,
		); err != nil {
			return fmt.Errorf("storage: withdraw stale offering (%q,%q): %w", accountID, pmID, err)
		}
	}
	return nil
}

// applyModel upserts one snapshot model's full row set: the canonical
// models identity (created once per canonical key, never overwritten by a
// later run — canonical native facts are a later enrichment unit's
// concern, not discovery's), the provider_model_aliases mapping, the
// account_model_offerings row, and — for each of the model's Operations —
// an offering_operations row plus (only for a brand-new offering_operation)
// a 'discovered'-baseline certifications row.
func (r *DiscoveryRepo) applyModel(ctx context.Context, tx *sql.Tx, accountID, providerID string, m intelligence.DiscoverySnapshotModel, epoch int64) error {
	modelID, err := r.ensureModel(ctx, tx, m.CanonicalKey, m.DisplayName, epoch)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO provider_model_aliases (provider_id, provider_model_id, model_id) VALUES (?, ?, ?)
		 ON CONFLICT(provider_id, provider_model_id) DO UPDATE SET model_id = excluded.model_id`,
		providerID, m.ProviderModelID, modelID,
	); err != nil {
		return fmt.Errorf("storage: upsert alias (%q,%q): %w", providerID, m.ProviderModelID, err)
	}

	capabilitiesJSON, err := marshalJSONColumn(m.Capabilities)
	if err != nil {
		return fmt.Errorf("storage: marshal capabilities for %q: %w", m.ProviderModelID, err)
	}
	pricingJSON, err := marshalJSONColumn(m.Pricing)
	if err != nil {
		return fmt.Errorf("storage: marshal pricing for %q: %w", m.ProviderModelID, err)
	}
	// account_model_offerings has no dedicated evidence column in the
	// frozen M4 schema; the sanitized Evidence map is persisted into the
	// existing generic lifecycle_json column — the only other free-form
	// JSON column on this table — rather than fabricating a new column
	// against a frozen migration.
	evidenceJSON, err := marshalJSONColumn(m.Evidence)
	if err != nil {
		return fmt.Errorf("storage: marshal evidence for %q: %w", m.ProviderModelID, err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO account_model_offerings
		    (account_id, provider_id, provider_model_id, model_id, availability, context_length, max_input_tokens, max_output_tokens, capabilities_json, pricing_json, lifecycle_json, first_seen_at, last_seen_at)
		 VALUES (?, ?, ?, ?, 'available', ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(account_id, provider_model_id) DO UPDATE SET
		    availability = 'available',
		    model_id = excluded.model_id,
		    context_length = excluded.context_length,
		    max_input_tokens = excluded.max_input_tokens,
		    max_output_tokens = excluded.max_output_tokens,
		    capabilities_json = excluded.capabilities_json,
		    pricing_json = excluded.pricing_json,
		    lifecycle_json = excluded.lifecycle_json,
		    last_seen_at = excluded.last_seen_at`,
		accountID, providerID, m.ProviderModelID, modelID,
		intPtrArg(m.ContextLength), intPtrArg(m.MaxInputTokens), intPtrArg(m.MaxOutputTokens),
		capabilitiesJSON, pricingJSON, evidenceJSON, epoch, epoch,
	); err != nil {
		return fmt.Errorf("storage: upsert offering (%q,%q): %w", accountID, m.ProviderModelID, err)
	}

	// An offering_operations row is written for the UNION of declared
	// (m.Operations) and candidate (m.CandidateOperations) operations — a
	// candidate exists purely so the capability stays probeable (Task 3) —
	// but capabilities_json above was derived from m.Capabilities alone, so
	// a candidate's row is never mistaken for a declaration. A duplicate
	// operation string in both lists is declared: seenOps is seeded by the
	// declared loop first, so the candidate loop skips it and no second row
	// is attempted (ensureOfferingOperation upserts on natural key regardless,
	// so this dedupe is a clarity/efficiency measure, not a correctness one).
	seenOps := make(map[string]bool, len(m.Operations)+len(m.CandidateOperations))
	for _, op := range m.Operations {
		if seenOps[string(op)] {
			continue
		}
		seenOps[string(op)] = true
		if err := r.ensureOfferingOperation(ctx, tx, accountID, providerID, m.ProviderModelID, string(op), epoch); err != nil {
			return err
		}
	}
	for _, op := range m.CandidateOperations {
		if seenOps[string(op)] {
			continue
		}
		seenOps[string(op)] = true
		if err := r.ensureOfferingOperation(ctx, tx, accountID, providerID, m.ProviderModelID, string(op), epoch); err != nil {
			return err
		}
	}
	return nil
}

// ensureModel returns the models.id for canonicalKey, inserting a new
// identity-only row (no native facts — those belong to a later enrichment
// unit) if none exists yet. An existing row's display_name is left
// untouched: discovery registers identity, it does not overwrite whatever
// enrichment or a prior run already recorded.
func (r *DiscoveryRepo) ensureModel(ctx context.Context, tx *sql.Tx, canonicalKey, displayName string, epoch int64) (string, error) {
	var existingID string
	err := tx.QueryRowContext(ctx, `SELECT id FROM models WHERE canonical_key_sha256 = ?`, canonicalKey).Scan(&existingID)
	if err == nil {
		return existingID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("storage: lookup model by canonical key: %w", err)
	}

	id := r.newID()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO models (id, canonical_key_sha256, display_name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		id, canonicalKey, nullableString(displayName), epoch, epoch,
	); err != nil {
		return "", fmt.Errorf("storage: insert model %q: %w", canonicalKey, err)
	}
	return id, nil
}

// ensureOfferingOperation upserts one offering_operations row keyed by its
// natural identity (account_id, provider_model_id, operation). A BRAND NEW
// row gets its 'discovered'-baseline certifications row and nothing more —
// the very first time discovery ever sees an offering-operation, that
// sighting IS the baseline, not yet "recorded evidence" for something
// already on file (P3a's own acceptance gates pin this: a single
// first-ever discovery run leaves every certification at
// discovered/unknown). An ALREADY-EXISTING row instead calls
// recordEvidenceObserved: THIS re-discovery is "concrete evidence...
// recorded" for an offering-operation already known (04 §5 edge 1), so it
// advances a still-'discovered' certification to 'observed'. Either way,
// an offering-operation a probe has since progressed past 'observed'
// (toward 'certified', or into 'suspended'/'expired') is left completely
// untouched — re-discovery must never reset that progress back.
func (r *DiscoveryRepo) ensureOfferingOperation(ctx context.Context, tx *sql.Tx, accountID, providerID, providerModelID, operation string, epoch int64) error {
	var existingID string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM offering_operations WHERE account_id = ? AND provider_model_id = ? AND operation = ?`,
		accountID, providerModelID, operation,
	).Scan(&existingID)
	switch {
	case err == nil:
		return r.recordEvidenceObserved(ctx, tx, existingID, epoch)
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("storage: lookup offering_operation (%q,%q,%q): %w", accountID, providerModelID, operation, err)
	}

	id := r.newID()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO offering_operations (id, account_id, provider_id, provider_model_id, operation, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, accountID, providerID, providerModelID, operation, epoch, epoch,
	); err != nil {
		return fmt.Errorf("storage: insert offering_operation (%q,%q,%q): %w", accountID, providerModelID, operation, err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO certifications (offering_operation_id, status, capability_truth, version, created_at, updated_at) VALUES (?, 'discovered', 'unknown', 1, ?, ?)`,
		id, epoch, epoch,
	); err != nil {
		return fmt.Errorf("storage: insert certification baseline for %q: %w", id, err)
	}
	// GOVERNOR CORRECTION (P3c-CERT-008): edge 1 fires on THIS snapshot too,
	// not only on a later re-discovery. 04 §5 edge 1's trigger names
	// "discovery snapshot / provider metadata" literally — but this method's
	// two callers (the declared m.Operations loop and the CANDIDATE
	// m.CandidateOperations loop, Task 3, above) both insert through here, so
	// a fresh row's mere existence is NOT by itself the provider's
	// declaration; it is only evidence that a discovery snapshot happened at
	// all, which is what the discovered->observed edge actually requires.
	// Whether an operation was ACTUALLY declared is decided elsewhere:
	// catalog.go's ListNonChatOperationsToCertify reads
	// account_model_offerings.capabilities_json (written from
	// m.Capabilities only, never from CandidateOperations) as the one
	// honest declared/candidate boundary, which is what stops a candidate
	// row from ever being certified off mere discovery. Firing only on the
	// already-existing-row branch would mean a freshly discovered
	// offering-operation could never be probed until the owner triggered
	// discovery a SECOND time (nothing schedules discovery), an
	// undiscoverable requirement no document states.
	return r.recordEvidenceObserved(ctx, tx, id, epoch)
}

// recordEvidenceObserved advances edge 1 (discovered -> observed, 04 §5)
// for offeringOperationID INSIDE tx — the same transaction the snapshot
// itself is written in, never a second connection (P3c-CERT-008: the
// pool is SetMaxOpenConns(1); acquiring a second connection while tx
// holds the only one would deadlock). This is a direct SQL mirror of
// intelligence.CertificationDriver.Observe's one edge, not a call through
// that port — Observe's backing CertificationStore (CertificationRepo)
// uses db.Conn() independently of tx and would deadlock exactly that way
// if invoked here.
//
// The `status = 'discovered'` guard is what makes this both idempotent (a
// re-discovery of an already-observed-or-later row matches zero rows —
// a no-op, never an error) and safe against ever resetting a row a probe
// has since progressed past 'observed' — the same invariant
// ensureOfferingOperation's existing-row branch has always upheld, now
// upheld by this guard instead of by never running at all. It never
// touches version or capability_truth — those remain
// models.Certification.Transition's exclusive concern (edge 1's own
// Transition call carries neither), so this SQL only ever sets status and
// updated_at.
func (r *DiscoveryRepo) recordEvidenceObserved(ctx context.Context, tx *sql.Tx, offeringOperationID string, epoch int64) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE certifications SET status = 'observed', updated_at = ? WHERE offering_operation_id = ? AND status = 'discovered'`,
		epoch, offeringOperationID,
	); err != nil {
		return fmt.Errorf("storage: advance certification %q to observed: %w", offeringOperationID, err)
	}
	return nil
}

// SetNativeContextTokens persists a VERIFIED native context-window limit
// (04 §2/§3) onto modelID's canonical models row, stamping updated_at. This
// is the write-back that flips models.EffectiveContext's provenance to
// `native` for a probed model: the context probe already extracts the real
// limit from a provider's rejection (contextprobe.go's ExtractContextLimit
// ladder), and until this method existed nothing ever persisted it.
//
// tokens must be strictly positive: a non-positive limit is never a fact
// (04 §2: "a zero/negative declared limit fails the record rather than
// being stored"), so this method rejects it and leaves the row completely
// untouched — the ExtractContextLimit ladder already guarantees it never
// hands back a non-positive value, but this guard holds regardless of what
// a caller passes.
//
// A modelID matching no row is ErrModelNotFound, not a silent no-op — the
// same typed-error contract CatalogRepo.SetQualityRating already uses for
// an identical "write against a vanished model" case.
//
// This is the ONLY writer of models.native_context_tokens anywhere in this
// codebase — the dashboard's "native"-provenance checkmark on a context
// badge (ModelsSurface.tsx) is only ever honest because this method, and
// nothing else, ever sets that column.
func (r *DiscoveryRepo) SetNativeContextTokens(ctx context.Context, modelID string, tokens int) error {
	if tokens <= 0 {
		return fmt.Errorf("storage: native context tokens must be positive, got %d", tokens)
	}
	res, err := r.db.Conn().ExecContext(ctx,
		`UPDATE models SET native_context_tokens = ?, updated_at = ? WHERE id = ?`,
		tokens, time.Now().Unix(), modelID,
	)
	if err != nil {
		return fmt.Errorf("storage: set native context tokens: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: set native context tokens: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %q", ErrModelNotFound, modelID)
	}
	return nil
}

// marshalJSONColumn marshals v (either []string or map[string]any) into a
// nullable TEXT column value: a nil slice/map yields SQL NULL, anything
// else (including an empty-but-non-nil slice/map) yields its JSON text.
func marshalJSONColumn(v any) (sql.NullString, error) {
	switch t := v.(type) {
	case []string:
		if t == nil {
			return sql.NullString{}, nil
		}
	case map[string]any:
		if t == nil {
			return sql.NullString{}, nil
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}

func intPtrArg(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

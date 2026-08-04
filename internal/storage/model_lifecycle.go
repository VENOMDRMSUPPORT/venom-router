package storage

import (
	"context"
	"fmt"
)

// ModelLifecyclePurgeResult reports exactly what one cleanup sweep removed.
// It is intentionally aggregate-only: model ids and provider payloads do not
// belong in scheduler logs or the owner-facing API.
type ModelLifecyclePurgeResult struct {
	OfferingsDeleted int64
	AliasesDeleted   int64
	ModelsDeleted    int64
}

// ModelLifecycleRepo owns destructive catalog lifecycle cleanup. DiscoveryRepo
// remains the only writer that creates or updates discovered models; this repo
// only enforces the inverse invariant that an inoperable account cannot retain
// a supposedly live catalog.
type ModelLifecycleRepo struct {
	db *DB
}

func NewModelLifecycleRepo(db *DB) *ModelLifecycleRepo {
	return &ModelLifecycleRepo{db: db}
}

// PurgeInactive deletes every offering that is not currently usable, then
// removes aliases no remaining account exposes and canonical models with no
// remaining offering or alias. Offering operations, certifications and probe
// runs are removed by their ON DELETE CASCADE graph.
//
// The account predicate deliberately matches the live owner-console contract:
// connected + healthy + not in reauthentication. A future discovery after the
// account recovers recreates the catalog from the provider's current truth.
func (r *ModelLifecycleRepo) PurgeInactive(ctx context.Context) (ModelLifecyclePurgeResult, error) {
	var result ModelLifecyclePurgeResult
	tx, err := r.db.Conn().BeginTx(ctx, nil)
	if err != nil {
		return result, fmt.Errorf("storage: begin model lifecycle purge: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return result, fmt.Errorf("storage: model lifecycle purge: defer fks: %w", err)
	}

	offerings, err := tx.ExecContext(ctx, `
		DELETE FROM account_model_offerings
		WHERE availability <> 'available'
		   OR NOT EXISTS (
		       SELECT 1
		       FROM accounts a
		       WHERE a.id = account_model_offerings.account_id
		         AND a.connection_state = 'connected'
		         AND a.health_state = 'healthy'
		         AND a.reauth_in_progress = 0
		   )`)
	if err != nil {
		return result, fmt.Errorf("storage: model lifecycle purge: offerings: %w", err)
	}
	if result.OfferingsDeleted, err = offerings.RowsAffected(); err != nil {
		return result, fmt.Errorf("storage: model lifecycle purge: offering count: %w", err)
	}

	aliases, err := tx.ExecContext(ctx, `
		DELETE FROM provider_model_aliases
		WHERE NOT EXISTS (
		    SELECT 1
		    FROM account_model_offerings amo
		    WHERE amo.provider_id = provider_model_aliases.provider_id
		      AND amo.provider_model_id = provider_model_aliases.provider_model_id
		)`)
	if err != nil {
		return result, fmt.Errorf("storage: model lifecycle purge: aliases: %w", err)
	}
	if result.AliasesDeleted, err = aliases.RowsAffected(); err != nil {
		return result, fmt.Errorf("storage: model lifecycle purge: alias count: %w", err)
	}

	modelsResult, err := tx.ExecContext(ctx, `
		DELETE FROM models
		WHERE NOT EXISTS (
		          SELECT 1 FROM account_model_offerings amo
		          WHERE amo.model_id = models.id
		      )
		  AND NOT EXISTS (
		          SELECT 1 FROM provider_model_aliases pma
		          WHERE pma.model_id = models.id
		      )`)
	if err != nil {
		return result, fmt.Errorf("storage: model lifecycle purge: canonical models: %w", err)
	}
	if result.ModelsDeleted, err = modelsResult.RowsAffected(); err != nil {
		return result, fmt.Errorf("storage: model lifecycle purge: canonical model count: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return ModelLifecyclePurgeResult{}, fmt.Errorf("storage: model lifecycle purge: commit: %w", err)
	}
	return result, nil
}

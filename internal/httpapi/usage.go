package httpapi

import (
	"context"
	"errors"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/routing"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// usageRecorder is the usage_records write surface. *storage.UsageRecordRepo
// satisfies it; tests inject a fake (including a failing one to prove the write
// is SURFACED, never swallowed — the card's "never swallowed" requirement).
type usageRecorder interface {
	Insert(ctx context.Context, rec storage.UsageRecord) error
}

// usageStatus maps a terminal loop error (nil = success) to the closed usage
// status vocabulary — a typed code, never raw provider text.
func usageStatus(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, routing.ErrNoEligibleOffering):
		return "no_eligible_offering"
	case errors.Is(err, routing.ErrContextExceedsTier):
		return "context_exceeds_tier"
	case errors.Is(err, routing.ErrCapabilityUnsupported):
		return "capability_unsupported"
	default:
		return "failure"
	}
}

// buildUsageRecord assembles a secret-free usage_records row from the request's
// ids and the loop's terminal result. It carries provider/account/funding as
// correlation ids only — never any content — and attributes to the
// authenticated key.
func buildUsageRecord(id, requestID string, apiKeyID *string, tier string, res routing.FallbackResult, err error) storage.UsageRecord {
	rec := storage.UsageRecord{
		ID:        id,
		RequestID: requestID,
		APIKeyID:  apiKeyID,
		Tier:      tier,
		Status:    usageStatus(err),
	}
	if res.ProviderID != "" {
		p := res.ProviderID
		rec.ProviderID = &p
	}
	if res.AccountID != "" {
		a := res.AccountID
		rec.AccountID = &a
	}
	if res.Attempts > 0 {
		n := res.Attempts
		rec.FallbackAttempts = &n
	}
	return rec
}

package httpapi

import (
	"context"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// freeChatOfferingLister is the catalog read the usability run needs:
// *storage.CatalogRepo satisfies it via ListChatOfferingsToVerify.
type freeChatOfferingLister interface {
	ListChatOfferingsToVerify(ctx context.Context, accountID string) ([]storage.ChatOfferingToVerify, error)
}

// credentialLeaser is the decrypt-once lease the probe needs to obtain the
// account key: the real CredentialService.Use satisfies it. The plaintext lives
// only inside the callback (the lease zeroes it on return), so the probe runs
// entirely within that scope and the key never escapes.
type credentialLeaser interface {
	Use(ctx context.Context, credentialID string, fn func([]byte) error) error
}

// usabilityVerifier assembles the live dependencies of one per-account
// usability pass: list the observed chat offering-ops, lease the credential,
// and run verifyAccountChatUsability with the leased key.
type usabilityVerifier struct {
	offerings freeChatOfferingLister
	creds     credentialLeaser
	driver    certRecorder
	probe     usabilityProbeFn
	baseURL   string
}

// verifyAccount runs one usability pass for accountID using credentialID. The
// credential is leased ONLY when there is at least one offering to probe — an
// account with nothing observed never triggers a decrypt. A lister error is
// surfaced before any lease; a lease error is surfaced too. The probe itself
// runs inside the lease callback, so the plaintext key never outlives it.
func (v *usabilityVerifier) verifyAccount(ctx context.Context, accountID, credentialID string) (usabilityRunSummary, error) {
	rows, err := v.offerings.ListChatOfferingsToVerify(ctx, accountID)
	if err != nil {
		return usabilityRunSummary{}, err
	}
	if len(rows) == 0 {
		return usabilityRunSummary{}, nil
	}

	offerings := make([]chatOffering, len(rows))
	for i, r := range rows {
		offerings[i] = chatOffering{OfferingOperationID: r.OfferingOperationID, ProviderModelID: r.ProviderModelID}
	}

	var summary usabilityRunSummary
	if err := v.creds.Use(ctx, credentialID, func(key []byte) error {
		summary = verifyAccountChatUsability(ctx, v.driver, v.probe, v.baseURL, string(key), offerings)
		return nil
	}); err != nil {
		return usabilityRunSummary{}, err
	}
	return summary, nil
}

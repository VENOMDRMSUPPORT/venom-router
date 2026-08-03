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

// declaredCapabilityLister reads the account's declared NON-chat capabilities
// stranded in `probing` (tools, vision, …) so the run can certify them from
// their declaration: *storage.CatalogRepo satisfies it.
type declaredCapabilityLister interface {
	ListNonChatOperationsToCertify(ctx context.Context, accountID string) ([]storage.NonChatOperationToCertify, error)
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
	declared  declaredCapabilityLister
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
	var summary usabilityRunSummary

	// 1) Certify declared NON-chat capabilities (tools, vision, …) from their
	// declaration. These have no runtime prober, so no credential is leased —
	// the offering-operation's existence is the models.dev declaration, and that
	// is the evidence. Done first so an account with only declared capabilities
	// (no chat rows left to probe) is still certified rather than short-circuited.
	if v.declared != nil {
		declaredRows, err := v.declared.ListNonChatOperationsToCertify(ctx, accountID)
		if err != nil {
			return usabilityRunSummary{}, err
		}
		if len(declaredRows) > 0 {
			caps := make([]declaredCapability, len(declaredRows))
			for i, r := range declaredRows {
				caps[i] = declaredCapability{OfferingOperationID: r.OfferingOperationID, Operation: r.Operation}
			}
			summary.CertifiedDeclared = certifyDeclaredCapabilities(ctx, v.driver, caps)
		}
	}

	// 2) Verify chat with a LIVE runtime probe (04 §5): the credential is leased
	// only when there is at least one chat offering to probe — an account with
	// nothing observed never triggers a decrypt. The probe runs inside the lease
	// callback, so the plaintext key never outlives it.
	rows, err := v.offerings.ListChatOfferingsToVerify(ctx, accountID)
	if err != nil {
		return usabilityRunSummary{}, err
	}
	if len(rows) == 0 {
		return summary, nil
	}

	offerings := make([]chatOffering, len(rows))
	for i, r := range rows {
		offerings[i] = chatOffering{OfferingOperationID: r.OfferingOperationID, ProviderModelID: r.ProviderModelID}
	}

	if err := v.creds.Use(ctx, credentialID, func(key []byte) error {
		chat := verifyAccountChatUsability(ctx, v.driver, v.probe, v.baseURL, string(key), offerings)
		summary.Probed = chat.Probed
		summary.Usable = chat.Usable
		summary.StoppedOnAuth = chat.StoppedOnAuth
		return nil
	}); err != nil {
		return usabilityRunSummary{}, err
	}
	return summary, nil
}

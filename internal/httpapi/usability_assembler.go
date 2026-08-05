package httpapi

import (
	"context"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// chatOfferingLister is the catalog read the usability run needs — the
// account's chat offering-operations already in `probing`, which is the work
// EVERY pass executes. *storage.CatalogRepo satisfies it via
// ListChatOfferingsToVerify. (It was once free-specific to opencode-zen; the
// sweep now spans seven providers and the read was never free-filtered, hence
// the plain name.)
type chatOfferingLister interface {
	ListChatOfferingsToVerify(ctx context.Context, accountID string) ([]storage.ChatOfferingToVerify, error)
}

// observedChatOfferingLister reads the account's chat offering-operations still
// stranded at `observed` — the rows discovery just seeded. Only the FAST LANE
// consumes it: *storage.CatalogRepo satisfies it via ListObservedChatOfferings.
type observedChatOfferingLister interface {
	ListObservedChatOfferings(ctx context.Context, accountID string) ([]storage.ChatOfferingToVerify, error)
}

// probeStarter drives certification edge 2 (observed -> probing, 04 §5) — the
// SAME *intelligence.CertificationDriver.StartProbe edge
// intelligence.ReviewDrainer.Drain drives for the steady state (reached from
// this package via ProbeWorkers.DrainTick). The fast lane drives it for the
// account it was just handed; the scheduled sweep never does.
type probeStarter interface {
	StartProbe(ctx context.Context, offeringOperationID string) (models.Certification, error)
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
// usability pass: list the chat offering-ops awaiting a verdict, lease the
// credential, and run verifyAccountChatUsability with the leased key. The same
// instance serves BOTH lanes — verifyAccount (scheduled sweep) and
// verifyAccountFastLane (post-discovery) — so they can never drift into two
// different certification/probe wirings.
type usabilityVerifier struct {
	offerings chatOfferingLister
	// observed + starter are the FAST LANE's extra pair: the freshly seeded
	// `observed` chat rows and the edge that advances them. Both nil-safe — a
	// verifier without them simply never drives the edge, which is exactly the
	// scheduled sweep's behaviour anyway.
	observed observedChatOfferingLister
	starter  probeStarter
	declared declaredCapabilityLister
	creds    credentialLeaser
	driver   certRecorder
	probe    usabilityProbeFn
	baseURL  string
	// newPacer mints this account's pacer for THIS pass. It is a factory, not
	// a shared pacer, because pacer state (shrunken concurrency, an open
	// breaker) is per account per sweep: one throttled account must not narrow
	// another's window, and every sweep starts fresh — cross-sweep protection
	// is the provider's own Retry-After, not stale in-memory state. A nil
	// factory leaves the pacer nil, which verifyAccountChatUsability treats as
	// unpaced-at-full-width.
	newPacer func() *usabilityPacer
}

// verifyAccount runs one SCHEDULED-SWEEP usability pass for accountID using
// credentialID. The credential is leased ONLY when there is at least one
// offering to probe — an account with nothing in `probing` never triggers a
// decrypt. A lister error is surfaced before any lease; a lease error is
// surfaced too. The probe itself runs inside the lease callback, so the
// plaintext key never outlives it.
//
// It keeps the probing-only contract deliberately: in the steady state the
// probe_drain tick (ProbeWorkers.DrainTick -> intelligence.ReviewDrainer) owns
// the observed -> probing edge, and two components driving one edge is exactly
// the drift this split avoids.
func (v *usabilityVerifier) verifyAccount(ctx context.Context, accountID, credentialID string) (usabilityRunSummary, error) {
	return v.run(ctx, accountID, credentialID, false)
}

// verifyAccountFastLane runs the same pass, except it FIRST drives the
// observed -> probing edge for the account's own chat offering-operations.
//
// This is what makes the fast lane actually fast (spec §3.C.5). It fires within
// milliseconds of a successful discovery run, when every chat row that run just
// created is still `observed` — the drainer has not ticked yet. Without driving
// the edge here, ListChatOfferingsToVerify returns zero rows and the freshly
// connected account waits out a whole scheduler round for the fill it was
// promised immediately.
func (v *usabilityVerifier) verifyAccountFastLane(ctx context.Context, accountID, credentialID string) (usabilityRunSummary, error) {
	return v.run(ctx, accountID, credentialID, true)
}

// startObservedChatProbes drives edge 2 for every chat offering-operation of
// accountID still at `observed`, and returns how many advanced. It mirrors
// ReviewDrainer.Drain's semantics exactly: one row's failure (a concurrent CAS
// conflict, a row a competing drainer already advanced) is SKIPPED and the rest
// continue — never fatal. A LISTER failure is likewise not fatal: the pass then
// simply proceeds with whatever is already in `probing`, which is precisely
// what the scheduled sweep would have done.
func (v *usabilityVerifier) startObservedChatProbes(ctx context.Context, accountID string) int {
	if v.observed == nil || v.starter == nil {
		return 0
	}
	rows, err := v.observed.ListObservedChatOfferings(ctx, accountID)
	if err != nil {
		return 0
	}
	started := 0
	for _, r := range rows {
		if _, err := v.starter.StartProbe(ctx, r.OfferingOperationID); err != nil {
			continue
		}
		started++
	}
	return started
}

// run is verifyAccount / verifyAccountFastLane's shared body. driveObservedChat
// is the ONE difference between the two lanes, so neither can drift into a
// different certification/probe wiring than the other.
func (v *usabilityVerifier) run(ctx context.Context, accountID, credentialID string, driveObservedChat bool) (usabilityRunSummary, error) {
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

	// 2) FAST LANE ONLY: advance this account's freshly discovered chat rows
	// across observed -> probing so step 3 can see them at all. The scheduled
	// sweep skips this entirely — the drainer owns that edge in the steady state.
	if driveObservedChat {
		summary.StartedProbing = v.startObservedChatProbes(ctx, accountID)
	}

	// 3) Verify chat with a LIVE runtime probe (04 §5): the credential is leased
	// only when there is at least one chat offering to probe — an account with
	// nothing in `probing` never triggers a decrypt. The probe runs inside the
	// lease callback, so the plaintext key never outlives it.
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

	var pacer *usabilityPacer
	if v.newPacer != nil {
		pacer = v.newPacer()
	}

	if err := v.creds.Use(ctx, credentialID, func(key []byte) error {
		chat := verifyAccountChatUsability(ctx, v.driver, v.probe, pacer, v.baseURL, string(key), offerings)
		summary.Probed = chat.Probed
		summary.Usable = chat.Usable
		summary.StoppedOnAuth = chat.StoppedOnAuth
		return nil
	}); err != nil {
		return usabilityRunSummary{}, err
	}
	return summary, nil
}

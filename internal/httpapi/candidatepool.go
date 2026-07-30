package httpapi

import (
	"context"
	"sort"
	"sync"

	accountsdomain "github.com/VENOMDRMSUPPORT/venom-router/internal/accounts/domain"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/models"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/quota"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/routing"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/storage"
)

// DefaultMaxCandidates bounds the total number of offerings a single snapshot
// pass will assemble. When the fleet exceeds it the snapshot is CAPPED and the
// cap is reported (never silently truncated — a silent cap reads as "the whole
// fleet was considered" when it was not, 05 §2).
const DefaultMaxCandidates = 2000

// inflightCounter is the process-local per-account in-flight request counter the
// executor increments around a dispatch and the snapshot reads for the P2C
// live-signal (05 §2 Step 7 stage 3). It is shared across requests, so one
// instance lives at the composition root.
type inflightCounter struct {
	mu sync.Mutex
	n  map[string]int
}

func newInflightCounter() *inflightCounter { return &inflightCounter{n: map[string]int{}} }

func (c *inflightCounter) inc(accountID string) {
	c.mu.Lock()
	c.n[accountID]++
	c.mu.Unlock()
}

func (c *inflightCounter) dec(accountID string) {
	c.mu.Lock()
	if c.n[accountID] > 0 {
		c.n[accountID]--
	}
	c.mu.Unlock()
}

func (c *inflightCounter) count(accountID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n[accountID]
}

// SnapshotBuilder assembles routing's candidate snapshot from the REAL catalog,
// account, funding, credential, and quota-window facts (P5-WIRE-003 governor
// decision). It deliberately does NOT read httpapi's EffectiveOffering
// projection, whose NativeCapabilities/TransportOperations are hardcoded nil so
// Routable is permanently false — a pool derived from it would always be empty.
type SnapshotBuilder struct {
	catalog       *storage.CatalogRepo
	accounts      *storage.AccountRepo
	funding       *storage.FundingEvidenceRepo
	creds         *storage.AccountCredentialRepo
	windows       quotaWindowLister
	inflight      *inflightCounter
	maxCandidates int
}

// quotaWindowLister is the ONE batched quota-window read the snapshot performs
// (never per-candidate). It is an interface so a test can both provide windows
// without seeding and count that it is called exactly once.
// *storage.QuotaWindowRepo satisfies it.
type quotaWindowLister interface {
	ListByAccounts(ctx context.Context, accountIDs []string) (map[string][]quota.Window, error)
}

// NewSnapshotBuilder builds a snapshot builder. maxCandidates <= 0 uses
// DefaultMaxCandidates.
func NewSnapshotBuilder(catalog *storage.CatalogRepo, accounts *storage.AccountRepo, funding *storage.FundingEvidenceRepo, creds *storage.AccountCredentialRepo, windows quotaWindowLister, inflight *inflightCounter, maxCandidates int) *SnapshotBuilder {
	if maxCandidates <= 0 {
		maxCandidates = DefaultMaxCandidates
	}
	if inflight == nil {
		inflight = newInflightCounter()
	}
	return &SnapshotBuilder{catalog: catalog, accounts: accounts, funding: funding, creds: creds, windows: windows, inflight: inflight, maxCandidates: maxCandidates}
}

// SnapshotResult is one assembly pass's output: the ranked pool plus the
// secret-free decision facts (summary + exclusion counts), whether the fleet
// was capped, and the per-account active-credential id the executor resolves.
type SnapshotResult struct {
	Pool                  routing.RoutePool
	Summary               observability.CandidateSummary
	ExclusionReasons      map[string]int
	Capped                bool
	CredentialIDByAccount map[string]string
}

// Build runs the whole Step 2→7 assembly for one request: it reads the real
// facts, constructs []routing.CandidateOffering, and ranks them into a
// routing.RoutePool for the given tier and requirements.
func (b *SnapshotBuilder) Build(ctx context.Context, tier routing.Tier, reqs routing.Requirements) (SnapshotResult, error) {
	policies, err := routing.Policies()
	if err != nil {
		return SnapshotResult{}, err
	}
	policy := policies[tier]

	accountByID, fundingByID, credValidByID, credIDByID, err := b.loadAccounts(ctx)
	if err != nil {
		return SnapshotResult{}, err
	}

	ids := make([]string, 0, len(accountByID))
	for id := range accountByID {
		ids = append(ids, id)
	}
	sort.Strings(ids) // deterministic order; never a map-range dependence
	windowsByID, err := b.windows.ListByAccounts(ctx, ids)
	if err != nil {
		return SnapshotResult{}, err
	}

	candidates, capped, err := b.assembleCandidates(ctx, accountByID, fundingByID, credValidByID, windowsByID)
	if err != nil {
		return SnapshotResult{}, err
	}

	// Steps 2→6: the real routing pipeline (never re-implemented here).
	pool := routing.BuildCandidatePool(models.OperationChat, candidates)
	eligible, excluded, gateErr := routing.ApplyHardGates(pool, reqs, policy)
	if gateErr != nil {
		// A request-level ceiling rejection is a real routing outcome, not a
		// storage error — surface it to the handler to map to the public 400.
		return SnapshotResult{ExclusionReasons: map[string]int{}, CredentialIDByAccount: credIDByID}, gateErr
	}
	groups := routing.BuildRouteGroups(eligible)
	scored := routing.ScoreGroups(groups, policy)
	inBand := routing.ApplyCompetitiveBand(scored, policy)

	return SnapshotResult{
		Pool:                  rankPool(inBand),
		Summary:               summarize(candidates, groups),
		ExclusionReasons:      countExclusions(excluded),
		Capped:                capped,
		CredentialIDByAccount: credIDByID,
	}, nil
}

// loadAccounts pages every account once and, per account, resolves its funding
// classification and active-credential id (bounded by account count, not
// candidate count — the batched query mandate is on quota windows only).
func (b *SnapshotBuilder) loadAccounts(ctx context.Context) (map[string]accountsdomain.Account, map[string]accountsdomain.Funding, map[string]bool, map[string]string, error) {
	accountByID := map[string]accountsdomain.Account{}
	fundingByID := map[string]accountsdomain.Funding{}
	credValidByID := map[string]bool{}
	credIDByID := map[string]string{}

	cursor := ""
	for {
		page, next, err := b.accounts.List(ctx, cursor, 0, "")
		if err != nil {
			return nil, nil, nil, nil, err
		}
		for _, acc := range page {
			accountByID[acc.ID] = acc
			fundingByID[acc.ID] = accountsdomain.FundingUnknown
			if ev, ok, ferr := b.funding.CurrentForAccount(ctx, acc.ID); ferr == nil && ok {
				fundingByID[acc.ID] = ev.Funding
			}
			creds, cerr := b.creds.ListForAccount(ctx, acc.ID)
			if cerr == nil {
				for _, cred := range creds {
					if cred.State == accountsdomain.CredentialActive {
						credValidByID[acc.ID] = true
						credIDByID[acc.ID] = cred.ID
						break
					}
				}
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return accountByID, fundingByID, credValidByID, credIDByID, nil
}

// assembleCandidates pages the catalog and joins each offering with the account
// facts + batched quota windows into a routing.CandidateOffering. It caps the
// total at maxCandidates and reports the cap.
func (b *SnapshotBuilder) assembleCandidates(ctx context.Context, accountByID map[string]accountsdomain.Account, fundingByID map[string]accountsdomain.Funding, credValidByID map[string]bool, windowsByID map[string][]quota.Window) ([]routing.CandidateOffering, bool, error) {
	var candidates []routing.CandidateOffering
	cursor := ""
	for {
		rows, next, err := b.catalog.ListOfferings(ctx, storage.CatalogListParams{Cursor: cursor})
		if err != nil {
			return nil, false, err
		}
		for _, row := range rows {
			acc, ok := accountByID[row.AccountID]
			if !ok {
				continue // an offering with no live account cannot be routed
			}
			if len(candidates) >= b.maxCandidates {
				return candidates, true, nil // capped — reported, never silent
			}
			candidates = append(candidates, routing.CandidateOffering{
				ProviderID:            row.ProviderID,
				AccountID:             row.AccountID,
				ProviderModelID:       row.ProviderModelID,
				Funding:               fundingByID[row.AccountID],
				AccountHealth:         acc.HealthState,
				CredentialValid:       credValidByID[row.AccountID],
				Cooling:               false, // cooldown state is a later unit's concern (NON-GOAL here)
				VerifiedContextTokens: effectiveContextCeiling(row.NativeContextTokens, row.ContextLength),
				Certifications:        certificationsFor(row.Operations),
				QualityRating:         row.QualityRating,
				QuotaWindows:          windowsByID[row.AccountID],
				InFlightCount:         b.inflight.count(row.AccountID),
			})
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return candidates, false, nil
}

// effectiveContextCeiling is min(native, provider cap) when BOTH are known,
// else the one that is known, else nil — UNKNOWN, never 0 (ApplyHardGates fails
// an unknown ceiling closed).
func effectiveContextCeiling(native, providerCap *int) *int64 {
	switch {
	case native != nil && providerCap != nil:
		m := *native
		if *providerCap < m {
			m = *providerCap
		}
		v := int64(m)
		return &v
	case native != nil:
		v := int64(*native)
		return &v
	case providerCap != nil:
		v := int64(*providerCap)
		return &v
	default:
		return nil
	}
}

// certificationsFor maps the catalog's per-operation rows to routing's
// certification map — the REAL per-operation state + capability truth.
func certificationsFor(ops []storage.CatalogOperationRow) map[models.Operation]models.Certification {
	if len(ops) == 0 {
		return nil
	}
	out := make(map[models.Operation]models.Certification, len(ops))
	for _, op := range ops {
		out[models.Operation(op.Operation)] = models.Certification{
			State: models.CertificationState(op.CertificationStatus),
			Truth: models.CapabilityTruth(op.CapabilityTruth),
		}
	}
	return out
}

// rankPool orders the in-band group scores quality-first (Composite descending,
// stable by input order) into a RoutePool the fallback loop iterates. Account-
// level distribution (Max DRR+P2C, Pro deficit) happens inside the loop's
// SelectAccount; funding-mix deficit ordering that needs persisted state is a
// later unit (NON-GOAL here).
func rankPool(inBand []routing.GroupScore) routing.RoutePool {
	ranked := make([]routing.GroupScore, len(inBand))
	copy(ranked, inBand)
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Composite > ranked[j].Composite })
	groups := make([]routing.RouteGroup, 0, len(ranked))
	for _, gs := range ranked {
		groups = append(groups, gs.Group)
	}
	return routing.RoutePool{Groups: groups}
}

// summarize builds the secret-free candidate summary (counts + group keys).
func summarize(candidates []routing.CandidateOffering, groups []routing.RouteGroup) observability.CandidateSummary {
	keys := make([]string, 0, len(groups))
	for _, g := range groups {
		keys = append(keys, g.ProviderID+"/"+g.ProviderModelID)
	}
	return observability.CandidateSummary{TotalCandidates: len(candidates), EligibleGroups: len(groups), GroupKeys: keys}
}

// countExclusions collapses ApplyHardGates' per-candidate reason lists into a
// reason-code → count map for the route decision record.
func countExclusions(excluded map[int][]string) map[string]int {
	out := map[string]int{}
	for _, reasons := range excluded {
		for _, r := range reasons {
			out[r]++
		}
	}
	return out
}

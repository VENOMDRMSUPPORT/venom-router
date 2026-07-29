package routing

// RoutePool is the ranked set of in-band route groups Step 7 hands to the
// Step-8 fallback loop (P4-WIRE-001). Carrying the whole ranked pool — not one
// group — is what makes ROUTE-014's cross-offering fallback ("another offering
// on the same account") and skip-provider ("skip all routes for this provider")
// expressible: both require more than one group to be in view at once.
type RoutePool struct {
	Groups []RouteGroup
}

// SingleGroupPool wraps one group as a pool — the trivial Step-7 output when
// only one offering survived the band.
func SingleGroupPool(g RouteGroup) RoutePool {
	return RoutePool{Groups: []RouteGroup{g}}
}

// candKey is a candidate's stable per-request identity (provider, offering,
// account) used to bound transient retries and to exclude a spent candidate.
func candKey(c CandidateOffering) string {
	return c.ProviderID + "\x00" + c.ProviderModelID + "\x00" + c.AccountID
}

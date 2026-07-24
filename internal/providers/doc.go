// Package providers defines the extension seam every inference provider
// plugs into: a typed adapter contract (03 §1) and a Registry that
// dispatches to adapters by typed capability, never by a switch on a
// provider's slug string (01 §4.5 / 08 §8). This contract freezes at the
// end of P2b for the P7 provider fan-out.
//
// This package is pure: it imports no infrastructure (database/sql,
// net/http, internal/storage) and no internal/accounts or internal/models
// (01 §3), so it stays a stable, dependency-free seam that those layers
// build on rather than a package that depends on them. It does not import
// internal/execution either, even though execution.ProviderID is
// documented as a placeholder for this package's canonical ProviderID —
// that avoids risking a future import cycle; unifying the two is a later
// unit's concern.
//
// Concrete provider adapters (OpenAI, Anthropic, etc.) are later units
// (PROV-002/005/007, P7); this package only freezes the contract and
// dispatch shape.
package providers

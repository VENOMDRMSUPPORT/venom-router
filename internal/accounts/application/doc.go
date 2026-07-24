// Package application is the accounts application layer: services that
// orchestrate the pure accounts/domain rules against persistence,
// expressed through repository PORT interfaces (ports.go) rather than
// any concrete storage — this package imports only accounts/domain,
// internal/secrets, and stdlib. internal/storage implements these
// ports structurally (duck-typed: matching method signatures), and the
// composition root wires storage's concrete repos into these services.
// This package must NEVER import internal/storage, database/sql, or
// net/http (internal/staticgate enforces this) — that is the whole
// point of the ports & adapters split (P2b-PROV-003).
package application

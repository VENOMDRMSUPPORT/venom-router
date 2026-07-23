// Package sanitize is the full-redaction boundary that guarantees no
// secret value crosses a log, error, trace, or audit sink.
//
// Redaction here is always FULL: a value classified as secret is
// replaced in its entirety by the exact placeholder [REDACTED]. This
// package never emits a partial or masked secret (no leading/trailing
// characters, no length hints) — a masked secret is still a leak.
//
// The package is standalone. This unit deliberately does not wire itself
// into internal/observability; the logging, error, and audit boundaries
// call this API in later units. It exposes three entry points:
//
//   - IsSecretKey reports whether a structured field key names a secret.
//   - Value redacts a value when its key is secret, and otherwise
//     returns the value unchanged.
//   - Text redacts known secret token shapes embedded in free text:
//     Authorization/Bearer and Basic credentials, and OAuth/PKCE and
//     credential query parameters such as code=, state=, and
//     code_verifier=.
//
// Classification fails closed: within the secret categories below an
// uncertain key is treated as secret. To keep operational logs useful it
// does not over-redact — a small allowlist of known-non-secret
// identifiers (for example key_id, the public keyring key identifier) is
// checked first and is never redacted.
package sanitize

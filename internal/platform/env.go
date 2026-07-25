package platform

import "os"

// envKeyEncryptionKey is the environment variable that, when set to a
// non-empty value, overrides the on-disk keyring as the sole source of
// the master encryption key for a run (internal/secrets P1-SEC-001).
// os.LookupEnv is called only within this package (and internal/config);
// forbidigo enforces that no other package reads environment variables
// directly — internal/secrets receives the resolved (value, present)
// pair as parameters instead of reading the environment itself.
const envKeyEncryptionKey = "VENOM_ENCRYPTION_KEY"

// EncryptionKeyOverride returns the raw value of VENOM_ENCRYPTION_KEY and
// whether it was set at all (present is true even for an empty value —
// callers decide how to treat "set but empty").
func EncryptionKeyOverride() (value string, present bool) {
	return os.LookupEnv(envKeyEncryptionKey)
}

// EnvPresent reports only whether the environment variable name is
// set — never its value. This is deliberately presence-only (not a
// general LookupEnv passthrough): callers that need to report a
// confidential-client OAuth provider's missing configuration (e.g.
// P2b-PROV-002's GET /providers) must never be able to accidentally
// surface a secret's value, so the value itself is not retrievable
// through this function at all.
func EnvPresent(name string) bool {
	_, ok := os.LookupEnv(name)
	return ok
}

// envAntigravityClientID/envAntigravityClientSecret are the two
// confidential-client OAuth environment variables antigravity's catalog
// entry declares as RequiredEnv (P2b-PROV-007). os.LookupEnv is called
// only within this package (and internal/config); forbidigo enforces
// that no other package reads environment variables directly —
// internal/providers/internal/httpapi receive the resolved values as
// constructor parameters instead of reading the environment themselves.
const (
	envAntigravityClientID     = "VENOM_ANTIGRAVITY_CLIENT_ID"
	envAntigravityClientSecret = "VENOM_ANTIGRAVITY_CLIENT_SECRET"
)

// AntigravityOAuthClientCredentials returns antigravity's confidential
// OAuth client id/secret pair and whether BOTH are present with a
// non-empty value. Composition (internal/httpapi's ControlMux) uses this
// to decide whether to register a live antigravity OAuth adapter; when
// ok is false, the caller registers nothing and GET /providers/
// antigravity continues to report configured=false + missing_env (via
// EnvPresent, above) — the two are deliberately independent checks so a
// var set to an empty string is treated as "missing" here without
// changing EnvPresent's own presence-only semantics used elsewhere.
func AntigravityOAuthClientCredentials() (clientID, clientSecret string, ok bool) {
	id, idOK := os.LookupEnv(envAntigravityClientID)
	secret, secretOK := os.LookupEnv(envAntigravityClientSecret)
	if !idOK || !secretOK || id == "" || secret == "" {
		return "", "", false
	}
	return id, secret, true
}

// envE2EOpenCodeZenKey is the environment variable an opt-in, non-CI-
// blocking real-account acceptance test (P2b-TEST-003 C.2) reads to
// exercise the opencode-zen connect flow against the real, public
// endpoint with a real free-tier API key. Absent (the default for every
// normal CI run, which sets no such variable), that test skips itself
// rather than failing. os.LookupEnv is called only within this package
// (and internal/config), per forbidigo's rule; this narrow, single-
// purpose accessor is the test-support seam that lets the test itself
// stay outside the packages forbidigo permits to call os.Getenv/
// os.LookupEnv directly.
const envE2EOpenCodeZenKey = "VENOM_E2E_OPENCODE_ZEN_KEY"

// OpenCodeZenE2ECredential returns the real opencode-zen API key an
// owner has opted to run the real-account E2E harness with, and whether
// it was set at all (present is true even for an empty value — the
// caller decides how to treat "set but empty", exactly like
// EncryptionKeyOverride above).
func OpenCodeZenE2ECredential() (value string, present bool) {
	return os.LookupEnv(envE2EOpenCodeZenKey)
}

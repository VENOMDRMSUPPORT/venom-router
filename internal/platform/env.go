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

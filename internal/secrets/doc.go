// Package secrets holds the master encryption keyring in memory outside
// the application database (01 §8). It resolves the master key material
// for a run — from an injected environment override, or from
// <dataDir>/secrets/keyring.json, creating that file on first run — and
// never itself reads the environment (forbidigo enforces that
// os.Getenv/os.LookupEnv are confined to internal/config and
// internal/platform); callers pass the resolved (value, present) pair
// from platform.EncryptionKeyOverride in as parameters.
//
// This package is scoped to P1-SEC-001 only: it stores and loads key
// material and its key_id. It does not encrypt or decrypt anything
// (SEC-002), rotate keys (SEC-003), reconcile the keyring against
// ciphertext already in the database (SEC-004), or redact secrets from
// logs (SEC-005).
package secrets

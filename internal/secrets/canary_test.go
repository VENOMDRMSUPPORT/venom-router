// Package secrets_test is the P1-SEC-006 secret canary: a mechanical
// proof (08 §5/§7) that a known, distinctive secret never survives the
// leak paths that exist at P1 — encryption (envelope + errors),
// structured logging (through the sanitize boundary), and error
// messages. It is an external test package (not `package secrets`) so
// it can import secrets, sanitize, and observability together without
// affecting the internal/staticgate import-layering test, which only
// inspects production import graphs.
//
// Two leak paths the card names do not exist yet and are deliberately
// NOT faked here:
//   - trace: distributed tracing does not exist at P1.
//   - audit: the audit log / evidence rows land with M2 in P2b.
//
// When either lands, add a TestSecretCanary_<Name> function alongside
// these, exercising it the same way: push canarySecret through it,
// capture the real output, and call assertNoSecretFragment.
package secrets_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/observability"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/sanitize"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/secrets"
)

// canarySecret is the known, distinctive secret injected through every
// leak path below. It is fixed (not random) so the detector's fragment
// search is exact and every test run is deterministic.
const canarySecret = "CANARY-SECRET-9f3Kx2Qw8pLm0Zt7Vb4Nr1Hy6Dc5Ea"

// canaryFragmentSize is the minimum substring length the detector
// searches for — the same "no partial leak" rigor internal/sanitize's
// own tests use (assertFullyRedacted there), reimplemented here as a
// pure, reusable function rather than imported, since it is a test-only
// helper private to each package's test suite.
const canaryFragmentSize = 4

// findSecretFragment reports the first window of secret (length >=
// canaryFragmentSize) that appears anywhere in output. It is pure
// (no *testing.T) so the meta-test below can assert its return value
// directly, proving the detector actually detects a leak rather than
// merely never firing.
func findSecretFragment(output, secret string) (fragment string, found bool) {
	for start := 0; start+canaryFragmentSize <= len(secret); start++ {
		for end := start + canaryFragmentSize; end <= len(secret); end++ {
			frag := secret[start:end]
			if strings.Contains(output, frag) {
				return frag, true
			}
		}
	}
	return "", false
}

// assertNoSecretFragment fails the test if any window (>= 4 chars) of
// secret appears anywhere in output.
func assertNoSecretFragment(t *testing.T, output, secret string) {
	t.Helper()
	if frag, found := findSecretFragment(output, secret); found {
		t.Fatalf("output leaked secret fragment %q:\n%s", frag, output)
	}
}

// TestCanaryDetector_BitesOnDeliberateLeak is the required meta-test:
// it proves findSecretFragment (and therefore assertNoSecretFragment)
// actually detects a leak, by feeding it output that deliberately
// contains the raw secret — so the canary tests below are not
// vacuously green. This tests the detector's own logic; it does not,
// and must not, make `task gate` red.
func TestCanaryDetector_BitesOnDeliberateLeak(t *testing.T) {
	leaked := "debug: raw value observed was " + canarySecret + " during processing"

	frag, found := findSecretFragment(leaked, canarySecret)
	if !found {
		t.Fatalf("findSecretFragment did not detect a deliberately embedded leak — the canary would be vacuously green")
	}
	if len(frag) < canaryFragmentSize {
		t.Fatalf("detected fragment %q is shorter than the minimum window %d", frag, canaryFragmentSize)
	}

	// Sanity: leak-free output must NOT be flagged.
	if _, found := findSecretFragment("nothing secret here", canarySecret); found {
		t.Fatalf("findSecretFragment reported a leak in output containing no fragment of the secret")
	}
}

// TestSecretCanary_Encryption pushes canarySecret through Encrypt as
// the plaintext, then asserts it appears in neither the JSON-serialized
// Envelope nor any Decrypt error string produced around that envelope
// (wrong identity, tampered ciphertext, unknown key_id).
func TestSecretCanary_Encryption(t *testing.T) {
	kr, err := secrets.Load(t.TempDir(), "", false)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	id := secrets.RecordIdentity{Purpose: "canary", Provider: "canary", Account: "canary", Record: "canary", Kind: "canary"}

	env, err := secrets.Encrypt(kr, id, []byte(canarySecret))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("json.Marshal(env) error = %v", err)
	}
	assertNoSecretFragment(t, string(data), canarySecret)

	otherID := secrets.RecordIdentity{Purpose: "other", Provider: "other", Account: "other", Record: "other", Kind: "other"}
	if _, err := secrets.Decrypt(kr, otherID, env); err == nil {
		t.Fatalf("Decrypt() with mismatched identity succeeded, want an error")
	} else {
		assertNoSecretFragment(t, err.Error(), canarySecret)
	}

	tampered := env
	tampered.Ciphertext = append([]byte(nil), env.Ciphertext...)
	tampered.Ciphertext[0] ^= 0x01
	if _, err := secrets.Decrypt(kr, id, tampered); err == nil {
		t.Fatalf("Decrypt() of tampered ciphertext succeeded, want an error")
	} else {
		assertNoSecretFragment(t, err.Error(), canarySecret)
	}

	unknownKey := env
	unknownKey.KeyID = "k_does_not_exist"
	if _, err := secrets.Decrypt(kr, id, unknownKey); err == nil {
		t.Fatalf("Decrypt() with unknown key_id succeeded, want an error")
	} else {
		assertNoSecretFragment(t, err.Error(), canarySecret)
	}
}

// TestSecretCanary_Logging pushes canarySecret through the sanitize
// boundary (sanitize.Value with a secret-named key, sanitize.Text over
// free text embedding the secret) and into a real structured logger
// whose JSON output is captured in a buffer, then asserts no fragment
// of the secret survived.
func TestSecretCanary_Logging(t *testing.T) {
	var buf bytes.Buffer
	logger := observability.New(slog.NewJSONHandler(&buf, nil))

	logger.Info("canary log line",
		observability.String("api_key", sanitize.Value("api_key", canarySecret)),
		observability.String("message", sanitize.Text("Authorization: Bearer "+canarySecret)),
		observability.String("query", sanitize.Text("callback?code="+canarySecret+"&foo=bar")),
	)

	got := buf.String()
	assertNoSecretFragment(t, got, canarySecret)
	if !strings.Contains(got, sanitize.Placeholder) {
		t.Fatalf("expected the sanitize placeholder to appear in the captured log output: %s", got)
	}
	// Sanity: the log line itself, and non-secret surrounding text, must
	// still be genuinely present — proving this is a real capture, not
	// an empty buffer trivially free of the secret.
	if !strings.Contains(got, "canary log line") || !strings.Contains(got, "foo=bar") {
		t.Fatalf("captured log output missing expected non-secret content: %s", got)
	}
}

// TestSecretCanary_Errors builds error values that secrets produces
// around canarySecret through two distinct paths — Load rejecting it as
// a malformed VENOM_ENCRYPTION_KEY override, and Decrypt failing on an
// envelope sealed over it — and asserts no fragment leaks into either.
func TestSecretCanary_Errors(t *testing.T) {
	// canarySecret is not a valid base64 encoding of 32 bytes, so Load
	// must reject it — proving the rejection error never echoes the
	// invalid value back.
	_, err := secrets.Load(t.TempDir(), canarySecret, true)
	if err == nil {
		t.Fatalf("Load() with canarySecret as VENOM_ENCRYPTION_KEY succeeded, want ErrInvalidEnvKey")
	}
	assertNoSecretFragment(t, err.Error(), canarySecret)

	kr, err := secrets.Load(t.TempDir(), "", false)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	id := secrets.RecordIdentity{Purpose: "canary", Provider: "canary", Account: "canary", Record: "canary", Kind: "canary"}
	env, err := secrets.Encrypt(kr, id, []byte(canarySecret))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	badID := secrets.RecordIdentity{Purpose: "wrong", Provider: "wrong", Account: "wrong", Record: "wrong", Kind: "wrong"}
	_, decErr := secrets.Decrypt(kr, badID, env)
	if decErr == nil {
		t.Fatalf("Decrypt() with mismatched identity succeeded, want an error")
	}
	assertNoSecretFragment(t, decErr.Error(), canarySecret)
}

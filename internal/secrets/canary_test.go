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
//
// ITS SHAPE IS LOAD-BEARING: a '-' appears at least every 3 characters, so
// EVERY 4-char window contains a '-'. Std-alphabet base64 (A–Z a–z 0–9 + / =)
// can never contain '-', so no window of this secret can occur by chance inside
// the base64 nonce/ciphertext of a marshalled Envelope.
//
// The previous value ("CANARY-SECRET-9f3Kx2Qw8pLm0Zt7Vb4Nr1Hy6Dc5Ea") had a
// 30-character pure-alphanumeric tail, and that made this canary FLAKY: its
// 4-char windows collide with random base64 at a measured rate of ~1 in 6,100
// marshalled envelopes (2,000,000-trial probe), and each run searches several
// outputs on both OSes on every push plus nightly. The nightly race run
// 30506005200 failed exactly this way, reporting the fragment "c5Ea" — a chance
// collision inside random ciphertext, NOT a leak. A false alarm in a security
// canary is worse than a merely annoying flake: it teaches everyone to dismiss
// the one test whose whole job is to be believed.
//
// The fix removes the collision surface instead of widening
// canaryFragmentSize, so the "no partial leak" rigor stays at 4 characters.
// TestCanarySecret_ShapeCannotCollideWithBase64 pins the property.
const canarySecret = "CAN-ARY-SEC-RET-9f3-Kx2-Qw8-pLm-0Zt-7Vb-4Nr-1Hy-6Dc-5Ea"

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

// TestCanarySecret_ShapeCannotCollideWithBase64 pins the property that makes
// this canary sound rather than flaky: every window the detector searches for
// (canaryFragmentSize chars and longer) must contain at least one character
// that std-alphabet base64 cannot produce, so a fragment can never appear by
// chance inside a random nonce or ciphertext.
//
// Without this, the canary reports leaks that did not happen — measured at ~1
// in 6,100 marshalled envelopes with the old pure-alphanumeric secret, which is
// what failed nightly race run 30506005200 on the fragment "c5Ea".
func TestCanarySecret_ShapeCannotCollideWithBase64(t *testing.T) {
	const base64Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/="

	isBase64Char := func(r rune) bool { return strings.ContainsRune(base64Alphabet, r) }

	// It is enough to check the SHORTEST windows: every longer window contains a
	// shortest one, so if no 4-char window is pure base64, no longer window is.
	for start := 0; start+canaryFragmentSize <= len(canarySecret); start++ {
		window := canarySecret[start : start+canaryFragmentSize]
		pure := true
		for _, r := range window {
			if !isBase64Char(r) {
				pure = false
				break
			}
		}
		if pure {
			t.Fatalf("canarySecret window %q (offset %d) contains only base64 characters — it can collide with random ciphertext and make this canary report a leak that never happened; keep a '-' at least every %d characters",
				window, start, canaryFragmentSize-1)
		}
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

// TestSecretCanary_OwnerPasswordHash pushes canarySecret through
// DeriveOwnerPasswordHash (P2b-SEC-001) as the owner password, then
// asserts the raw secret appears in none of: the JSON-serialized
// OwnerPasswordHash (only Hash/Salt/params should be present), the
// too-short rejection error (a distinct, shorter canary-derived value),
// or a wrong-password VerifyOwnerPassword call's inputs echoed anywhere.
// The password itself is never returned by any of these functions, so a
// leak here would mean a fragment of the raw secret bytes coincidentally
// surviving into the derived hash/salt's byte representation — which
// this proves does not happen.
func TestSecretCanary_OwnerPasswordHash(t *testing.T) {
	stored, err := secrets.DeriveOwnerPasswordHash(canarySecret)
	if err != nil {
		t.Fatalf("DeriveOwnerPasswordHash(canarySecret) error = %v", err)
	}

	data, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("json.Marshal(stored) error = %v", err)
	}
	assertNoSecretFragment(t, string(data), canarySecret)

	// canarySecret is well above MinPasswordLength, so also prove the
	// too-short rejection path (a different, shorter secret) never echoes
	// what was rejected.
	shortSecret := canarySecret[:4]
	_, shortErr := secrets.DeriveOwnerPasswordHash(shortSecret)
	if shortErr == nil {
		t.Fatalf("DeriveOwnerPasswordHash(shortSecret) succeeded, want ErrPasswordTooShort")
	}
	assertNoSecretFragment(t, shortErr.Error(), shortSecret)

	if !secrets.VerifyOwnerPassword(canarySecret, stored) {
		t.Fatalf("VerifyOwnerPassword(canarySecret, its own stored hash) = false, want true")
	}
	if secrets.VerifyOwnerPassword("a-completely-different-password", stored) {
		t.Fatalf("VerifyOwnerPassword(wrong password, canarySecret's stored hash) = true, want false")
	}
}

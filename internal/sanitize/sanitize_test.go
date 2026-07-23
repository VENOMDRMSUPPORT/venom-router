package sanitize

import (
	"strings"
	"testing"
)

// assertFullyRedacted proves no partial secret survived: it asserts that
// no window of secret of length >= minWindow appears anywhere in got. A
// masked secret (leading/trailing characters, length hints) would leave
// such a window and fail this check.
func assertFullyRedacted(t *testing.T, got, secret string) {
	t.Helper()
	const minWindow = 4
	if len(secret) < minWindow {
		t.Fatalf("test secret %q is too short to prove non-partial redaction", secret)
	}
	for start := 0; start+minWindow <= len(secret); start++ {
		for end := start + minWindow; end <= len(secret); end++ {
			frag := secret[start:end]
			if strings.Contains(got, frag) {
				t.Fatalf("output %q leaked secret fragment %q", got, frag)
			}
		}
	}
}

func TestValue_KnownSecretKeys_FullyRedacted(t *testing.T) {
	// Each key names one of the core secret categories in the card. The
	// value is distinctive so we can assert none of it survives.
	keys := []string{
		"credential", "credentials",
		"password", "passwd", "passphrase",
		"secret", "client_secret",
		"token", "access_token", "refresh_token", "id_token",
		"api_key", "apikey", "apiKey",
		"access_key", "private_key",
		"authorization", "Authorization",
		"cookie", "session_token", "session_id",
		"bearer",
		"code", "state",
		"code_verifier", "code_challenge",
		"pkce_verifier",
	}
	for _, key := range keys {
		secret := "Zt7-Qk92_Lm4xVp0-" + key
		got := Value(key, secret)
		if got != Placeholder {
			t.Fatalf("Value(%q, secret) = %q, want exactly %q", key, got, Placeholder)
		}
		assertFullyRedacted(t, got, secret)
	}
}

func TestValue_KnownNonSecretKeys_NotRedacted(t *testing.T) {
	// Guards against over-redaction: real, useful operational fields must
	// pass through untouched even though the classifier fails closed
	// within the secret categories.
	nonSecret := []string{
		"key_id", "account_id", "window_key", "cache_key",
		"request_id", "job_id", "provider", "model", "status", "stage",
	}
	for _, key := range nonSecret {
		value := "operational-value-for-" + key
		got := Value(key, value)
		if got != value {
			t.Fatalf("Value(%q, %q) = %q, want the value unchanged (non-secret must not be redacted)", key, value, got)
		}
	}
}

func TestValue_KeyID_IsNotASecret(t *testing.T) {
	// Explicit: key_id is the PUBLIC keyring key identifier, never a
	// secret. Redacting it would blind operators to which key was used.
	const value = "k_0123456789abcdef"
	if got := Value("key_id", value); got != value {
		t.Fatalf("Value(\"key_id\", %q) = %q, want %q — key_id must never be redacted", value, got, value)
	}
	if IsSecretKey("key_id") {
		t.Fatalf("IsSecretKey(\"key_id\") = true, want false")
	}
}

func TestIsSecretKey_CaseInsensitive(t *testing.T) {
	for _, key := range []string{"AUTHORIZATION", "Api_Key", "Password", "TOKEN"} {
		if !IsSecretKey(key) {
			t.Fatalf("IsSecretKey(%q) = false, want true (matching must be case-insensitive)", key)
		}
	}
}

func TestValue_NeverPartial_ForRepresentativeSecret(t *testing.T) {
	// A representative secret: the output must be EXACTLY the placeholder
	// and contain no substring of the original.
	const secret = "sk-live-9f3Kx2Qw8pLm0Zt7Vb4Nr1Hy6Dc5Ea"
	got := Value("api_key", secret)
	if got != Placeholder {
		t.Fatalf("Value(\"api_key\", secret) = %q, want exactly %q", got, Placeholder)
	}
	assertFullyRedacted(t, got, secret)
}

func TestText_RedactsBearerAuthorizationHeader(t *testing.T) {
	const token = "Zt7Qk92Lm4xVp0Rb8Nc3Hy6Dc5Ea1Fg2"
	in := "GET /v1/chat Authorization: Bearer " + token + " requestcompleted"
	got := Text(in)

	if strings.Contains(got, token) {
		t.Fatalf("Text kept the bearer token: %q", got)
	}
	assertFullyRedacted(t, got, token)
	if !strings.Contains(got, Placeholder) {
		t.Fatalf("Text did not insert the placeholder: %q", got)
	}
	// Surrounding non-secret text and the scheme name stay intact.
	if !strings.Contains(got, "GET /v1/chat") || !strings.Contains(got, "requestcompleted") {
		t.Fatalf("Text damaged surrounding non-secret text: %q", got)
	}
	if !strings.Contains(got, "Bearer") {
		t.Fatalf("Text should keep the visible scheme, got: %q", got)
	}
}

func TestText_RedactsOAuthCodeAndState(t *testing.T) {
	const code = "Ac1Bd2Ce3Df4Eg5Fh6"
	const state = "Xy9Wz8Vu7Ts6Rq5Po4"
	in := "callback?code=" + code + "&state=" + state + "&foo=bar"
	got := Text(in)

	if strings.Contains(got, code) {
		t.Fatalf("Text kept the OAuth code value: %q", got)
	}
	if strings.Contains(got, state) {
		t.Fatalf("Text kept the OAuth state value: %q", got)
	}
	assertFullyRedacted(t, got, code)
	assertFullyRedacted(t, got, state)
	// Non-secret query parameter and the leading path stay intact.
	if !strings.Contains(got, "foo=bar") {
		t.Fatalf("Text redacted a non-secret query parameter: %q", got)
	}
	if !strings.Contains(got, "callback?") {
		t.Fatalf("Text damaged surrounding text: %q", got)
	}
	// The keys stay visible so the log remains readable.
	if !strings.Contains(got, "code="+Placeholder) || !strings.Contains(got, "state="+Placeholder) {
		t.Fatalf("Text should keep keys visible with redacted values: %q", got)
	}
}

func TestText_RedactsPKCECodeVerifier(t *testing.T) {
	const verifier = "Nv4Mw3Lx2Ky1Jz0Iu9Ht8Gs7Fr6Eq5"
	in := "token exchange code_verifier=" + verifier + " done"
	got := Text(in)

	if strings.Contains(got, verifier) {
		t.Fatalf("Text kept the PKCE code_verifier value: %q", got)
	}
	assertFullyRedacted(t, got, verifier)
	if !strings.Contains(got, "code_verifier="+Placeholder) {
		t.Fatalf("Text should redact only the verifier value: %q", got)
	}
	if !strings.Contains(got, "done") {
		t.Fatalf("Text damaged surrounding text: %q", got)
	}
}

func TestText_LeavesBenignTextUntouched(t *testing.T) {
	in := "provider=openai model=gpt-4 status=ok stage=dispatch request_id=req_123"
	if got := Text(in); got != in {
		t.Fatalf("Text over-redacted benign operational text:\n got: %q\nwant: %q", got, in)
	}
}

package providers

import (
	"encoding/json"
	"testing"
	"time"
)

// TestOAuthTokenExpiry_ReadsEveryOAuthAdaptersStoredShape proves the helper
// reads the REAL stored-token envelopes the three OAuth adapters marshal —
// not a parallel invented shape — by round-tripping through each adapter's
// own marshal path where one is exported, and the raw JSON convention
// otherwise.
func TestOAuthTokenExpiry_ReadsEveryOAuthAdaptersStoredShape(t *testing.T) {
	wantExpiry := time.Now().Add(time.Hour).Unix()

	// clinepass: marshalClinePassToken is the adapter's own writer.
	clineCreds, err := marshalClinePassToken("at", "rt", wantExpiry, nil)
	if err != nil {
		t.Fatalf("marshalClinePassToken: %v", err)
	}

	// claude-code / antigravity both store the same three snake_case fields.
	claudeValue, _ := json.Marshal(claudeCodeStoredToken{AccessToken: "at", RefreshToken: "rt", ExpiresAt: wantExpiry})
	antigravityValue, _ := json.Marshal(antigravityStoredToken{AccessToken: "at", RefreshToken: "rt", ExpiresAt: wantExpiry})

	for name, creds := range map[string]StoredCredentials{
		"clinepass":   clineCreds,
		"claude-code": {Value: string(claudeValue)},
		"antigravity": {Value: string(antigravityValue)},
	} {
		got, ok := OAuthTokenExpiry(creds)
		if !ok {
			t.Fatalf("%s: OAuthTokenExpiry ok = false, want true", name)
		}
		if got != wantExpiry {
			t.Fatalf("%s: OAuthTokenExpiry = %d, want %d", name, got, wantExpiry)
		}
	}
}

func TestOAuthTokenExpiry_UnknownForNonJSONOrMissing(t *testing.T) {
	cases := map[string]string{
		"api key plaintext":  "sk-not-json",
		"empty":              "",
		"json without field": `{"access_token":"at"}`,
		"zero expiry":        `{"access_token":"at","expires_at":0}`,
		"negative expiry":    `{"access_token":"at","expires_at":-5}`,
	}
	for name, value := range cases {
		if _, ok := OAuthTokenExpiry(StoredCredentials{Value: value}); ok {
			t.Fatalf("%s: OAuthTokenExpiry ok = true, want false", name)
		}
	}
}

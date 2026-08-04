package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestParseClinePassEmbeddedCode(t *testing.T) {
	payload := map[string]any{
		"accessToken":  "at-embedded",
		"refreshToken": "rt-embedded",
		"email":        "user@example.com",
		"name":         "",
		"expiresAt":    "2026-08-03T19:17:18.72883055Z",
	}
	raw, _ := json.Marshal(payload)
	code := base64.StdEncoding.EncodeToString(raw) + "0Gv4_fakeSignatureSuffix"

	tok, ok := parseClinePassEmbeddedCode(code)
	if !ok {
		t.Fatal("expected embedded code to parse")
	}
	if tok.AccessToken != "at-embedded" || tok.RefreshToken != "rt-embedded" {
		t.Fatalf("tokens = %+v", tok)
	}
	if tok.UserInfo == nil || tok.UserInfo.Email != "user@example.com" {
		t.Fatalf("userInfo = %+v, want email from payload", tok.UserInfo)
	}
}

func TestClinePass_CompleteOAuth_EmbeddedCodeSkipsExchange(t *testing.T) {
	payload := map[string]any{
		"accessToken":  "at-embedded",
		"refreshToken": "rt-embedded",
		"email":        "user@example.com",
		"expiresAt":    "2026-08-03T19:17:18Z",
	}
	raw, _ := json.Marshal(payload)
	code := base64.StdEncoding.EncodeToString(raw) + "sig"

	post := &fakeClinePost{status: 500, body: `should not be called`}
	get := &fakeClineGet{byPath: map[string]struct {
		status int
		body   string
	}{
		"/users/me": {200, `{"success":true,"data":{"id":"uid-9","email":"user@example.com"}}`},
	}}
	a := NewClinePassAdapter(post.probe, get.probe)
	id, creds, err := a.CompleteOAuth(context.Background(), code, "verifier-unused", "http://127.0.0.1:8081/callback")
	if err != nil {
		t.Fatalf("CompleteOAuth: %v", err)
	}
	if post.lastBody != nil {
		t.Fatalf("token exchange was called unexpectedly: %s", post.lastBody)
	}
	if id.ExternalID == "" || id.Email != "user@example.com" {
		t.Fatalf("identity = %+v", id)
	}
	var stored clinePassStoredToken
	if err := json.Unmarshal([]byte(creds.Value), &stored); err != nil || stored.AccessToken != "at-embedded" {
		t.Fatalf("stored = %+v err=%v", stored, err)
	}
}

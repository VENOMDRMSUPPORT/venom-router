package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
)

// fakeClinePost records the last JSON body and returns a fixed status/body.
type fakeClinePost struct {
	status   int
	body     string
	err      error
	lastBody []byte
}

func (f *fakeClinePost) probe(_ context.Context, _ string, body []byte) (int, []byte, error) {
	f.lastBody = body
	if f.err != nil {
		return 0, nil, f.err
	}
	return f.status, []byte(f.body), nil
}

func clineGet(entries map[string]struct {
	status int
	body   string
}) *fakeClaudeGet {
	return &fakeClaudeGet{byPath: entries}
}

func clineIdentityGet(status int, body string) *fakeClaudeGet {
	return clineGet(map[string]struct {
		status int
		body   string
	}{"/api/v1/users/me": {status, body}})
}

const clineTokenOK = `{"accessToken":"at-1","refreshToken":"rt-1","expiresIn":3600}`
const clineIdentityOK = `{"clineUserId":"cline-777","email":"a@b.com"}`

func TestClinePass_BaseURLMatchesCatalog(t *testing.T) {
	var entry CatalogEntry
	for _, e := range BuiltinCatalog() {
		if e.ID == ClinePassID {
			entry = e
		}
	}
	if entry.BaseURL != ClinePassBaseURL {
		t.Fatalf("ClinePassBaseURL = %q, catalog = %q", ClinePassBaseURL, entry.BaseURL)
	}
}

func TestClinePass_RegistersNativeOAuthOpenAIChat(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterClinePass(reg, (&fakeClinePost{}).probe, clineIdentityGet(200, clineIdentityOK).probe); err != nil {
		t.Fatalf("RegisterClinePass: %v", err)
	}
	def, _ := reg.Definition(ClinePassID)
	if def.Transport != TransportKindNativeOAuth || def.WireSchema != WireSchemaOpenAIChat {
		t.Fatalf("def = {%q,%q}, want native_oauth/openai_chat", def.Transport, def.WireSchema)
	}
}

// TestClinePass_AuthorizeURL is mutations 1 (no code_challenge on the wire) and
// 2 (client_type=extension present).
func TestClinePass_AuthorizeURL(t *testing.T) {
	a := NewClinePassAdapter((&fakeClinePost{}).probe, clineIdentityGet(200, clineIdentityOK).probe)
	raw, err := a.BeginOAuth(context.Background(), "http://localhost/cb", "st", "challenge-should-be-ignored")
	if err != nil {
		t.Fatalf("BeginOAuth: %v", err)
	}
	u, _ := url.Parse(raw)
	q := u.Query()
	if q.Has("code_challenge") {
		t.Fatal("authorize URL carries code_challenge — clinepass's PKCE verifier must NOT be sent on the wire")
	}
	if q.Get("client_type") != "extension" {
		t.Fatalf("client_type = %q, want extension", q.Get("client_type"))
	}
	if q.Get("provider") != "clinepass" {
		t.Fatalf("provider = %q, want clinepass", q.Get("provider"))
	}
}

func TestClinePass_CompleteIdentityAndFunding(t *testing.T) {
	post := &fakeClinePost{status: 200, body: clineTokenOK}
	a := NewClinePassAdapter(post.probe, clineIdentityGet(200, clineIdentityOK).probe)
	id, creds, err := a.CompleteOAuth(context.Background(), "the-code", "verifier", "http://localhost/cb")
	if err != nil {
		t.Fatalf("CompleteOAuth: %v", err)
	}
	// The verifier must NOT appear in the token body (not sent on the wire).
	if strings.Contains(string(post.lastBody), "verifier") || strings.Contains(string(post.lastBody), "code_verifier") {
		t.Fatalf("token body carries the PKCE verifier: %s", post.lastBody)
	}
	if id.ExternalID != "cline-777" {
		t.Fatalf("ExternalID = %q, want cline-777", id.ExternalID)
	}
	var stored clinePassStoredToken
	if err := json.Unmarshal([]byte(creds.Value), &stored); err != nil || stored.AccessToken != "at-1" {
		t.Fatalf("stored = %+v (err %v), want at-1", stored, err)
	}
}

// TestClinePass_FundingPaidAndLocked is mutation row 4: funding is paid; and it
// asserts the catalog entry is the Locked+Paid fixed policy that makes an owner
// override fail funding_locked (the enforcement lives in the frozen funding
// domain + this catalog flag).
func TestClinePass_FundingPaidAndLocked(t *testing.T) {
	post := &fakeClinePost{status: 200, body: clineTokenOK}
	a := NewClinePassAdapter(post.probe, clineIdentityGet(200, clineIdentityOK).probe)
	id, _, err := a.CompleteOAuth(context.Background(), "c", "v", "cb")
	if err != nil {
		t.Fatalf("CompleteOAuth: %v", err)
	}
	if id.Funding != string(FundingPaid) {
		t.Fatalf("Funding = %q, want paid", id.Funding)
	}

	var entry CatalogEntry
	for _, e := range BuiltinCatalog() {
		if e.ID == ClinePassID {
			entry = e
		}
	}
	if entry.Funding.Mode != FundingModeFixed || entry.Funding.Fixed != FundingPaid || !entry.Funding.Locked {
		t.Fatalf("catalog funding = %+v, want fixed/paid/locked (this is what makes an owner override fail funding_locked)", entry.Funding)
	}
}

// TestClinePass_MissingUserIDIsTypedError is mutation row 3.
func TestClinePass_MissingUserIDIsTypedError(t *testing.T) {
	post := &fakeClinePost{status: 200, body: clineTokenOK}
	a := NewClinePassAdapter(post.probe, clineIdentityGet(200, `{"email":"a@b.com"}`).probe)
	id, _, err := a.CompleteOAuth(context.Background(), "c", "v", "cb")
	if !errors.Is(err, ErrMissingStableIdentity) {
		t.Fatalf("error = %v, want ErrMissingStableIdentity", err)
	}
	if id.ExternalID != "" {
		t.Fatalf("ExternalID = %q, want empty", id.ExternalID)
	}
}

func TestClinePass_RefreshRetention(t *testing.T) {
	old, _ := json.Marshal(clinePassStoredToken{AccessToken: "old-at", RefreshToken: "old-rt"})
	t.Run("none keeps old", func(t *testing.T) {
		post := &fakeClinePost{status: 200, body: `{"accessToken":"new-at","expiresIn":10}`}
		a := NewClinePassAdapter(post.probe, clineIdentityGet(200, clineIdentityOK).probe)
		out, err := a.RefreshCredentials(context.Background(), StoredCredentials{Value: string(old)})
		if err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		var s clinePassStoredToken
		_ = json.Unmarshal([]byte(out.Value), &s)
		if s.RefreshToken != "old-rt" {
			t.Fatalf("refresh = %q, want kept old-rt", s.RefreshToken)
		}
	})
	t.Run("failed refresh returns error", func(t *testing.T) {
		post := &fakeClinePost{err: errors.New("down")}
		a := NewClinePassAdapter(post.probe, clineIdentityGet(200, clineIdentityOK).probe)
		if _, err := a.RefreshCredentials(context.Background(), StoredCredentials{Value: string(old)}); err == nil {
			t.Fatal("Refresh error = nil, want a typed failure")
		}
	})
}

func TestClinePass_Discovery(t *testing.T) {
	get := clineGet(map[string]struct {
		status int
		body   string
	}{"/recommended-models": {200, `{"models":[{"id":"cline/x","name":"X"},{"modelId":"cline/y"}]}`}})
	a := NewClinePassAdapter((&fakeClinePost{}).probe, get.probe)
	models, err := a.DiscoverModels(context.Background(), storedCline("at"))
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %d, want 2", len(models))
	}
	if models[0].ProviderModelID != "cline/x" || models[0].DisplayName != "X" || len(models[0].Capabilities) != 1 {
		t.Fatalf("model0 = %+v, want cline/x chat-only", models[0])
	}
}

func TestClinePass_Quota(t *testing.T) {
	t.Run("credits map to a balance window", func(t *testing.T) {
		get := clineGet(map[string]struct {
			status int
			body   string
		}{"/balance": {200, `{"credits":12.5}`}})
		a := NewClinePassAdapter((&fakeClinePost{}).probe, get.probe)
		res, err := a.FetchQuota(context.Background(), storedCline("at"))
		if err != nil {
			t.Fatalf("FetchQuota: %v", err)
		}
		if len(res.Windows) != 1 || res.Windows[0].Unit != "credits" || res.Windows[0].Remaining == nil || *res.Windows[0].Remaining != 12.5 {
			t.Fatalf("windows = %+v, want one credits/12.5 balance window", res.Windows)
		}
	})
	t.Run("no balance yields no window", func(t *testing.T) {
		get := clineGet(map[string]struct {
			status int
			body   string
		}{"/balance": {200, `{"unrelated":1}`}})
		a := NewClinePassAdapter((&fakeClinePost{}).probe, get.probe)
		res, err := a.FetchQuota(context.Background(), storedCline("at"))
		if err != nil {
			t.Fatalf("FetchQuota: %v", err)
		}
		if len(res.Windows) != 0 {
			t.Fatalf("windows = %+v, want empty (no grounded balance)", res.Windows)
		}
	})
}

func TestClinePass_TokenNeverInError(t *testing.T) {
	const marker = "PLAINMARKER-cline-token"
	post := &fakeClinePost{status: 401}
	a := NewClinePassAdapter(post.probe, clineIdentityGet(200, clineIdentityOK).probe)
	_, _, err := a.CompleteOAuth(context.Background(), marker, "v", "cb")
	if err != nil && strings.Contains(err.Error(), marker) {
		t.Fatalf("error leaks the code: %v", err)
	}
}

func storedCline(access string) StoredCredentials {
	v, _ := json.Marshal(clinePassStoredToken{AccessToken: access, RefreshToken: "r"})
	return StoredCredentials{Value: string(v)}
}

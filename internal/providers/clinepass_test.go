package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
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

// fakeClineGet is the clinepass GET seam shape: (ctx, url, accessToken) -> raw
// status/body, keyed by URL fragment. It records the last accessToken so tests
// can assert the public models call received none.
type fakeClineGet struct {
	byPath map[string]struct {
		status int
		body   string
	}
	err       error
	lastToken string
	callCount int
}

func (f *fakeClineGet) probe(_ context.Context, reqURL, accessToken string) (int, []byte, error) {
	f.callCount++
	f.lastToken = accessToken
	if f.err != nil {
		return 0, nil, f.err
	}
	// Longest matching fragment wins, so "/api/v1/users/me" never shadows the
	// more specific "/api/v1/users/me/plan/usage-limits" regardless of map
	// iteration order.
	best := ""
	bestLen := -1
	for frag := range f.byPath {
		if strings.Contains(reqURL, frag) && len(frag) > bestLen {
			best = frag
			bestLen = len(frag)
		}
	}
	if best != "" {
		resp := f.byPath[best]
		return resp.status, []byte(resp.body), nil
	}
	return 404, []byte(`{}`), nil
}

func clineGet(entries map[string]struct {
	status int
	body   string
}) *fakeClineGet {
	return &fakeClineGet{byPath: entries}
}

func clineIdentityGet(status int, body string) *fakeClineGet {
	return clineGet(map[string]struct {
		status int
		body   string
	}{"/api/v1/users/me": {status, body}})
}

// clineTokenOK is the REAL token response shape: {success, data:{accessToken,
// refreshToken, expiresAt, userInfo}} — the earlier top-level camelCase shape
// was drift (legacy 2026-08-03).
const clineTokenOK = `{"success":true,"data":{"accessToken":"at-1","refreshToken":"rt-1","expiresAt":"2026-08-03T16:00:00Z","userInfo":{"subject":"sub-1","email":"a@b.com","name":"A","clineUserId":"cline-777"}}}`

// clineIdentityOK is the REAL /users/me shape: {success, data:{id, ...}}.
const clineIdentityOK = `{"success":true,"data":{"id":"u-42","email":"a@b.com"}}`

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
// 2 (client_type=extension present): the reference flow sends exactly
// client_type/callback_url/redirect_uri/state (legacy 2026-08-03).
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
	if q.Get("callback_url") != "http://localhost/cb" || q.Get("redirect_uri") != "http://localhost/cb" {
		t.Fatalf("callback/redirect = %q/%q, want both the redirect uri", q.Get("callback_url"), q.Get("redirect_uri"))
	}
	if q.Get("state") != "st" {
		t.Fatalf("state = %q, want st", q.Get("state"))
	}
}

// TestClinePass_CompleteParsesEnvelopeAndUserInfo proves the token response is
// the {success, data} envelope, the stable id comes from userInfo, and the
// verifier is NOT in the body.
func TestClinePass_CompleteParsesEnvelopeAndUserInfo(t *testing.T) {
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
	// The code IS sent.
	if !strings.Contains(string(post.lastBody), `"code":"the-code"`) {
		t.Fatalf("token body = %q, want the code", post.lastBody)
	}
	if id.ExternalID != "cline-777" || id.Email != "a@b.com" {
		t.Fatalf("identity = %+v, want cline-777/a@b.com from userInfo", id)
	}
	var stored clinePassStoredToken
	if err := json.Unmarshal([]byte(creds.Value), &stored); err != nil || stored.AccessToken != "at-1" || stored.RefreshToken != "rt-1" {
		t.Fatalf("stored = %+v (err %v), want at-1/rt-1", stored, err)
	}
	if stored.UserInfo == nil || stored.UserInfo.ClineUserID != "cline-777" {
		t.Fatalf("stored userInfo = %+v, want cline-777 retained for later identity", stored.UserInfo)
	}
}

// TestClinePass_IdentityFallsBackToUsersMe proves a token response WITHOUT
// userInfo still resolves the stable id from GET /users/me.
func TestClinePass_IdentityFallsBackToUsersMe(t *testing.T) {
	tokenNoUser := `{"success":true,"data":{"accessToken":"at-1","refreshToken":"rt-1"}}`
	me := `{"success":true,"data":{"id":"u-9","clineUserId":"cline-fallback","email":"f@b.com"}}`
	post := &fakeClinePost{status: 200, body: tokenNoUser}
	a := NewClinePassAdapter(post.probe, clineIdentityGet(200, me).probe)
	id, _, err := a.CompleteOAuth(context.Background(), "c", "v", "cb")
	if err != nil {
		t.Fatalf("CompleteOAuth: %v", err)
	}
	if id.ExternalID != "cline-fallback" || id.Email != "f@b.com" {
		t.Fatalf("identity = %+v, want the /users/me fallback", id)
	}
}

// TestClinePass_CodeFragmentIsStripped proves the `#`-suffix quirk: a code
// like "abc#fragment" exchanges as "abc" (legacy 2026-08-03).
func TestClinePass_CodeFragmentIsStripped(t *testing.T) {
	post := &fakeClinePost{status: 200, body: clineTokenOK}
	a := NewClinePassAdapter(post.probe, clineIdentityGet(200, clineIdentityOK).probe)
	if _, _, err := a.CompleteOAuth(context.Background(), "abc#frag", "v", "cb"); err != nil {
		t.Fatalf("CompleteOAuth: %v", err)
	}
	if !strings.Contains(string(post.lastBody), `"code":"abc"`) {
		t.Fatalf("token body = %q, want the fragment-stripped code", post.lastBody)
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
	post := &fakeClinePost{status: 200, body: `{"success":true,"data":{"accessToken":"at-1"}}`}
	a := NewClinePassAdapter(post.probe, clineIdentityGet(200, `{"success":true,"data":{"email":"a@b.com"}}`).probe)
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
		post := &fakeClinePost{status: 200, body: `{"success":true,"data":{"accessToken":"new-at","expiresAt":"2026-08-03T16:00:00Z"}}`}
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
	t.Run("new refresh replaces and userInfo retained", func(t *testing.T) {
		withInfo, _ := json.Marshal(clinePassStoredToken{AccessToken: "old-at", RefreshToken: "old-rt", UserInfo: &clinePassUserInfo{ClineUserID: "cline-777", Email: "a@b.com"}})
		post := &fakeClinePost{status: 200, body: `{"success":true,"data":{"accessToken":"new-at","refreshToken":"new-rt","expiresAt":"2026-08-03T16:00:00Z"}}`}
		a := NewClinePassAdapter(post.probe, clineIdentityGet(200, clineIdentityOK).probe)
		out, err := a.RefreshCredentials(context.Background(), StoredCredentials{Value: string(withInfo)})
		if err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		var s clinePassStoredToken
		_ = json.Unmarshal([]byte(out.Value), &s)
		if s.RefreshToken != "new-rt" || s.UserInfo == nil || s.UserInfo.ClineUserID != "cline-777" {
			t.Fatalf("stored = %+v, want new-rt + retained userInfo", s)
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

// TestClinePass_RefreshBodyCamelCase pins the refresh REQUEST body shape:
// {refreshToken, grantType} camelCase (03 §3 / legacy).
func TestClinePass_RefreshBodyCamelCase(t *testing.T) {
	old, _ := json.Marshal(clinePassStoredToken{AccessToken: "old-at", RefreshToken: "old-rt"})
	post := &fakeClinePost{status: 200, body: `{"success":true,"data":{"accessToken":"new-at"}}`}
	a := NewClinePassAdapter(post.probe, clineIdentityGet(200, clineIdentityOK).probe)
	if _, err := a.RefreshCredentials(context.Background(), StoredCredentials{Value: string(old)}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !strings.Contains(string(post.lastBody), `"refreshToken":"old-rt"`) || !strings.Contains(string(post.lastBody), `"grantType":"refresh_token"`) {
		t.Fatalf("refresh body = %q, want camelCase refreshToken/grantType", post.lastBody)
	}
}

// TestClinePass_DiscoveryGroupMerge proves /recommended-models is the three
// GROUP shape (not a `models` array) and merges with clinePass > recommended >
// free priority (legacy 2026-08-03). The models endpoint is PUBLIC: the seam
// receives a blank token, so no Authorization header is sent.
func TestClinePass_DiscoveryGroupMerge(t *testing.T) {
	get := clineGet(map[string]struct {
		status int
		body   string
	}{"/recommended-models": {200, `{
		"clinePass":[{"id":"cline/opus","name":"Opus"}],
		"recommended":[{"id":"cline/opus","name":"Opus (dup)"},{"id":"cline/sonnet","name":"Sonnet"}],
		"free":[{"id":"cline/free","name":"Free"}]}`}})
	a := NewClinePassAdapter((&fakeClinePost{}).probe, get.probe)
	models, err := a.DiscoverModels(context.Background(), storedCline("at"))
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if len(models) != 3 {
		t.Fatalf("models = %d, want 3 unique", len(models))
	}
	byID := map[string]DiscoveredModel{}
	for _, m := range models {
		byID[m.ProviderModelID] = m
	}
	// The duplicate id must keep the higher-priority clinePass name.
	if byID["cline/opus"].DisplayName != "Opus" {
		t.Fatalf("cline/opus display = %q, want the clinePass-group name", byID["cline/opus"].DisplayName)
	}
	if byID["cline/sonnet"].ProviderModelID == "" || byID["cline/free"].ProviderModelID == "" {
		t.Fatalf("missing merged models: %+v", models)
	}
}

// TestClinePass_Quota proves the REAL quota path: /users/me -> the numeric user
// id -> /users/{id}/balance (micro-USD) + /users/me/plan/usage-limits
// (five_hour/weekly/monthly percent windows).
func TestClinePass_Quota(t *testing.T) {
	t.Run("balance micro-usd and usage limits map to windows", func(t *testing.T) {
		get := clineGet(map[string]struct {
			status int
			body   string
		}{
			"/api/v1/users/me":           {200, `{"success":true,"data":{"id":"u-42"}}`},
			"/api/v1/users/u-42/balance": {200, `{"success":true,"data":{"balance":12500000}}`},
			"/api/v1/users/me/plan/usage-limits": {200, `{"success":true,"data":{"limits":[
				{"type":"five_hour","percentUsed":42.5,"resetsAt":"2026-08-03T15:00:00Z"},
				{"type":"weekly","percentUsed":10,"resetsAt":"2026-08-03T15:00:00Z"}]}}`},
		})
		a := NewClinePassAdapter((&fakeClinePost{}).probe, get.probe)
		res, err := a.FetchQuota(context.Background(), storedCline("at"))
		if err != nil {
			t.Fatalf("FetchQuota: %v", err)
		}
		if len(res.Windows) != 3 {
			t.Fatalf("windows = %+v, want 3 (balance + 5h + 7d)", res.Windows)
		}
		var balance *QuotaWindow
		var fh *QuotaWindow
		for i := range res.Windows {
			if res.Windows[i].WindowType == "balance" {
				balance = &res.Windows[i]
			}
			if res.Windows[i].WindowType == "rolling_5h" {
				fh = &res.Windows[i]
			}
		}
		if balance == nil || balance.Remaining == nil || *balance.Remaining != 12.5 {
			t.Fatalf("balance = %+v, want USD 12.5 (micro-usd / 1e6)", balance)
		}
		if fh == nil || fh.Used == nil || *fh.Used != 42.5 || fh.DurationSeconds == nil || *fh.DurationSeconds != 18000 {
			t.Fatalf("rolling_5h = %+v, want percent/42.5/18000", fh)
		}
		if fh.ResetAt == nil || *fh.ResetAt != time.Date(2026, 8, 3, 15, 0, 0, 0, time.UTC).Unix() {
			t.Fatalf("rolling_5h reset = %v, want the declared RFC3339", fh.ResetAt)
		}
	})

	t.Run("no user id yields no window", func(t *testing.T) {
		get := clineGet(map[string]struct {
			status int
			body   string
		}{"/api/v1/users/me": {200, `{"success":true,"data":{}}`}})
		a := NewClinePassAdapter((&fakeClinePost{}).probe, get.probe)
		res, err := a.FetchQuota(context.Background(), storedCline("at"))
		if err != nil {
			t.Fatalf("FetchQuota: %v", err)
		}
		if len(res.Windows) != 0 {
			t.Fatalf("windows = %+v, want empty (no grounded user id)", res.Windows)
		}
	})

	t.Run("balance absent but limits present still yields the limits", func(t *testing.T) {
		get := clineGet(map[string]struct {
			status int
			body   string
		}{
			"/api/v1/users/me":                   {200, `{"success":true,"data":{"id":"u-42"}}`},
			"/api/v1/users/u-42/balance":         {200, `{"success":true,"data":{"balance":0}}`},
			"/api/v1/users/me/plan/usage-limits": {200, `{"success":true,"data":{"limits":[{"type":"monthly","percentUsed":5}]}}`},
		})
		a := NewClinePassAdapter((&fakeClinePost{}).probe, get.probe)
		res, err := a.FetchQuota(context.Background(), storedCline("at"))
		if err != nil {
			t.Fatalf("FetchQuota: %v", err)
		}
		if len(res.Windows) != 2 {
			t.Fatalf("windows = %+v, want balance(0) + monthly", res.Windows)
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

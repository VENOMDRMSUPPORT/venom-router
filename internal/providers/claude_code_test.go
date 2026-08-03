package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
)

// fakeClaudeToken records the last form and returns a fixed body/err.
type fakeClaudeToken struct {
	body     string
	err      error
	lastForm url.Values
}

func (f *fakeClaudeToken) probe(_ context.Context, _ string, form url.Values) ([]byte, error) {
	f.lastForm = form
	if f.err != nil {
		return nil, f.err
	}
	return []byte(f.body), nil
}

// fakeClaudeGet returns a status/body keyed by a substring of the URL path.
type fakeClaudeGet struct {
	byPath map[string]struct {
		status int
		body   string
	}
	err error
}

func (f *fakeClaudeGet) probe(_ context.Context, reqURL, _ string) (int, []byte, error) {
	if f.err != nil {
		return 0, nil, f.err
	}
	for frag, resp := range f.byPath {
		if strings.Contains(reqURL, frag) {
			return resp.status, []byte(resp.body), nil
		}
	}
	return 404, []byte(`{}`), nil
}

func claudeGet(profileStatus int, profileBody string) *fakeClaudeGet {
	return &fakeClaudeGet{byPath: map[string]struct {
		status int
		body   string
	}{"/api/oauth/profile": {profileStatus, profileBody}}}
}

const claudeProfileOK = `{"account":{"uuid":"acc-123","email":"a@b.com","plan":"Pro"}}`
const claudeTokenOK = `{"access_token":"at-1","refresh_token":"rt-1","expires_in":3600}`

func TestClaudeCode_BaseURLMatchesCatalog(t *testing.T) {
	var entry CatalogEntry
	for _, e := range BuiltinCatalog() {
		if e.ID == ClaudeCodeID {
			entry = e
		}
	}
	if entry.BaseURL != ClaudeCodeAPIBase {
		t.Fatalf("ClaudeCodeAPIBase = %q, catalog = %q", ClaudeCodeAPIBase, entry.BaseURL)
	}
}

// TestClaudeCode_RegistersNativeOAuthAnthropic is mutation row 8.
func TestClaudeCode_RegistersNativeOAuthAnthropic(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterClaudeCode(reg, (&fakeClaudeToken{}).probe, claudeGet(200, claudeProfileOK).probe); err != nil {
		t.Fatalf("RegisterClaudeCode: %v", err)
	}
	def, _ := reg.Definition(ClaudeCodeID)
	if def.Transport != TransportKindNativeOAuth {
		t.Fatalf("Transport = %q, want native_oauth", def.Transport)
	}
	if def.WireSchema != WireSchemaAnthropicMessages {
		t.Fatalf("WireSchema = %q, want anthropic_messages", def.WireSchema)
	}
}

// TestClaudeCode_AuthorizeURL is mutation row 1: the authorize URL carries every
// required parameter including code_challenge_method=S256.
func TestClaudeCode_AuthorizeURL(t *testing.T) {
	a := NewClaudeCodeAdapter((&fakeClaudeToken{}).probe, claudeGet(200, claudeProfileOK).probe)
	raw, err := a.BeginOAuth(context.Background(), "http://localhost/cb", "state-xyz", "challenge-abc")
	if err != nil {
		t.Fatalf("BeginOAuth: %v", err)
	}
	u, _ := url.Parse(raw)
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" {
		t.Fatalf("code_challenge_method = %q, want S256", q.Get("code_challenge_method"))
	}
	for k, want := range map[string]string{
		"response_type": "code", "client_id": claudeCodeClientID, "redirect_uri": "http://localhost/cb",
		"state": "state-xyz", "code_challenge": "challenge-abc", "scope": claudeCodeScopes,
	} {
		if q.Get(k) != want {
			t.Fatalf("authorize %s = %q, want %q", k, q.Get(k), want)
		}
	}
}

func TestClaudeCode_CompleteExchangesAndIdentity(t *testing.T) {
	tok := &fakeClaudeToken{body: claudeTokenOK}
	a := NewClaudeCodeAdapter(tok.probe, claudeGet(200, claudeProfileOK).probe)
	id, creds, err := a.CompleteOAuth(context.Background(), "the-code", "the-verifier", "http://localhost/cb")
	if err != nil {
		t.Fatalf("CompleteOAuth: %v", err)
	}
	if tok.lastForm.Get("grant_type") != "authorization_code" || tok.lastForm.Get("code_verifier") != "the-verifier" {
		t.Fatalf("token form = %v, want authorization_code grant + verifier", tok.lastForm)
	}
	if id.ExternalID != "acc-123" || id.Funding != string(FundingPaid) {
		t.Fatalf("identity = %+v, want uuid acc-123 + paid", id)
	}
	var stored claudeCodeStoredToken
	if err := json.Unmarshal([]byte(creds.Value), &stored); err != nil || stored.AccessToken != "at-1" || stored.RefreshToken != "rt-1" {
		t.Fatalf("stored = %+v (err %v), want at-1/rt-1", stored, err)
	}
}

// TestClaudeCode_MissingUUIDIsTypedError is mutation row 2.
func TestClaudeCode_MissingUUIDIsTypedError(t *testing.T) {
	tok := &fakeClaudeToken{body: claudeTokenOK}
	a := NewClaudeCodeAdapter(tok.probe, claudeGet(200, `{"account":{"email":"a@b.com"}}`).probe)
	id, _, err := a.CompleteOAuth(context.Background(), "c", "v", "cb")
	if !errors.Is(err, ErrMissingStableIdentity) {
		t.Fatalf("error = %v, want ErrMissingStableIdentity", err)
	}
	if id.ExternalID != "" {
		t.Fatalf("ExternalID = %q, want empty (no fabrication)", id.ExternalID)
	}
}

// TestClaudeCode_RefreshRetention is mutation row 3: keep the old refresh token
// when the provider returns none; replace it when it returns one.
func TestClaudeCode_RefreshRetention(t *testing.T) {
	old, _ := json.Marshal(claudeCodeStoredToken{AccessToken: "old-at", RefreshToken: "old-rt", ExpiresAt: 1})

	t.Run("none returned keeps old", func(t *testing.T) {
		tok := &fakeClaudeToken{body: `{"access_token":"new-at","expires_in":3600}`}
		a := NewClaudeCodeAdapter(tok.probe, claudeGet(200, claudeProfileOK).probe)
		out, err := a.RefreshCredentials(context.Background(), StoredCredentials{Value: string(old)})
		if err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		var s claudeCodeStoredToken
		_ = json.Unmarshal([]byte(out.Value), &s)
		if s.RefreshToken != "old-rt" || s.AccessToken != "new-at" {
			t.Fatalf("stored = %+v, want kept old-rt + new-at", s)
		}
	})

	t.Run("new one replaces", func(t *testing.T) {
		tok := &fakeClaudeToken{body: `{"access_token":"new-at","refresh_token":"new-rt","expires_in":3600}`}
		a := NewClaudeCodeAdapter(tok.probe, claudeGet(200, claudeProfileOK).probe)
		out, err := a.RefreshCredentials(context.Background(), StoredCredentials{Value: string(old)})
		if err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		var s claudeCodeStoredToken
		_ = json.Unmarshal([]byte(out.Value), &s)
		if s.RefreshToken != "new-rt" {
			t.Fatalf("refresh = %q, want new-rt", s.RefreshToken)
		}
	})

	t.Run("failed refresh returns error, leaves stored untouched", func(t *testing.T) {
		tok := &fakeClaudeToken{err: errors.New("network down")}
		a := NewClaudeCodeAdapter(tok.probe, claudeGet(200, claudeProfileOK).probe)
		if _, err := a.RefreshCredentials(context.Background(), StoredCredentials{Value: string(old)}); err == nil {
			t.Fatal("Refresh error = nil, want a typed failure")
		}
	})
}

// TestClaudeCode_FundingByPlan is mutation row 4: recognized plans map to
// funding at 0.95; an unrecognized plan is no evidence ("" / 0).
func TestClaudeCode_FundingByPlan(t *testing.T) {
	cases := map[string]struct {
		funding string
		conf    float64
	}{
		"Free": {string(FundingFree), 0.95}, "Pro": {string(FundingPaid), 0.95},
		"Max": {string(FundingPaid), 0.95}, "Team": {string(FundingPaid), 0.95},
		"Enterprise": {string(FundingPaid), 0.95}, "Legendary": {"", 0}, "": {"", 0},
	}
	for plan, want := range cases {
		f, c := claudeCodeFundingForPlan(plan)
		if f != want.funding || c != want.conf {
			t.Fatalf("plan %q -> (%q, %v), want (%q, %v)", plan, f, c, want.funding, want.conf)
		}
	}
}

// TestClaudeCode_Quota is mutation row 5 (empty windows) and row 6 (5h/7d
// mapping): a realistic payload maps to the expected windows; an unrecognizable
// payload yields an EMPTY slice.
func TestClaudeCode_Quota(t *testing.T) {
	usage := `{"five_hour":{"utilization":42.5,"resets_at":1700000000},"seven_day":{"utilization":10},"seven_day_claude-x":{"utilization":5}}`
	get := &fakeClaudeGet{byPath: map[string]struct {
		status int
		body   string
	}{"/api/oauth/usage": {200, usage}}}
	a := NewClaudeCodeAdapter((&fakeClaudeToken{}).probe, get.probe)
	res, err := a.FetchQuota(context.Background(), storedClaude("at"))
	if err != nil {
		t.Fatalf("FetchQuota: %v", err)
	}
	byType := map[string]QuotaWindow{}
	for _, w := range res.Windows {
		key := w.WindowType
		if w.WindowKey != "" {
			key += ":" + w.WindowKey
		}
		byType[key] = w
	}
	fh, ok := byType["rolling_5h"]
	if !ok || fh.Used == nil || *fh.Used != 42.5 || fh.Unit != "percent" || fh.DurationSeconds == nil || *fh.DurationSeconds != 18000 {
		t.Fatalf("rolling_5h = %+v, want percent/42.5/18000", fh)
	}
	if fh.ResetAt == nil || *fh.ResetAt != 1700000000 {
		t.Fatalf("rolling_5h resets = %v, want 1700000000", fh.ResetAt)
	}
	if _, ok := byType["rolling_7d"]; !ok {
		t.Fatal("missing rolling_7d window")
	}
	if perModel, ok := byType["rolling_7d:claude-x"]; !ok || perModel.WindowKey != "claude-x" {
		t.Fatalf("missing seven_day_<model> window: %+v", perModel)
	}

	t.Run("a window with no utilization keeps Used nil, never 0", func(t *testing.T) {
		// 0 would read as "0% consumed", i.e. FULL headroom, from a provider that
		// reported no number at all — the 0-as-unknown fail-OPEN 05 §4 forbids
		// (unknown must never read as confidently available). Governor-verified:
		// defaulting Used to 0 left the rest of this suite green.
		g := &fakeClaudeGet{byPath: map[string]struct {
			status int
			body   string
		}{"/api/oauth/usage": {200, `{"five_hour":{"resets_at":1700000000}}`}}}
		a := NewClaudeCodeAdapter((&fakeClaudeToken{}).probe, g.probe)
		res, err := a.FetchQuota(context.Background(), storedClaude("at"))
		if err != nil {
			t.Fatalf("FetchQuota: %v", err)
		}
		if len(res.Windows) != 1 {
			t.Fatalf("windows = %+v, want the one declared window", res.Windows)
		}
		if res.Windows[0].Used != nil {
			t.Fatalf("Used = %v, want nil — the provider reported no utilization", *res.Windows[0].Used)
		}
		if res.Windows[0].ResetAt == nil || *res.Windows[0].ResetAt != 1700000000 {
			t.Fatalf("ResetAt = %v, want the declared reset (the window itself IS evidence)", res.Windows[0].ResetAt)
		}
	})

	t.Run("unrecognizable payload yields empty", func(t *testing.T) {
		g := &fakeClaudeGet{byPath: map[string]struct {
			status int
			body   string
		}{"/api/oauth/usage": {200, `{"nonsense":{"utilization":1}}`}}}
		a := NewClaudeCodeAdapter((&fakeClaudeToken{}).probe, g.probe)
		res, err := a.FetchQuota(context.Background(), storedClaude("at"))
		if err != nil {
			t.Fatalf("FetchQuota: %v", err)
		}
		if len(res.Windows) != 0 {
			t.Fatalf("windows = %+v, want empty (no fabricated window)", res.Windows)
		}
	})
}

// TestClaudeCode_NoCredentialInIdentity is mutation row 7 (and a secret-safety
// canary): a plain non-credential marker planted as the token must not appear
// in the identity Evidence.
func TestClaudeCode_NoCredentialInIdentity(t *testing.T) {
	const marker = "PLAINMARKER-not-credential-shaped"
	tok := &fakeClaudeToken{body: `{"access_token":"` + marker + `","refresh_token":"r","expires_in":1}`}
	a := NewClaudeCodeAdapter(tok.probe, claudeGet(200, claudeProfileOK).probe)
	id, _, err := a.CompleteOAuth(context.Background(), "c", "v", "cb")
	if err != nil {
		t.Fatalf("CompleteOAuth: %v", err)
	}
	ev, _ := json.Marshal(id.Evidence)
	if strings.Contains(string(ev), marker) {
		t.Fatalf("Evidence leaks the access token: %s", ev)
	}
}

func TestClaudeCode_InvalidCredentialOnAuthFailure(t *testing.T) {
	tok := &fakeClaudeToken{body: claudeTokenOK}
	a := NewClaudeCodeAdapter(tok.probe, claudeGet(401, `{}`).probe)
	if _, _, err := a.CompleteOAuth(context.Background(), "c", "v", "cb"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("error = %v, want ErrInvalidCredential on a 401 profile", err)
	}
}

func storedClaude(access string) StoredCredentials {
	v, _ := json.Marshal(claudeCodeStoredToken{AccessToken: access, RefreshToken: "r", ExpiresAt: 1})
	return StoredCredentials{Value: string(v)}
}

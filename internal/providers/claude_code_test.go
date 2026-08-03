package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
)

// fakeClaudeToken records the last JSON body and returns a fixed body/err.
type fakeClaudeToken struct {
	body     string
	err      error
	lastBody []byte
}

func (f *fakeClaudeToken) probe(_ context.Context, _ string, body []byte) ([]byte, error) {
	f.lastBody = body
	if f.err != nil {
		return nil, f.err
	}
	return []byte(f.body), nil
}

// fakeClaudeGet returns a status/body keyed by a substring of the URL path and
// records the last anthropic-beta header it was called with.
type fakeClaudeGet struct {
	byPath map[string]struct {
		status int
		body   string
	}
	err   error
	betas []string
}

func (f *fakeClaudeGet) probe(_ context.Context, reqURL, _, beta string) (int, []byte, error) {
	f.betas = append(f.betas, beta)
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

// claudeProfilePro is a realistic profile for a Pro account: the plan is
// DERIVED from the organization type/flags — the payload has no literal
// "plan" field (legacy 2026-08-03).
const claudeProfilePro = `{"account":{"uuid":"acc-123","email":"a@b.com","display_name":"A","has_claude_pro":true},"organization":{"uuid":"org-1","organization_type":"claude_pro","rate_limit_tier":"pro"}}`
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
	if err := RegisterClaudeCode(reg, (&fakeClaudeToken{}).probe, claudeGet(200, claudeProfilePro).probe); err != nil {
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
// required parameter including code_challenge_method=S256 and the legacy
// `code=true` flag claude.ai's authorize endpoint expects.
func TestClaudeCode_AuthorizeURL(t *testing.T) {
	a := NewClaudeCodeAdapter((&fakeClaudeToken{}).probe, claudeGet(200, claudeProfilePro).probe)
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
		"code": "true",
	} {
		if q.Get(k) != want {
			t.Fatalf("authorize %s = %q, want %q", k, q.Get(k), want)
		}
	}
}

// TestClaudeCode_CompleteExchangesJSONAndIdentity proves the token exchange
// POSTs a JSON body (03 §3's "JSON token exchange" — not a form), carries the
// verifier/client_id, and derives identity + paid funding from the profile's
// organization fields.
func TestClaudeCode_CompleteExchangesJSONAndIdentity(t *testing.T) {
	tok := &fakeClaudeToken{body: claudeTokenOK}
	a := NewClaudeCodeAdapter(tok.probe, claudeGet(200, claudeProfilePro).probe)
	id, creds, err := a.CompleteOAuth(context.Background(), "the-code", "the-verifier", "http://localhost/cb")
	if err != nil {
		t.Fatalf("CompleteOAuth: %v", err)
	}
	if len(tok.lastBody) == 0 || !strings.Contains(string(tok.lastBody), "authorization_code") || !strings.Contains(string(tok.lastBody), "the-verifier") || !strings.Contains(string(tok.lastBody), claudeCodeClientID) {
		t.Fatalf("token body = %q, want JSON with authorization_code grant + verifier + client_id", tok.lastBody)
	}
	if id.ExternalID != "acc-123" || id.Funding != string(FundingPaid) || id.Plan != "Pro Plan" {
		t.Fatalf("identity = %+v, want uuid acc-123 + paid + Pro Plan (derived from org flags)", id)
	}
	var stored claudeCodeStoredToken
	if err := json.Unmarshal([]byte(creds.Value), &stored); err != nil || stored.AccessToken != "at-1" || stored.RefreshToken != "rt-1" {
		t.Fatalf("stored = %+v (err %v), want at-1/rt-1", stored, err)
	}
}

// TestClaudeCode_CodeFragmentIsStripped proves the `#`-suffix quirk: a code
// like "abc#fragment" exchanges as "abc" (legacy 2026-08-03).
func TestClaudeCode_CodeFragmentIsStripped(t *testing.T) {
	tok := &fakeClaudeToken{body: claudeTokenOK}
	a := NewClaudeCodeAdapter(tok.probe, claudeGet(200, claudeProfilePro).probe)
	if _, _, err := a.CompleteOAuth(context.Background(), "abc#frag", "v", "cb"); err != nil {
		t.Fatalf("CompleteOAuth: %v", err)
	}
	if !strings.Contains(string(tok.lastBody), `"code":"abc"`) {
		t.Fatalf("token body = %q, want the fragment-stripped code", tok.lastBody)
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
		a := NewClaudeCodeAdapter(tok.probe, claudeGet(200, claudeProfilePro).probe)
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
		a := NewClaudeCodeAdapter(tok.probe, claudeGet(200, claudeProfilePro).probe)
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
		a := NewClaudeCodeAdapter(tok.probe, claudeGet(200, claudeProfilePro).probe)
		if _, err := a.RefreshCredentials(context.Background(), StoredCredentials{Value: string(old)}); err == nil {
			t.Fatal("Refresh error = nil, want a typed failure")
		}
	})
}

// TestClaudeCode_RefreshSendsClientID proves the refresh JSON body carries
// client_id, exactly as the legacy refresh does.
func TestClaudeCode_RefreshSendsClientID(t *testing.T) {
	old, _ := json.Marshal(claudeCodeStoredToken{AccessToken: "old-at", RefreshToken: "old-rt", ExpiresAt: 1})
	tok := &fakeClaudeToken{body: `{"access_token":"new-at","expires_in":3600}`}
	a := NewClaudeCodeAdapter(tok.probe, claudeGet(200, claudeProfilePro).probe)
	if _, err := a.RefreshCredentials(context.Background(), StoredCredentials{Value: string(old)}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !strings.Contains(string(tok.lastBody), claudeCodeClientID) || !strings.Contains(string(tok.lastBody), "refresh_token") {
		t.Fatalf("refresh body = %q, want client_id + refresh_token", tok.lastBody)
	}
}

// TestClaudeCode_FundingByPlan is mutation row 4: recognized plans (short and
// legacy-derived labels) map to funding at 0.95; an unrecognized plan is no
// evidence ("" / 0).
func TestClaudeCode_FundingByPlan(t *testing.T) {
	cases := map[string]struct {
		funding string
		conf    float64
	}{
		"Free": {string(FundingFree), 0.95}, "Pro": {string(FundingPaid), 0.95},
		"Max": {string(FundingPaid), 0.95}, "Team": {string(FundingPaid), 0.95},
		"Enterprise": {string(FundingPaid), 0.95},
		// Legacy-derived labels must classify identically.
		"Pro Plan": {string(FundingPaid), 0.95}, "Max Plan": {string(FundingPaid), 0.95},
		"Max 5x": {string(FundingPaid), 0.95}, "Max 20x": {string(FundingPaid), 0.95},
		"Team Plan": {string(FundingPaid), 0.95},
		"Legendary": {"", 0}, "": {"", 0},
	}
	for plan, want := range cases {
		f, c := claudeCodeFundingForPlan(plan)
		if f != want.funding || c != want.conf {
			t.Fatalf("plan %q -> (%q, %v), want (%q, %v)", plan, f, c, want.funding, want.conf)
		}
	}
}

// TestClaudeCode_PlanDerivation proves the plan is derived from the profile's
// organization/flags exactly like the legacy formatPlan (2026-08-03).
func TestClaudeCode_PlanDerivation(t *testing.T) {
	prof := func(orgType, tier string, hasPro, hasMax bool) claudeCodeProfile {
		var p claudeCodeProfile
		p.Account.UUID = "u"
		p.Organization.OrganizationType = orgType
		p.Organization.RateLimitTier = tier
		p.Account.HasClaudePro = hasPro
		p.Account.HasClaudeMax = hasMax
		return p
	}
	cases := []struct {
		name string
		p    claudeCodeProfile
		want string
	}{
		{"claude_pro org", prof("claude_pro", "pro", false, false), "Pro Plan"},
		{"has_claude_pro flag", prof("", "", true, false), "Pro Plan"},
		{"team org", prof("claude_team", "", false, false), "Team Plan"},
		{"enterprise org", prof("claude_enterprise", "", false, false), "Enterprise"},
		{"max org", prof("claude_max", "", false, false), "Max Plan"},
		{"max 20x tier", prof("claude_max", "20x", false, true), "Max 20x"},
		{"max 5x tier", prof("claude_max", "5x", false, false), "Max 5x"},
		{"bare uuid is free", prof("", "", false, false), "Free"},
		{"no uuid no org", func() claudeCodeProfile { var p claudeCodeProfile; return p }(), ""},
	}
	for _, c := range cases {
		if got := claudeCodePlanForProfile(c.p); got != c.want {
			t.Fatalf("%s: plan = %q, want %q", c.name, got, c.want)
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

// TestClaudeCode_DiscoveryMapsDeclaredFacts proves /v1/models discovery reads
// the declared capabilities + max_input_tokens and uses the EXTENDED beta
// header (legacy CLAUDE_CODE_BETA), while profile/quota calls use the short
// form — the exact per-call header split the reference makes.
func TestClaudeCode_DiscoveryMapsDeclaredFacts(t *testing.T) {
	list := `{"data":[
		{"id":"claude-opus-4-1","display_name":"Opus 4.1","max_input_tokens":1000000,
		 "capabilities":{"image_input":{"supported":true},"pdf_input":{"supported":true},"thinking":{"supported":true},"structured_outputs":{"supported":true},"code_execution":{"supported":true}}},
		{"id":"claude-sonnet-4","display_name":"Sonnet 4"},
		{"id":"legacy-model"}
	]}`
	get := &fakeClaudeGet{byPath: map[string]struct {
		status int
		body   string
	}{"/v1/models": {200, list}}}
	a := NewClaudeCodeAdapter((&fakeClaudeToken{}).probe, get.probe)
	models, err := a.DiscoverModels(context.Background(), storedClaude("at"))
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if len(models) != 3 {
		t.Fatalf("models = %d, want 3", len(models))
	}
	byID := map[string]DiscoveredModel{}
	for _, m := range models {
		byID[m.ProviderModelID] = m
	}
	opus := byID["claude-opus-4-1"]
	if opus.ContextLength == nil || *opus.ContextLength != 1000000 || opus.MaxInputTokens == nil || *opus.MaxInputTokens != 1000000 {
		t.Fatalf("opus context = %v/%v, want 1000000 from max_input_tokens", opus.ContextLength, opus.MaxInputTokens)
	}
	caps := map[string]bool{}
	for _, c := range opus.Capabilities {
		caps[c] = true
	}
	for _, want := range []string{"chat", "vision", "documents", "reasoning", "thinking", "agents", "structured_output", "tools"} {
		if !caps[want] {
			t.Fatalf("opus capabilities missing %q: %v", want, opus.Capabilities)
		}
	}
	sonnet := byID["claude-sonnet-4"]
	if sonnet.ContextLength != nil {
		t.Fatalf("sonnet context = %v, want nil (not declared)", sonnet.ContextLength)
	}
	if len(sonnet.Capabilities) != 1 || sonnet.Capabilities[0] != "chat" {
		t.Fatalf("sonnet capabilities = %v, want the bare chat base (no model-name inference)", sonnet.Capabilities)
	}

	// The /v1/models call must carry the EXTENDED beta (the legacy CLAUDE_CODE_
	// BETA list) — the one call this test exercises. The profile/usage calls
	// carry the short form; that split is pinned by TestClaudeCode_BetaSplit.
	if len(get.betas) == 0 || get.betas[0] != claudeCodeModelsBeta {
		t.Fatalf("betas seen = %v, want the extended list on /v1/models", get.betas)
	}
}

// TestClaudeCode_BetaSplit pins the per-call beta split the reference makes
// (legacy 2026-08-03): profile, usage, and health carry the SHORT beta
// (oauth-2025-04-20) while /v1/models carries the extended list.
func TestClaudeCode_BetaSplit(t *testing.T) {
	usage := `{"five_hour":{"utilization":10,"resets_at":1700000000}}`
	get := &fakeClaudeGet{byPath: map[string]struct {
		status int
		body   string
	}{
		"/api/oauth/profile": {200, claudeProfilePro},
		"/api/oauth/usage":   {200, usage},
	}}
	a := NewClaudeCodeAdapter((&fakeClaudeToken{body: claudeTokenOK}).probe, get.probe)

	if _, _, err := a.CompleteOAuth(context.Background(), "c", "v", "cb"); err != nil {
		t.Fatalf("CompleteOAuth: %v", err)
	}
	if _, err := a.FetchQuota(context.Background(), storedClaude("at")); err != nil {
		t.Fatalf("FetchQuota: %v", err)
	}
	if _, err := a.CheckAccountHealth(context.Background(), storedClaude("at")); err != nil {
		t.Fatalf("CheckAccountHealth: %v", err)
	}

	if len(get.betas) != 3 {
		t.Fatalf("betas seen = %v, want 3 short-form calls (profile/usage/health)", get.betas)
	}
	for _, b := range get.betas {
		if b != claudeCodeAnthropicBeta {
			t.Fatalf("beta = %q, want the short form %q on profile/usage/health", b, claudeCodeAnthropicBeta)
		}
	}
}

// TestClaudeCode_NoCredentialInIdentity is mutation row 7 (and a secret-safety
// canary): a plain non-credential marker planted as the token must not appear
// in the identity Evidence.
func TestClaudeCode_NoCredentialInIdentity(t *testing.T) {
	const marker = "PLAINMARKER-not-credential-shaped"
	tok := &fakeClaudeToken{body: `{"access_token":"` + marker + `","refresh_token":"r","expires_in":1}`}
	a := NewClaudeCodeAdapter(tok.probe, claudeGet(200, claudeProfilePro).probe)
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

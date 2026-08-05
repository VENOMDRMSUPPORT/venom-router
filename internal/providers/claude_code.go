package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ClaudeCodeID is the catalog slug this adapter registers under.
const ClaudeCodeID ProviderID = "claude-code"

// claude-code OAuth endpoints and the PUBLIC client id (03 §3/§4; verified
// against the legacy implementation 2026-08-03 — docs and legacy AGREE, which
// is why this adapter is first). There is NO client secret: this is a public
// client, so no env var is read and none is required.
const (
	claudeCodeAuthorizeURL = "https://claude.ai/oauth/authorize"
	claudeCodeTokenURL     = "https://platform.claude.com/v1/oauth/token"
	claudeCodeClientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	claudeCodeScopes       = "org:create_api_key user:profile user:inference"

	// claudeCodeRedirectURI is the ONLY redirect_uri claude.ai's authorize
	// endpoint accepts for this public client (verified 2026-08-03 against
	// the live authorize endpoint: BOTH a local /callback path and
	// /api/control/v1/... were rejected with "Redirect URI is not supported
	// by client"). It is a HOSTED page: after the owner authorizes, the
	// browser lands on platform.claude.com which DISPLAYS a copy-paste code
	// in the form <auth_code>#<fragment> — it never redirects back to Venom.
	// The token endpoint validates this exact redirect_uri, so the SAME
	// value is used at authorize and at exchange. The fragment after `#` is
	// the echoed `state` (43-char base64url — the exact length of the
	// state/verifier this adapter's BeginOAuth generates), so the pasted
	// code still binds to the transaction via the state-hash path.
	// Sources: coqu's Claude Code OAuth implementation ("from Claude CLI
	// source"), the ANTHROPIC_AUTH.md reference, and the live rejection.
	claudeCodeRedirectURI = "https://platform.claude.com/oauth/code/callback"

	// ClaudeCodeAPIBase is the identity/discovery/quota base (03 §3). It MUST
	// equal the BuiltinCatalog entry — asserted by a test. The native_oauth
	// transport is handed this same base and appends /v1/messages.
	ClaudeCodeAPIBase = "https://api.anthropic.com"

	claudeCodeProfilePath = "/api/oauth/profile"
	claudeCodeModelsPath  = "/v1/models"
	// claudeCodeUsagePath is NOT a guess: the archived reference implementation
	// calls exactly https://api.anthropic.com/api/oauth/usage (governor-verified
	// 2026-08-03). What still needs live confirmation is the PAYLOAD's plan
	// field name, not this path.
	claudeCodeUsagePath   = "/api/oauth/usage"
	claudeCodeConfidence  = 0.95
	claudeCode5hSeconds   = 18000
	claudeCode7dSeconds   = 604800
	claudeCodeSevenDayKey = "seven_day"
	claudeCodeFiveHourKey = "five_hour"
	claudeCodeSevenDayPfx = "seven_day_"

	// claudeCodeAnthropicBeta is the minimal beta header the profile, usage,
	// and health calls need. The MODELS list additionally requires the extended
	// header below — legacy 2026-08-03 sends the short form on profile/usage
	// and the extended form on /v1/models and /v1/messages (the spec's "Must
	// send Claude-Code identity/beta headers or the API returns 429").
	claudeCodeAnthropicBeta = "oauth-2025-04-20"
	// claudeCodeModelsBeta is the extended beta list the /v1/models call needs
	// (legacy CLAUDE_CODE_BETA, 2026-08-03). It is a superset of the short form.
	claudeCodeModelsBeta = "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27,prompt-caching-scope-2026-01-05"
)

// ErrMissingStableIdentity is returned when an OAuth identity response is a 2xx
// but carries no stable external id — never fabricated, never a fingerprint
// fallback (03 §4: the account uuid is the identity, and a substituted one
// would silently merge two accounts).
var ErrMissingStableIdentity = errors.New("providers: oauth identity response carried no stable external id")

// ClaudeCodeTokenProbe performs the OAuth token-endpoint POST (JSON body, per
// 03 §3's "JSON token exchange" and the legacy implementation); it is used for
// both the authorization_code exchange and the refresh_token grant. body must
// never be logged. Mirrors AntigravityTokenProbe's role but with the JSON wire
// shape claude.ai's token endpoint actually speaks.
type ClaudeCodeTokenProbe func(ctx context.Context, tokenURL string, body []byte) ([]byte, error)

// ClaudeCodeGetProbe performs an authenticated GET, returning the raw status +
// body so the adapter can classify (identity/health) and parse. anthropicBeta
// is the per-call beta header value (profile/usage use the short form; /v1/models
// uses the extended form). The required claude-code headers (anthropic-version,
// X-App, claude-cli UA) are applied by the concrete implementation in httpapi,
// never here. accessToken must never be logged.
type ClaudeCodeGetProbe func(ctx context.Context, url, accessToken, anthropicBeta string) (statusCode int, body []byte, err error)

type claudeCodeTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// claudeCodeProfile is the subset of GET /api/oauth/profile this adapter reads
// (03 §3: account.uuid is the stable external id; the plan is DERIVED from the
// organization type / rate-limit tier / pro-max flags, exactly as the legacy
// implementation derives it — the payload has no literal "plan" field).
type claudeCodeProfile struct {
	Account struct {
		UUID         string `json:"uuid"`
		Email        string `json:"email"`
		DisplayName  string `json:"display_name"`
		HasClaudePro bool   `json:"has_claude_pro"`
		HasClaudeMax bool   `json:"has_claude_max"`
	} `json:"account"`
	Organization struct {
		UUID             string `json:"uuid"`
		OrganizationType string `json:"organization_type"`
		RateLimitTier    string `json:"rate_limit_tier"`
	} `json:"organization"`
}

// claudeCodePlanForProfile derives the human plan label from the profile the
// same way the legacy formatPlan does (2026-08-03): org type and pro/max flags
// decide, and an account uuid with no recognized org reads as Free. An
// unrecognized combination returns "" (no evidence), never a guess.
func claudeCodePlanForProfile(p claudeCodeProfile) string {
	orgType := p.Organization.OrganizationType
	tier := p.Organization.RateLimitTier
	switch {
	case orgType == "claude_pro" || p.Account.HasClaudePro:
		return "Pro Plan"
	case orgType == "claude_team":
		return "Team Plan"
	case orgType == "claude_enterprise":
		return "Enterprise"
	case orgType == "claude_max" || p.Account.HasClaudeMax:
		switch {
		case strings.Contains(tier, "20x"):
			return "Max 20x"
		case strings.Contains(tier, "5x"):
			return "Max 5x"
		default:
			return "Max Plan"
		}
	case p.Account.UUID != "":
		return "Free"
	default:
		return ""
	}
}

type claudeCodeStoredToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
}

// ClaudeCodeAdapter implements OAuthAdapter + Identity/Discovery/Quota/Health
// for claude-code (03 §3), a PUBLIC OAuth client (PKCE, no secret). Its route
// speaks the anthropic_messages wire schema (registered below).
type ClaudeCodeAdapter struct {
	tokenProbe ClaudeCodeTokenProbe
	getProbe   ClaudeCodeGetProbe
}

// NewClaudeCodeAdapter builds the adapter over the two injected seams.
func NewClaudeCodeAdapter(tokenProbe ClaudeCodeTokenProbe, getProbe ClaudeCodeGetProbe) *ClaudeCodeAdapter {
	return &ClaudeCodeAdapter{tokenProbe: tokenProbe, getProbe: getProbe}
}

// splitOAuthCode handles the claude-code paste quirk: the platform page
// displays a 92-char string `<auth_code>#<fragment>` where ONLY the part
// before `#` (48 chars) is the actual authorization code. The fragment after
// `#` is the echoed `state` (43-char base64url) and is returned separately so
// the exchange can hand it back to the token endpoint ("state when the
// returned value includes it" — ANTHROPIC_AUTH.md / legacy 2026-08-03).
func splitOAuthCode(code string) (authCode, state string) {
	if i := strings.Index(code, "#"); i >= 0 {
		return code[:i], code[i+1:]
	}
	return code, ""
}

// RequiresManualCode reports true: claude-code's OAuth NEVER redirects back to
// Venom. The authorize flow ends on Anthropic's HOSTED code page
// (claudeCodeRedirectURI), which displays a code the owner must copy and paste
// back into the dashboard. The enrollment UI shows a paste field instead of a
// popup-and-poll when this is true (see httpapi's ServeBegin and the Connect
// dialog). This is a STRUCTURAL fact about the provider's registered client,
// not a per-account state.
func (a *ClaudeCodeAdapter) RequiresManualCode() bool { return true }

// BeginOAuth builds the authorize URL (pure string construction). The verifier
// is the framework's; code_challenge_method is always S256; `code=true` tells
// claude.ai to use the manual code flow (hosted redirect that displays a
// copy-paste code). redirectURI is IGNORED on purpose: this public client is
// registered with exactly one redirect_uri — claudeCodeRedirectURI — and
// claude.ai rejects any other with "Redirect URI is not supported by client"
// (live-verified 2026-08-03). The token exchange uses the SAME constant, so
// the two legs always match.
func (a *ClaudeCodeAdapter) BeginOAuth(_ context.Context, _ /*redirectURI*/, state, pkceChallenge string) (string, error) {
	q := url.Values{}
	q.Set("client_id", claudeCodeClientID)
	q.Set("redirect_uri", claudeCodeRedirectURI)
	q.Set("response_type", "code")
	q.Set("code_challenge", pkceChallenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	q.Set("scope", claudeCodeScopes)
	q.Set("code", "true")
	return claudeCodeAuthorizeURL + "?" + q.Encode(), nil
}

// CompleteOAuth exchanges the pasted code over a form-encoded body (public
// client — no secret; the token endpoint REJECTS a JSON body with
// invalid_grant — coqu, "from Claude CLI source"), then fetches the profile
// for the stable account uuid. code is the RAW pasted string
// `<auth_code>#<fragment>`: the part before `#` is exchanged, and the fragment
// (the echoed state) is handed back to the token endpoint. A 2xx profile
// without a uuid is a typed failure, never a fabricated identity. The plan is
// derived from the profile's org/flags (see claudeCodePlanForProfile); funding
// follows it.
func (a *ClaudeCodeAdapter) CompleteOAuth(ctx context.Context, code, pkceVerifier, _ /*redirectURI*/ string) (IdentityResult, StoredCredentials, error) {
	authCode, state := splitOAuthCode(code)
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", claudeCodeClientID)
	form.Set("code", authCode)
	form.Set("code_verifier", pkceVerifier)
	form.Set("redirect_uri", claudeCodeRedirectURI)
	if state != "" {
		form.Set("state", state)
	}

	tok, err := a.exchangeToken(ctx, form)
	if err != nil {
		return IdentityResult{}, StoredCredentials{}, err
	}

	profile, err := a.fetchProfile(ctx, tok.AccessToken)
	if err != nil {
		return IdentityResult{}, StoredCredentials{}, err
	}
	if profile.Account.UUID == "" {
		return IdentityResult{}, StoredCredentials{}, ErrMissingStableIdentity
	}

	plan := claudeCodePlanForProfile(profile)
	funding, confidence := claudeCodeFundingForPlan(plan)
	identity := IdentityResult{
		ExternalID: profile.Account.UUID,
		Email:      profile.Account.Email,
		Plan:       plan,
		Funding:    funding,
		Confidence: confidence,
	}
	if plan != "" {
		identity.Evidence = map[string]any{"plan": plan}
	}

	stored, err := marshalClaudeCodeToken(tok.AccessToken, tok.RefreshToken, tok.ExpiresIn)
	if err != nil {
		return IdentityResult{}, StoredCredentials{}, err
	}
	return identity, stored, nil
}

// RefreshCredentials re-mints the access token (form-encoded body carrying
// client_id, exactly as the reference refresh does). A NEW refresh token
// replaces the old one; if none is returned the existing one is KEPT (never
// blanked). A failed refresh returns a typed error and leaves the stored
// credential untouched (the caller keeps the old envelope).
func (a *ClaudeCodeAdapter) RefreshCredentials(ctx context.Context, creds StoredCredentials) (StoredCredentials, error) {
	var stored claudeCodeStoredToken
	if err := json.Unmarshal([]byte(creds.Value), &stored); err != nil {
		return StoredCredentials{}, fmt.Errorf("providers: claude-code: refresh: parse stored credentials: %w", err)
	}
	if stored.RefreshToken == "" {
		return StoredCredentials{}, errors.New("providers: claude-code: refresh: no refresh token available")
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", claudeCodeClientID)
	form.Set("refresh_token", stored.RefreshToken)

	tok, err := a.exchangeToken(ctx, form)
	if err != nil {
		return StoredCredentials{}, err
	}
	newRefresh := tok.RefreshToken
	if newRefresh == "" {
		newRefresh = stored.RefreshToken
	}
	return marshalClaudeCodeToken(tok.AccessToken, newRefresh, tok.ExpiresIn)
}

func (a *ClaudeCodeAdapter) exchangeToken(ctx context.Context, form url.Values) (claudeCodeTokenResponse, error) {
	respBody, err := a.tokenProbe(ctx, claudeCodeTokenURL, []byte(form.Encode()))
	if err != nil {
		return claudeCodeTokenResponse{}, fmt.Errorf("providers: claude-code: token endpoint: %w", err)
	}
	var tok claudeCodeTokenResponse
	if err := json.Unmarshal(respBody, &tok); err != nil {
		return claudeCodeTokenResponse{}, fmt.Errorf("providers: claude-code: parse token response: %w", err)
	}
	if tok.AccessToken == "" {
		return claudeCodeTokenResponse{}, ErrInvalidCredential
	}
	return tok, nil
}

func marshalClaudeCodeToken(access, refresh string, expiresIn int64) (StoredCredentials, error) {
	value, err := json.Marshal(claudeCodeStoredToken{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second).Unix(),
	})
	if err != nil {
		return StoredCredentials{}, fmt.Errorf("providers: claude-code: marshal stored credentials: %w", err)
	}
	return StoredCredentials{Value: string(value)}, nil
}

func (a *ClaudeCodeAdapter) fetchProfile(ctx context.Context, accessToken string) (claudeCodeProfile, error) {
	status, body, err := a.getProbe(ctx, ClaudeCodeAPIBase+claudeCodeProfilePath, accessToken, claudeCodeAnthropicBeta)
	if err != nil {
		return claudeCodeProfile{}, fmt.Errorf("providers: claude-code: fetch profile: %w", err)
	}
	if status == 401 || status == 403 {
		return claudeCodeProfile{}, ErrInvalidCredential
	}
	if status < 200 || status >= 300 {
		return claudeCodeProfile{}, ErrProviderUnavailable
	}
	var profile claudeCodeProfile
	if err := json.Unmarshal(body, &profile); err != nil {
		return claudeCodeProfile{}, fmt.Errorf("providers: claude-code: parse profile: %w", err)
	}
	return profile, nil
}

// FetchIdentity implements IdentityAdapter over the stored access token.
func (a *ClaudeCodeAdapter) FetchIdentity(ctx context.Context, creds StoredCredentials) (IdentityResult, error) {
	token, err := claudeCodeAccessToken(creds)
	if err != nil {
		return IdentityResult{}, err
	}
	profile, err := a.fetchProfile(ctx, token)
	if err != nil {
		return IdentityResult{}, err
	}
	if profile.Account.UUID == "" {
		return IdentityResult{}, ErrMissingStableIdentity
	}
	plan := claudeCodePlanForProfile(profile)
	funding, confidence := claudeCodeFundingForPlan(plan)
	identity := IdentityResult{ExternalID: profile.Account.UUID, Email: profile.Account.Email, Plan: plan, Funding: funding, Confidence: confidence}
	if plan != "" {
		identity.Evidence = map[string]any{"plan": plan}
	}
	return identity, nil
}

// claudeCodeFundingForPlan maps a plan label onto funding + confidence. The
// label may be the legacy-derived form ("Pro Plan", "Max 20x", "Team Plan",
// "Enterprise", "Free") or the short form ("Pro", "Max", "Team"); any
// recognized label is real evidence (0.95), anything else is NO evidence
// ("" / 0), so an unrecognized tier never outranks a later correct
// classification. if/else, not a slug switch.
func claudeCodeFundingForPlan(plan string) (string, float64) {
	normalized := strings.ToLower(strings.TrimSpace(plan))
	normalized = strings.NewReplacer(" ", "_", "-", "_").Replace(normalized)
	switch {
	case strings.Contains(normalized, "free"):
		return string(FundingFree), claudeCodeConfidence
	case strings.Contains(normalized, "pro"),
		strings.Contains(normalized, "max"),
		strings.Contains(normalized, "team"),
		strings.Contains(normalized, "enterprise"):
		return string(FundingPaid), claudeCodeConfidence
	default:
		return "", 0
	}
}

// claudeCodeModelList is the subset of GET /v1/models entries this adapter
// reads: the declared capabilities object and the input-token limit (03 §3).
//
// 2026-08-05 hybrid-capabilities-and-context (Task 2) finding: the official
// payload (verified verbatim against the legacy reference's
// ClaudeApiModelEntry type, venom-router-legacy
// src/lib/providers/claude-models-snapshot.ts:15-21) carries NO
// output-token-limit field — only max_input_tokens. DiscoveredModel.
// MaxOutputTokens therefore stays nil for every claude-code model; adding a
// field here would be guessing a number the provider never sent (spec D6).
type claudeCodeModelList struct {
	Data []claudeCodeModelEntry `json:"data"`
}

type claudeCodeModelEntry struct {
	ID             string           `json:"id"`
	DisplayName    string           `json:"display_name"`
	MaxInputTokens *int             `json:"max_input_tokens"`
	Capabilities   *claudeModelCaps `json:"capabilities"`
}

type claudeModelCaps struct {
	ImageInput        *claudeCapFlag `json:"image_input"`
	PDFInput          *claudeCapFlag `json:"pdf_input"`
	Thinking          *claudeCapFlag `json:"thinking"`
	StructuredOutputs *claudeCapFlag `json:"structured_outputs"`
	CodeExecution     *claudeCapFlag `json:"code_execution"`
}

type claudeCapFlag struct {
	Supported bool `json:"supported"`
}

// claudeCapabilities maps the provider's DECLARED capability flags onto the
// operation vocabulary (04 §2). Only explicit fields are read; a missing
// capabilities object yields just the base chat capability (the /v1/models
// list is a chat-model list), never a model-name-derived guess — capability
// names never come from the model id in this project (README §2.1).
//
// "reasoning" is a BASELINE label, not gated on the thinking flag: it is
// emitted whenever a capabilities object is declared at all, mirroring the
// verified legacy reference verbatim (venom-router-legacy
// src/lib/providers/claude-models-snapshot.ts:23-44, mapClaudeApiCapabilities
// — every claude-code model with a declared capabilities object is treated
// as reasoning-capable at a basic level; thinking.supported additionally
// unlocks "thinking"/"agents"). Before 2026-08-05's operation-vocabulary
// unfreeze (models.OperationReasoning) this string was silently dropped by
// ParseOperation regardless of value, so the unconditional emission was
// inert; it is now load-bearing, pinned by
// TestClaudeCode_ReasoningCapabilityAndNoOutputLimit. "chat" is always the
// first element regardless of any flag combination — a reasoning-only
// offering must never occur for claude-code, or classification.go's
// allImageGeneration precedent could misclassify it as non-routable.
func claudeCapabilities(caps *claudeModelCaps) []string {
	if caps == nil {
		return []string{"chat"}
	}
	out := []string{"chat"}
	if caps.ImageInput != nil && caps.ImageInput.Supported {
		out = append(out, "vision")
	}
	if caps.PDFInput != nil && caps.PDFInput.Supported {
		out = append(out, "documents")
	}
	if caps.Thinking != nil && caps.Thinking.Supported {
		out = append(out, "reasoning", "thinking", "agents")
	} else {
		out = append(out, "reasoning")
	}
	if caps.StructuredOutputs != nil && caps.StructuredOutputs.Supported {
		out = append(out, "structured_output")
	}
	if caps.CodeExecution != nil && caps.CodeExecution.Supported {
		out = append(out, "tools")
	}
	return out
}

// DiscoverModels reads GET /v1/models with the extended beta header + X-App
// (the seam applies the headers) and reports only explicit facts: id, display
// name, declared capabilities, and the declared input-token limit. No limit
// field is fabricated when absent.
func (a *ClaudeCodeAdapter) DiscoverModels(ctx context.Context, creds StoredCredentials) ([]DiscoveredModel, error) {
	token, err := claudeCodeAccessToken(creds)
	if err != nil {
		return nil, err
	}
	status, body, err := a.getProbe(ctx, ClaudeCodeAPIBase+claudeCodeModelsPath, token, claudeCodeModelsBeta)
	if err != nil {
		return nil, fmt.Errorf("providers: claude-code: discover models: %w", err)
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("providers: claude-code: discover models: status %d", status)
	}
	var list claudeCodeModelList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("providers: claude-code: parse models: %w", err)
	}
	out := make([]DiscoveredModel, 0, len(list.Data))
	for _, m := range list.Data {
		if m.ID == "" {
			continue
		}
		display := m.DisplayName
		if display == "" {
			display = m.ID
		}
		model := DiscoveredModel{
			ProviderModelID: m.ID,
			DisplayName:     display,
			Capabilities:    claudeCapabilities(m.Capabilities),
		}
		if m.MaxInputTokens != nil && *m.MaxInputTokens > 0 {
			model.ContextLength = m.MaxInputTokens
			model.MaxInputTokens = m.MaxInputTokens
		}
		out = append(out, model)
	}
	return out, nil
}

// claudeCodeUsageWindow is one window object in the usage payload.
type claudeCodeUsageWindow struct {
	Utilization *float64        `json:"utilization"`
	ResetsAt    json.RawMessage `json:"resets_at"`
}

// FetchQuota maps the usage payload's five_hour / seven_day[_<model>] windows
// onto QuotaWindows (03 §3). utilization is a PERCENT USED, so it populates
// Used; Total and Remaining are left nil because the payload does not state
// them and fabricating Total=100 would assert a ceiling the provider never
// reported (see the batch report). A payload with no recognizable window yields
// an EMPTY Windows slice — never a fabricated window.
func (a *ClaudeCodeAdapter) FetchQuota(ctx context.Context, creds StoredCredentials) (QuotaResult, error) {
	token, err := claudeCodeAccessToken(creds)
	if err != nil {
		return QuotaResult{}, err
	}
	status, body, err := a.getProbe(ctx, ClaudeCodeAPIBase+claudeCodeUsagePath, token, claudeCodeAnthropicBeta)
	if err != nil {
		return QuotaResult{}, fmt.Errorf("providers: claude-code: fetch quota: %w", err)
	}
	if status < 200 || status >= 300 {
		return QuotaResult{}, fmt.Errorf("providers: claude-code: fetch quota: status %d", status)
	}
	var raw map[string]claudeCodeUsageWindow
	if err := json.Unmarshal(body, &raw); err != nil {
		return QuotaResult{}, fmt.Errorf("providers: claude-code: parse usage: %w", err)
	}
	var windows []QuotaWindow
	for key, w := range raw {
		windowType, duration, windowKey, ok := claudeCodeWindowKind(key)
		if !ok {
			continue
		}
		qw := QuotaWindow{
			Unit:            "percent",
			WindowType:      windowType,
			WindowKey:       windowKey,
			DurationSeconds: intPtr(duration),
			Confidence:      claudeCodeConfidence,
		}
		if w.Utilization != nil {
			used := *w.Utilization
			qw.Used = &used
		}
		if resetAt := parseResetsAt(w.ResetsAt); resetAt != nil {
			qw.ResetAt = resetAt
		}
		windows = append(windows, qw)
	}
	return QuotaResult{Windows: windows}, nil
}

// claudeCodeWindowKind classifies a usage payload key. if/else, not a slug
// switch. seven_day_<model> variants carry the model in WindowKey.
func claudeCodeWindowKind(key string) (windowType string, duration int, windowKey string, ok bool) {
	if key == claudeCodeFiveHourKey {
		return "rolling_5h", claudeCode5hSeconds, "", true
	}
	if key == claudeCodeSevenDayKey {
		return "rolling_7d", claudeCode7dSeconds, "", true
	}
	if strings.HasPrefix(key, claudeCodeSevenDayPfx) {
		return "rolling_7d", claudeCode7dSeconds, strings.TrimPrefix(key, claudeCodeSevenDayPfx), true
	}
	return "", 0, "", false
}

// parseResetsAt reads a resets_at value as either unix seconds (a JSON number)
// or an RFC3339 string, returning nil for absent/unparseable.
func parseResetsAt(raw json.RawMessage) *int64 {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if n, err := strconv.ParseInt(strings.Trim(string(raw), `"`), 10, 64); err == nil {
		return &n
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			unix := t.Unix()
			return &unix
		}
	}
	return nil
}

// CheckAccountHealth reuses the profile GET as the credential-authentic call.
func (a *ClaudeCodeAdapter) CheckAccountHealth(ctx context.Context, creds StoredCredentials) (HealthObservation, error) {
	return a.checkHealth(ctx, creds, "account")
}

// CheckOfferingHealth reuses the account-level probe (no per-model endpoint).
func (a *ClaudeCodeAdapter) CheckOfferingHealth(ctx context.Context, creds StoredCredentials, _ string) (HealthObservation, error) {
	return a.checkHealth(ctx, creds, "offering")
}

func (a *ClaudeCodeAdapter) checkHealth(ctx context.Context, creds StoredCredentials, scope string) (HealthObservation, error) {
	token, err := claudeCodeAccessToken(creds)
	if err != nil {
		return observationFromValidation(ValidationUnavailable, scope, time.Now().Unix()), nil
	}
	status, _, err := a.getProbe(ctx, ClaudeCodeAPIBase+claudeCodeProfilePath, token, claudeCodeAnthropicBeta)
	if err != nil {
		return observationFromValidation(ValidationUnavailable, scope, time.Now().Unix()), nil
	}
	return observationFromValidation(classifyOAuthStatus(status), scope, time.Now().Unix()), nil
}

// classifyOAuthStatus maps an authenticated GET's status onto the 3-way
// validation classification: 401/403 -> invalid; 2xx -> valid; else ->
// unavailable (retryable). Switches on an int, never a slug string.
func classifyOAuthStatus(status int) ValidationStatus {
	switch {
	case status == 401 || status == 403:
		return ValidationInvalid
	case status >= 200 && status < 300:
		return ValidationValid
	default:
		return ValidationUnavailable
	}
}

// claudeCodeAccessToken extracts the current access token from the stored
// credential envelope.
func claudeCodeAccessToken(creds StoredCredentials) (string, error) {
	var stored claudeCodeStoredToken
	if err := json.Unmarshal([]byte(creds.Value), &stored); err != nil {
		return "", fmt.Errorf("providers: claude-code: parse stored credentials: %w", err)
	}
	if stored.AccessToken == "" {
		return "", ErrInvalidCredential
	}
	return stored.AccessToken, nil
}

func intPtr(v int) *int { return &v }

// RegisterClaudeCode registers the claude-code OAuth + Identity + Discovery +
// Quota + Health adapters into reg with the native_oauth transport and the
// anthropic_messages wire schema.
func RegisterClaudeCode(reg *Registry, tokenProbe ClaudeCodeTokenProbe, getProbe ClaudeCodeGetProbe) error {
	adapter := NewClaudeCodeAdapter(tokenProbe, getProbe)
	return reg.Register(Definition{
		ID:         ClaudeCodeID,
		AuthMode:   AuthModeOAuth,
		Transport:  TransportKindNativeOAuth,
		WireSchema: WireSchemaAnthropicMessages,
		OAuth:      adapter,
		Identity:   adapter,
		Discovery:  adapter,
		Quota:      adapter,
		Health:     adapter,
	})
}

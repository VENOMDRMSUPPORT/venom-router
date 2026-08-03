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

	// ClaudeCodeAPIBase is the identity/discovery/quota base (03 §3). It MUST
	// equal the BuiltinCatalog entry — asserted by a test. The native_oauth
	// transport is handed this same base and appends /v1/messages.
	ClaudeCodeAPIBase = "https://api.anthropic.com"

	claudeCodeProfilePath = "/api/oauth/profile"
	claudeCodeModelsPath  = "/v1/models"
	claudeCodeUsagePath   = "/api/oauth/usage" // endpoint to confirm on live re-verification
	claudeCodeConfidence  = 0.95
	claudeCode5hSeconds   = 18000
	claudeCode7dSeconds   = 604800
	claudeCodeSevenDayKey = "seven_day"
	claudeCodeFiveHourKey = "five_hour"
	claudeCodeSevenDayPfx = "seven_day_"
)

// ErrMissingStableIdentity is returned when an OAuth identity response is a 2xx
// but carries no stable external id — never fabricated, never a fingerprint
// fallback (03 §4: the account uuid is the identity, and a substituted one
// would silently merge two accounts).
var ErrMissingStableIdentity = errors.New("providers: oauth identity response carried no stable external id")

// ClaudeCodeTokenProbe performs the OAuth token-endpoint POST (form body); it
// is used for both the authorization_code exchange and the refresh_token
// grant. form must never be logged. Mirrors AntigravityTokenProbe.
type ClaudeCodeTokenProbe func(ctx context.Context, tokenURL string, form url.Values) ([]byte, error)

// ClaudeCodeGetProbe performs an authenticated GET, returning the raw status +
// body so the adapter can classify (identity/health) and parse. The required
// claude-code headers (anthropic-version/beta, X-App, claude-cli UA) are
// applied by the concrete implementation in httpapi, never here. accessToken
// must never be logged.
type ClaudeCodeGetProbe func(ctx context.Context, url, accessToken string) (statusCode int, body []byte, err error)

type claudeCodeTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// claudeCodeProfile is the subset of GET /api/oauth/profile this adapter reads
// (03 §3: account.uuid is the stable external id). The plan field name is to be
// confirmed on live re-verification; the funding mapping treats an
// unrecognized/absent plan as no evidence.
type claudeCodeProfile struct {
	Account struct {
		UUID  string `json:"uuid"`
		Email string `json:"email"`
		Plan  string `json:"plan"`
	} `json:"account"`
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

// BeginOAuth builds the authorize URL (pure string construction). The verifier
// is the framework's; code_challenge_method is always S256.
func (a *ClaudeCodeAdapter) BeginOAuth(_ context.Context, redirectURI, state, pkceChallenge string) (string, error) {
	q := url.Values{}
	q.Set("client_id", claudeCodeClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("code_challenge", pkceChallenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	q.Set("scope", claudeCodeScopes)
	return claudeCodeAuthorizeURL + "?" + q.Encode(), nil
}

// CompleteOAuth exchanges the code (public client — no secret), then fetches the
// profile for the stable account uuid. A 2xx profile without a uuid is a typed
// failure, never a fabricated identity.
func (a *ClaudeCodeAdapter) CompleteOAuth(ctx context.Context, code, pkceVerifier, redirectURI string) (IdentityResult, StoredCredentials, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", claudeCodeClientID)
	form.Set("code", code)
	form.Set("code_verifier", pkceVerifier)
	form.Set("redirect_uri", redirectURI)

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

	funding, confidence := claudeCodeFundingForPlan(profile.Account.Plan)
	identity := IdentityResult{
		ExternalID: profile.Account.UUID,
		Email:      profile.Account.Email,
		Plan:       profile.Account.Plan,
		Funding:    funding,
		Confidence: confidence,
	}
	if profile.Account.Plan != "" {
		identity.Evidence = map[string]any{"plan": profile.Account.Plan}
	}

	stored, err := marshalClaudeCodeToken(tok.AccessToken, tok.RefreshToken, tok.ExpiresIn)
	if err != nil {
		return IdentityResult{}, StoredCredentials{}, err
	}
	return identity, stored, nil
}

// RefreshCredentials re-mints the access token. A NEW refresh token replaces the
// old one; if none is returned the existing one is KEPT (never blanked). A
// failed refresh returns a typed error and leaves the stored credential
// untouched (the caller keeps the old envelope).
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
	body, err := a.tokenProbe(ctx, claudeCodeTokenURL, form)
	if err != nil {
		return claudeCodeTokenResponse{}, fmt.Errorf("providers: claude-code: token endpoint: %w", err)
	}
	var tok claudeCodeTokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
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
	status, body, err := a.getProbe(ctx, ClaudeCodeAPIBase+claudeCodeProfilePath, accessToken)
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
	funding, confidence := claudeCodeFundingForPlan(profile.Account.Plan)
	return IdentityResult{ExternalID: profile.Account.UUID, Email: profile.Account.Email, Plan: profile.Account.Plan, Funding: funding, Confidence: confidence}, nil
}

// claudeCodeFundingForPlan maps the reported plan onto funding + confidence.
// Recognized paid tiers (Pro/Max/Team/Enterprise) and Free are real evidence
// (0.95); anything else is NO evidence ("" / 0), so an unrecognized tier never
// outranks a later correct classification. if/else, not a slug switch.
func claudeCodeFundingForPlan(plan string) (string, float64) {
	p := strings.ToLower(strings.TrimSpace(plan))
	if p == "free" {
		return string(FundingFree), claudeCodeConfidence
	}
	if p == "pro" || p == "max" || p == "team" || p == "enterprise" {
		return string(FundingPaid), claudeCodeConfidence
	}
	return "", 0
}

type claudeCodeModelList struct {
	Data []struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"data"`
}

// DiscoverModels reads GET /v1/models. Only explicit facts are reported:
// capabilities are chat unless the response declares more (it declares none
// today), and there are no limit fields to read, so limits stay nil.
func (a *ClaudeCodeAdapter) DiscoverModels(ctx context.Context, creds StoredCredentials) ([]DiscoveredModel, error) {
	token, err := claudeCodeAccessToken(creds)
	if err != nil {
		return nil, err
	}
	status, body, err := a.getProbe(ctx, ClaudeCodeAPIBase+claudeCodeModelsPath, token)
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
		out = append(out, DiscoveredModel{ProviderModelID: m.ID, DisplayName: display, Capabilities: []string{"chat"}})
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
	status, body, err := a.getProbe(ctx, ClaudeCodeAPIBase+claudeCodeUsagePath, token)
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
	status, _, err := a.getProbe(ctx, ClaudeCodeAPIBase+claudeCodeProfilePath, token)
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

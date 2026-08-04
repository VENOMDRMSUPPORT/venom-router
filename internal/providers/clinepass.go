package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ClinePassID is the catalog slug this adapter registers under.
const ClinePassID ProviderID = "clinepass"

// clinepass endpoints (03 §3; verified against the legacy implementation
// 2026-08-03). The base MUST equal the BuiltinCatalog entry — asserted by a
// test. The paths below are appended to it.
const (
	ClinePassBaseURL = "https://api.cline.bot"

	clinePassAuthorizePath = "/api/v1/auth/authorize"
	clinePassTokenPath     = "/api/v1/auth/token"
	clinePassRefreshPath   = "/api/v1/auth/refresh"
	clinePassIdentityPath  = "/api/v1/users/me"
	clinePassModelsPath    = "/api/v1/ai/cline/recommended-models"
	// clinePassBalancePath is a TEMPLATE: the real quota call is
	// /api/v1/users/{id}/balance (the numeric user id from /users/me), NOT
	// /users/me/balance — the legacy reference confirmed the dashboard's own
	// balance widget calls it per-user. See FetchQuota.
	clinePassBalancePath     = "/api/v1/users/%s/balance"
	clinePassUsageLimitsPath = "/api/v1/users/me/plan/usage-limits"
	clinePassConfidence      = 1.0
	clinePassMicroPerUSD     = 1_000_000
	clinePassDefaultExpiry   = 3600 // seconds; used only when the token omits expiresAt
)

// ErrSubscriptionRequired is returned when the credential is valid but the
// authenticated account has no active ClinePass entitlement. It is distinct
// from invalid credentials and provider availability: re-login with the same
// unsubscribed account cannot make this integration routable.
var ErrSubscriptionRequired = errors.New("providers: active ClinePass subscription required")

// ClinePassPostProbe performs a JSON POST (token / refresh — clinepass uses
// JSON bodies, not form). It returns the raw status + body so the adapter can
// classify and parse. body must never be logged (it carries the code / refresh
// token).
type ClinePassPostProbe func(ctx context.Context, url string, body []byte) (statusCode int, respBody []byte, err error)

// ClinePassGetProbe performs an authenticated GET (identity / discovery /
// quota). The concrete implementation applies the wire decoration clinepass
// needs (the workos: Bearer prefix and the cline headers); accessToken is never
// logged. It returns the raw status so the adapter can classify. A blank
// accessToken means NO Authorization header is sent — the recommended-models
// endpoint is public and must not receive a workos:-prefixed header.
type ClinePassGetProbe func(ctx context.Context, url, accessToken string) (statusCode int, body []byte, err error)

// clinePassEnvelope is the wire envelope EVERY authenticated clinepass JSON
// endpoint returns: {success, data, error} (legacy ClineEnvelope, 2026-08-03).
type clinePassEnvelope[T any] struct {
	Success bool   `json:"success"`
	Data    *T     `json:"data"`
	Error   string `json:"error"`
}

// clinePassUserInfo is the user object carried inside the TOKEN RESPONSE's
// data (legacy ClineTokenUserInfo, 2026-08-03) — clineUserId is the stable
// external id (03 §3), subject is the documented fallback.
type clinePassUserInfo struct {
	Subject     string   `json:"subject"`
	Email       string   `json:"email"`
	Name        string   `json:"name"`
	ClineUserID string   `json:"clineUserId"`
	Accounts    []string `json:"accounts"`
}

// clinePassTokenData is the token endpoint's data object: camelCase access/
// refresh tokens, an ISO-string expiresAt, and the userInfo identity. This is
// the REAL shape — the earlier top-level access_token/refresh_token/expires_in
// parsing was drift the legacy reference corrected (2026-08-03).
type clinePassTokenData struct {
	AccessToken  string             `json:"accessToken"`
	RefreshToken string             `json:"refreshToken"`
	ExpiresAt    string             `json:"expiresAt"`
	UserInfo     *clinePassUserInfo `json:"userInfo"`
}

type clinePassIdentity struct {
	ClineUserID string `json:"clineUserId"`
	// ID is the numeric /users/me id (legacy balance path). Some responses
	// omit clineUserId and only carry this field — treat it as the stable id.
	ID    string `json:"id"`
	Email string `json:"email"`
}

// clinePassEmbeddedPayload is the shape Cline sometimes puts DIRECTLY in the
// callback `code` query param (base64 JSON + signature): tokens already minted,
// no separate authorization_code exchange. Live 2026-08-03 evidence: code
// prefixes decode to {"accessToken":...}.
type clinePassEmbeddedPayload struct {
	AccessToken  string             `json:"accessToken"`
	RefreshToken string             `json:"refreshToken"`
	Email        string             `json:"email"`
	Name         string             `json:"name"`
	ExpiresAt    string             `json:"expiresAt"`
	UserInfo     *clinePassUserInfo `json:"userInfo"`
}

type clinePassStoredToken struct {
	AccessToken  string             `json:"access_token"`
	RefreshToken string             `json:"refresh_token"`
	ExpiresAt    int64              `json:"expires_at"`
	UserInfo     *clinePassUserInfo `json:"user_info,omitempty"`
}

// ClinePassAdapter implements OAuthAdapter + Identity/Discovery/Quota/Health for
// clinepass (03 §3): a paid-subscription OAuth extension flow where a PKCE
// verifier IS generated by the framework but is NOT sent on the wire. Funding
// policy is PAID and LOCKED (the catalog's Locked flag makes an owner override
// fail funding_locked), while eligibility separately requires an active pass. Its route
// speaks the openai_chat wire schema; the workos: Bearer prefix and cline
// headers are the TRANSPORT's job (P7-EXEC-001), so the stored token stays raw.
type ClinePassAdapter struct {
	postProbe ClinePassPostProbe
	getProbe  ClinePassGetProbe
}

// NewClinePassAdapter builds the adapter over the two injected seams.
func NewClinePassAdapter(postProbe ClinePassPostProbe, getProbe ClinePassGetProbe) *ClinePassAdapter {
	return &ClinePassAdapter{postProbe: postProbe, getProbe: getProbe}
}

// OmitStateFromCallback reports true: clinepass's authorize redirect does NOT
// echo the `state` parameter back to the callback URL (verified against the
// legacy implementation 2026-08-03, which handled it with a per-flow
// "__recovered__" sentinel). The enrollment service therefore embeds the
// unguessable transaction id into the callback URL path so the callback can
// still be bound to exactly one transaction.
func (a *ClinePassAdapter) OmitStateFromCallback() bool { return true }

// BeginOAuth builds the extension-flow authorize URL with EXACTLY the four
// parameters the reference flow sends (03 §3 / legacy 2026-08-03):
// client_type=extension, callback_url, redirect_uri, state. The PKCE verifier
// is generated by the framework but is DELIBERATELY NOT sent on the wire: this
// is clinepass's documented extension flow, NOT an oversight — a future reader
// must not "fix" it by adding code_challenge, so a test pins its absence.
// pkceChallenge is intentionally ignored for that reason.
func (a *ClinePassAdapter) BeginOAuth(_ context.Context, redirectURI, state, _ string) (string, error) {
	q := url.Values{}
	q.Set("client_type", "extension")
	q.Set("callback_url", redirectURI)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	return ClinePassBaseURL + clinePassAuthorizePath + "?" + q.Encode(), nil
}

// CompleteOAuth exchanges the code (JSON body; NO code_verifier — the verifier
// is not sent), then resolves identity. The stable external id comes from the
// token response's own userInfo (clineUserId, falling back to subject), with
// GET /users/me as a last-resort fallback — never a fingerprint fabrication.
// Funding is reported paid (see TestClinePass_FundingPaidAndLocked for why the
// lock lives in the catalog, not here).
//
// Some Cline extension redirects place an already-minted token JSON blob in
// `code` (base64 + signature) instead of an authorization_code. When detected,
// we parse it and skip the token endpoint.
func (a *ClinePassAdapter) CompleteOAuth(ctx context.Context, code, _ /*pkceVerifier*/, redirectURI string) (IdentityResult, StoredCredentials, error) {
	authCode, _ := splitOAuthCode(code) // clinepass takes the pre-# code only; the fragment is not sent back

	var tok clinePassTokenData
	if embedded, ok := parseClinePassEmbeddedCode(authCode); ok {
		tok = embedded
	} else {
		reqBody, _ := json.Marshal(map[string]string{
			"grant_type":   "authorization_code",
			"client_type":  "extension",
			"provider":     "clinepass",
			"code":         authCode,
			"redirect_uri": redirectURI,
		})
		var err error
		tok, err = a.exchange(ctx, ClinePassBaseURL+clinePassTokenPath, reqBody)
		if err != nil {
			return IdentityResult{}, StoredCredentials{}, err
		}
	}

	stableID, email := clinePassStableIdentity(tok.UserInfo)
	if stableID == "" {
		// Fall back to /users/me — some flows omit userInfo from the token
		// response; the endpoint may still carry clineUserId or id.
		id, err := a.fetchIdentity(ctx, tok.AccessToken)
		if err != nil {
			return IdentityResult{}, StoredCredentials{}, err
		}
		stableID = id.ClineUserID
		if stableID == "" {
			stableID = id.ID
		}
		if email == "" {
			email = id.Email
		}
	}
	// Last resort matching legacy fingerprint: email from the token blob.
	if stableID == "" && tok.UserInfo != nil && tok.UserInfo.Email != "" {
		stableID = tok.UserInfo.Email
		if email == "" {
			email = tok.UserInfo.Email
		}
	}
	if stableID == "" {
		return IdentityResult{}, StoredCredentials{}, ErrMissingStableIdentity
	}

	stored, err := marshalClinePassToken(tok.AccessToken, tok.RefreshToken, parseClinePassExpiry(tok.ExpiresAt), tok.UserInfo)
	if err != nil {
		return IdentityResult{}, StoredCredentials{}, err
	}
	return IdentityResult{
		ExternalID: stableID,
		Email:      email,
		Plan:       "ClinePass",
		Funding:    string(FundingPaid),
		Confidence: clinePassConfidence,
		Evidence:   map[string]any{"funding_source": "clinepass_locked_paid"},
	}, stored, nil
}

// parseClinePassEmbeddedCode detects Cline's token-in-code callback shape:
// base64(JSON{accessToken,refreshToken,...}) optionally followed by a
// signature suffix. Returns ok=false when the value is a normal auth code.
func parseClinePassEmbeddedCode(code string) (clinePassTokenData, bool) {
	if code == "" || !strings.HasPrefix(code, "eyJ") {
		return clinePassTokenData{}, false
	}
	tryDecode := func(s string) (clinePassEmbeddedPayload, bool) {
		encodings := []*base64.Encoding{
			base64.StdEncoding,
			base64.RawStdEncoding,
			base64.URLEncoding,
			base64.RawURLEncoding,
		}
		for _, enc := range encodings {
			padded := s
			if enc == base64.StdEncoding || enc == base64.URLEncoding {
				if m := len(padded) % 4; m != 0 {
					padded += strings.Repeat("=", 4-m)
				}
			}
			raw, err := enc.DecodeString(padded)
			if err != nil {
				continue
			}
			var p clinePassEmbeddedPayload
			if err := json.Unmarshal(raw, &p); err != nil || p.AccessToken == "" {
				continue
			}
			return p, true
		}
		return clinePassEmbeddedPayload{}, false
	}

	if p, ok := tryDecode(code); ok {
		ui := p.UserInfo
		if ui == nil && p.Email != "" {
			ui = &clinePassUserInfo{Email: p.Email, Name: p.Name}
		}
		return clinePassTokenData{
			AccessToken: p.AccessToken, RefreshToken: p.RefreshToken,
			ExpiresAt: p.ExpiresAt, UserInfo: ui,
		}, true
	}
	// Signature may be appended after the JSON base64; try shrinking prefixes.
	for i := len(code); i > 32; i-- {
		if p, ok := tryDecode(code[:i]); ok {
			ui := p.UserInfo
			if ui == nil && p.Email != "" {
				ui = &clinePassUserInfo{Email: p.Email, Name: p.Name}
			}
			return clinePassTokenData{
				AccessToken: p.AccessToken, RefreshToken: p.RefreshToken,
				ExpiresAt: p.ExpiresAt, UserInfo: ui,
			}, true
		}
	}
	return clinePassTokenData{}, false
}

// RefreshCredentials posts {refreshToken, grantType:"refresh_token"} (camelCase,
// 03 §3). Same retention rules as claude-code: keep the old refresh token when
// none is returned; keep the prior userInfo when the response carries none; a
// failed refresh returns a typed error and leaves the stored credential
// untouched.
func (a *ClinePassAdapter) RefreshCredentials(ctx context.Context, creds StoredCredentials) (StoredCredentials, error) {
	var stored clinePassStoredToken
	if err := json.Unmarshal([]byte(creds.Value), &stored); err != nil {
		return StoredCredentials{}, fmt.Errorf("providers: clinepass: refresh: parse stored credentials: %w", err)
	}
	if stored.RefreshToken == "" {
		return StoredCredentials{}, errors.New("providers: clinepass: refresh: no refresh token available")
	}
	reqBody, _ := json.Marshal(map[string]string{"refreshToken": stored.RefreshToken, "grantType": "refresh_token"})
	tok, err := a.exchangeRefresh(ctx, ClinePassBaseURL+clinePassRefreshPath, reqBody)
	if err != nil {
		return StoredCredentials{}, err
	}
	newRefresh := tok.RefreshToken
	if newRefresh == "" {
		newRefresh = stored.RefreshToken
	}
	userInfo := tok.UserInfo
	if userInfo == nil {
		userInfo = stored.UserInfo
	}
	return marshalClinePassToken(tok.AccessToken, newRefresh, parseClinePassExpiry(tok.ExpiresAt), userInfo)
}

// exchangeRefresh is deliberately stricter than the authorization-code
// exchange about declaring a credential dead. Cline's edge can return 401/403
// for transient policy/gateway failures; only an explicit refresh-token marker
// is definitive enough to force re-login.
func (a *ClinePassAdapter) exchangeRefresh(ctx context.Context, url string, body []byte) (clinePassTokenData, error) {
	status, respBody, err := a.postProbe(ctx, url, body)
	if err != nil {
		return clinePassTokenData{}, fmt.Errorf("providers: clinepass: refresh endpoint: %w", err)
	}
	if status < 200 || status >= 300 {
		if clinePassRefreshIsUnrecoverable(respBody) {
			return clinePassTokenData{}, ErrInvalidCredential
		}
		return clinePassTokenData{}, ErrProviderUnavailable
	}
	var env clinePassEnvelope[clinePassTokenData]
	if err := json.Unmarshal(respBody, &env); err != nil {
		return clinePassTokenData{}, fmt.Errorf("providers: clinepass: parse refresh response: %w", err)
	}
	if !env.Success || env.Data == nil || env.Data.AccessToken == "" {
		// A malformed success is provider drift, not proof that the stored
		// refresh token was revoked. Preserve it and retry later.
		return clinePassTokenData{}, ErrProviderUnavailable
	}
	return *env.Data, nil
}

func clinePassRefreshIsUnrecoverable(body []byte) bool {
	lower := strings.ToLower(string(body))
	for _, marker := range []string{
		"invalid_grant",
		"invalid_token",
		"refresh_token_expired",
		"refresh_token_reused",
		"refresh_token_invalidated",
		"refresh token expired",
		"refresh token revoked",
		"refresh token invalid",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func (a *ClinePassAdapter) exchange(ctx context.Context, url string, body []byte) (clinePassTokenData, error) {
	status, respBody, err := a.postProbe(ctx, url, body)
	if err != nil {
		return clinePassTokenData{}, fmt.Errorf("providers: clinepass: token endpoint: %w", err)
	}
	if status == 401 || status == 403 {
		return clinePassTokenData{}, ErrInvalidCredential
	}
	if status < 200 || status >= 300 {
		return clinePassTokenData{}, ErrProviderUnavailable
	}
	var env clinePassEnvelope[clinePassTokenData]
	if err := json.Unmarshal(respBody, &env); err != nil {
		return clinePassTokenData{}, fmt.Errorf("providers: clinepass: parse token response: %w", err)
	}
	if !env.Success || env.Data == nil || env.Data.AccessToken == "" {
		return clinePassTokenData{}, ErrInvalidCredential
	}
	return *env.Data, nil
}

// clinePassStableIdentity resolves the stable external id from the token
// response's userInfo: clineUserId first, then subject (03 §3). Email is the
// userInfo email. Empty id = "resolve elsewhere or fail".
func clinePassStableIdentity(u *clinePassUserInfo) (stableID, email string) {
	if u == nil {
		return "", ""
	}
	if u.ClineUserID != "" {
		return u.ClineUserID, u.Email
	}
	return u.Subject, u.Email
}

// parseClinePassExpiry parses the token response's ISO-string expiresAt; a
// missing/unparseable value falls back to the legacy default (now + 1h), never
// a zero expiry.
func parseClinePassExpiry(value string) int64 {
	if value == "" {
		return time.Now().Add(clinePassDefaultExpiry * time.Second).Unix()
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Now().Add(clinePassDefaultExpiry * time.Second).Unix()
	}
	return t.Unix()
}

func marshalClinePassToken(access, refresh string, expiresAt int64, userInfo *clinePassUserInfo) (StoredCredentials, error) {
	value, err := json.Marshal(clinePassStoredToken{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresAt:    expiresAt,
		UserInfo:     userInfo,
	})
	if err != nil {
		return StoredCredentials{}, fmt.Errorf("providers: clinepass: marshal stored credentials: %w", err)
	}
	return StoredCredentials{Value: string(value)}, nil
}

// fetchIdentity reads GET /api/v1/users/me (the credential-authentic identity
// call, also used by health). The envelope is {success, data}. clineUserId, when
// present, is the stable id; email is read when present (the primary email
// source is the token userInfo, which some flows omit).
func (a *ClinePassAdapter) fetchIdentity(ctx context.Context, accessToken string) (clinePassIdentity, error) {
	status, body, err := a.getProbe(ctx, ClinePassBaseURL+clinePassIdentityPath, accessToken)
	if err != nil {
		return clinePassIdentity{}, fmt.Errorf("providers: clinepass: fetch identity: %w", err)
	}
	if status == 401 || status == 403 {
		return clinePassIdentity{}, ErrInvalidCredential
	}
	if status < 200 || status >= 300 {
		return clinePassIdentity{}, ErrProviderUnavailable
	}
	var env clinePassEnvelope[clinePassIdentity]
	if err := json.Unmarshal(body, &env); err != nil {
		return clinePassIdentity{}, fmt.Errorf("providers: clinepass: parse identity: %w", err)
	}
	if !env.Success || env.Data == nil {
		return clinePassIdentity{}, ErrMissingStableIdentity
	}
	return *env.Data, nil
}

// FetchIdentity implements IdentityAdapter: stored userInfo first (it was
// captured at token time and carries clineUserId/subject/email), then the
// /users/me fallback. A blank stable id is a typed error, never fabricated.
func (a *ClinePassAdapter) FetchIdentity(ctx context.Context, creds StoredCredentials) (IdentityResult, error) {
	token, userInfo, err := clinePassTokenAndUserInfo(creds)
	if err != nil {
		return IdentityResult{}, err
	}
	if stableID, email := clinePassStableIdentity(userInfo); stableID != "" {
		return IdentityResult{ExternalID: stableID, Email: email, Plan: "ClinePass", Funding: string(FundingPaid), Confidence: clinePassConfidence}, nil
	}
	id, err := a.fetchIdentity(ctx, token)
	if err != nil {
		return IdentityResult{}, err
	}
	if id.ClineUserID == "" {
		return IdentityResult{}, ErrMissingStableIdentity
	}
	return IdentityResult{ExternalID: id.ClineUserID, Email: id.Email, Plan: "ClinePass", Funding: string(FundingPaid), Confidence: clinePassConfidence}, nil
}

// clinePassModelEntry is one model in the recommended-models groups.
type clinePassModelEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// clinePassRecommendedModels is the REAL recommended-models shape: three
// arrays, NOT a `models` array (legacy 2026-08-03). The endpoint is PUBLIC.
type clinePassRecommendedModels struct {
	Recommended []clinePassModelEntry `json:"recommended"`
	Free        []clinePassModelEntry `json:"free"`
	ClinePass   []clinePassModelEntry `json:"clinePass"`
}

// DiscoverModels imports only the paid clinePass group. The endpoint also
// publishes recommended/free groups for Cline's API-key product, but those do
// not belong to this OAuth integration. Subscription is authenticated before
// the public catalog is accepted so an unsubscribed login cannot populate
// offerings that this account can never use.
func (a *ClinePassAdapter) DiscoverModels(ctx context.Context, creds StoredCredentials) ([]DiscoveredModel, error) {
	token, _, err := clinePassTokenAndUserInfo(creds)
	if err != nil {
		return nil, err
	}
	if _, err := a.fetchSubscriptionLimits(ctx, token); err != nil {
		return nil, err
	}
	// Empty token: the recommended-models endpoint is public and must not
	// receive a workos:-prefixed Authorization header.
	status, body, err := a.getProbe(ctx, ClinePassBaseURL+clinePassModelsPath, "")
	if err != nil {
		return nil, fmt.Errorf("providers: clinepass: discover models: %w", err)
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("providers: clinepass: discover models: status %d", status)
	}
	var list clinePassRecommendedModels
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("providers: clinepass: parse models: %w", err)
	}

	seen := make(map[string]struct{}, len(list.ClinePass))
	out := make([]DiscoveredModel, 0, len(list.ClinePass))
	for _, m := range list.ClinePass {
		if m.ID == "" {
			continue
		}
		if _, duplicate := seen[m.ID]; duplicate {
			continue
		}
		seen[m.ID] = struct{}{}
		display := m.Name
		if display == "" {
			display = m.ID
		}
		out = append(out, DiscoveredModel{ProviderModelID: m.ID, DisplayName: display, Capabilities: []string{"chat"}})
	}
	return out, nil
}

// clinePassBalance is the data of GET /users/{id}/balance: balance is
// micro-USD (1/1,000,000 of a dollar — legacy confirmed against Cline's own
// dashboard, 2026-08-03).
type clinePassBalance struct {
	Balance *float64 `json:"balance"`
}

// clinePassUsageLimit is one entry of GET /users/me/plan/usage-limits
// (legacy ClinePassUsageLimit): five_hour / weekly / monthly rolling windows
// with percentUsed + resetsAt.
type clinePassUsageLimit struct {
	Type        string   `json:"type"`
	PercentUsed *float64 `json:"percentUsed"`
	ResetsAt    string   `json:"resetsAt"`
}

type clinePassUsageLimits struct {
	Limits []clinePassUsageLimit `json:"limits"`
}

// FetchQuota grounds a balance window on GET /users/{id}/balance (micro-USD)
// and rolling windows on GET /users/me/plan/usage-limits, exactly the paths the
// reference uses (03 §3 / legacy 2026-08-03). A missing user id or a payload
// with no grounded number yields NO windows — an empty slice is "no evidence",
// never "unlimited".
func (a *ClinePassAdapter) FetchQuota(ctx context.Context, creds StoredCredentials) (QuotaResult, error) {
	token, _, err := clinePassTokenAndUserInfo(creds)
	if err != nil {
		return QuotaResult{}, err
	}

	// 1. /users/me -> the numeric user id the balance path is keyed on.
	id, err := a.fetchUserID(ctx, token)
	if err != nil {
		return QuotaResult{}, err
	}
	if id == "" {
		return QuotaResult{}, nil // no grounded user id -> no windows
	}

	limits, err := a.fetchSubscriptionLimits(ctx, token)
	if err != nil {
		return QuotaResult{}, err
	}

	var windows []QuotaWindow

	// 2. Balance (micro-USD -> USD).
	status, body, err := a.getProbe(ctx, fmt.Sprintf(ClinePassBaseURL+clinePassBalancePath, id), token)
	if err != nil {
		return QuotaResult{}, fmt.Errorf("providers: clinepass: fetch quota: %w", err)
	}
	if status < 200 || status >= 300 {
		return QuotaResult{}, fmt.Errorf("providers: clinepass: fetch quota: status %d", status)
	}
	var balEnv clinePassEnvelope[clinePassBalance]
	if err := json.Unmarshal(body, &balEnv); err != nil {
		return QuotaResult{}, fmt.Errorf("providers: clinepass: parse balance: %w", err)
	}
	if balEnv.Success && balEnv.Data != nil && balEnv.Data.Balance != nil {
		usd := *balEnv.Data.Balance / clinePassMicroPerUSD
		windows = append(windows, QuotaWindow{
			Unit:       "credits",
			WindowType: "balance",
			Remaining:  &usd,
			Confidence: clinePassConfidence,
		})
	}

	// 3. Usage limits (five_hour / weekly / monthly percent windows).
	for _, l := range limits {
		windowType, duration, ok := clinePassUsageWindowKind(l.Type)
		if !ok {
			continue
		}
		qw := QuotaWindow{
			Unit:            "percent",
			WindowType:      windowType,
			DurationSeconds: intPtr(duration),
			Confidence:      clinePassConfidence,
		}
		if l.PercentUsed != nil {
			used := *l.PercentUsed
			qw.Used = &used
		}
		if resetAt := parseRFC3339Seconds(l.ResetsAt); resetAt != nil {
			qw.ResetAt = resetAt
		}
		windows = append(windows, qw)
	}

	return QuotaResult{Windows: windows}, nil
}

func (a *ClinePassAdapter) fetchSubscriptionLimits(ctx context.Context, token string) ([]clinePassUsageLimit, error) {
	status, body, err := a.getProbe(ctx, ClinePassBaseURL+clinePassUsageLimitsPath, token)
	if err != nil {
		return nil, fmt.Errorf("providers: clinepass: fetch subscription: %w", err)
	}
	if status == 401 || status == 403 {
		return nil, ErrInvalidCredential
	}
	subscribed, definitive := clinePassSubscriptionFromLimits(status, body)
	if definitive && !subscribed {
		return nil, ErrSubscriptionRequired
	}
	if !definitive {
		return nil, ErrProviderUnavailable
	}
	var env clinePassEnvelope[clinePassUsageLimits]
	if err := json.Unmarshal(body, &env); err != nil || env.Data == nil {
		return nil, ErrProviderUnavailable
	}
	return env.Data.Limits, nil
}

// fetchUserID reads GET /users/me for the numeric id the balance path needs.
// A non-2xx or an envelope without data.id returns "" (fail closed, never a
// fabricated path).
func (a *ClinePassAdapter) fetchUserID(ctx context.Context, accessToken string) (string, error) {
	status, body, err := a.getProbe(ctx, ClinePassBaseURL+clinePassIdentityPath, accessToken)
	if err != nil {
		return "", fmt.Errorf("providers: clinepass: fetch user id: %w", err)
	}
	if status == 401 || status == 403 {
		return "", ErrInvalidCredential
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("providers: clinepass: fetch user id: status %d", status)
	}
	var env clinePassEnvelope[struct {
		ID string `json:"id"`
	}]
	if err := json.Unmarshal(body, &env); err != nil {
		return "", fmt.Errorf("providers: clinepass: parse user: %w", err)
	}
	if !env.Success || env.Data == nil {
		return "", nil
	}
	return env.Data.ID, nil
}

// clinePassUsageWindowKind maps a usage-limit type onto a window vocabulary
// entry. if/else, not a slug switch.
func clinePassUsageWindowKind(typ string) (windowType string, duration int, ok bool) {
	if typ == "five_hour" {
		return "rolling_5h", 18000, true
	}
	if typ == "weekly" {
		return "rolling_7d", 604800, true
	}
	if typ == "monthly" {
		return "rolling_30d", 2592000, true
	}
	return "", 0, false
}

// parseRFC3339Seconds parses an RFC3339 timestamp into unix seconds, returning
// nil for absent/unparseable (never a fabricated reset).
func parseRFC3339Seconds(value string) *int64 {
	if value == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil
	}
	unix := t.Unix()
	return &unix
}

// CheckAccountHealth reuses the identity GET as the credential-authentic call.
func (a *ClinePassAdapter) CheckAccountHealth(ctx context.Context, creds StoredCredentials) (HealthObservation, error) {
	return a.checkHealth(ctx, creds, "account")
}

// CheckOfferingHealth reuses the account-level probe (no per-model endpoint).
func (a *ClinePassAdapter) CheckOfferingHealth(ctx context.Context, creds StoredCredentials, _ string) (HealthObservation, error) {
	return a.checkHealth(ctx, creds, "offering")
}

// clinePassNoSubscriptionMessage is the owner-facing reason a token-valid but
// UNSUBSCRIBED account is degraded: this provider integration exists for
// ClinePass (subscription) accounts, so a signed-in account without an active
// Pass must be visibly broken — fixed (subscribe / sign in with a subscribed
// account) or removed — never silently listed as healthy.
const clinePassNoSubscriptionMessage = "no active ClinePass subscription on this account — sign in with a subscribed account or remove it"

func (a *ClinePassAdapter) checkHealth(ctx context.Context, creds StoredCredentials, scope string) (HealthObservation, error) {
	token, _, err := clinePassTokenAndUserInfo(creds)
	if err != nil {
		return observationFromValidation(ValidationUnavailable, scope, time.Now().Unix()), nil
	}
	status, _, err := a.getProbe(ctx, ClinePassBaseURL+clinePassIdentityPath, token)
	if err != nil {
		return observationFromValidation(ValidationUnavailable, scope, time.Now().Unix()), nil
	}
	obs := observationFromValidation(classifyOAuthStatus(status), scope, time.Now().Unix())
	if obs.Status != "healthy" {
		return obs, nil
	}

	// The credential authenticated — now check the SUBSCRIPTION. The Pass
	// plan's rolling usage-limits exist only for subscribed accounts (live
	// evidence 2026-08-04: the subscribed account returns limits, the
	// unsubscribed one does not), so a definitive limits answer with no plan
	// windows means "signed in, but no ClinePass" — degraded with an
	// actionable reason. Only a DEFINITIVE provider answer may claim that:
	// a transport error or 5xx leaves the healthy verdict from the identity
	// call untouched (never degrade on missing evidence).
	subStatus, subBody, subErr := a.getProbe(ctx, ClinePassBaseURL+clinePassUsageLimitsPath, token)
	if subErr != nil {
		return obs, nil
	}
	subscribed, definitive := clinePassSubscriptionFromLimits(subStatus, subBody)
	if definitive && !subscribed {
		obs.Status = "degraded"
		obs.Failure = &HealthFailure{
			Class:       "subscription",
			Retryable:   false,
			SafeMessage: clinePassNoSubscriptionMessage,
		}
	}
	return obs, nil
}

// clinePassSubscriptionFromLimits reads GET /users/me/plan/usage-limits as the
// subscription signal. definitive is true only when the provider ANSWERED the
// question: a 2xx envelope (subscribed = it carries at least one limit window)
// or a 404 (no plan resource = not subscribed). Anything else — 5xx, auth
// statuses, an unparseable body — is not evidence and must change nothing.
func clinePassSubscriptionFromLimits(status int, body []byte) (subscribed, definitive bool) {
	if status == 404 {
		return false, true
	}
	if status < 200 || status >= 300 {
		return false, false
	}
	var env clinePassEnvelope[clinePassUsageLimits]
	if err := json.Unmarshal(body, &env); err != nil {
		return false, false
	}
	if !env.Success {
		return false, false
	}
	if env.Data == nil || len(env.Data.Limits) == 0 {
		return false, true
	}
	return true, true
}

// clinePassTokenAndUserInfo extracts the access token (required) and the
// stored userInfo (may be nil) from the credential envelope.
func clinePassTokenAndUserInfo(creds StoredCredentials) (string, *clinePassUserInfo, error) {
	var stored clinePassStoredToken
	if err := json.Unmarshal([]byte(creds.Value), &stored); err != nil {
		return "", nil, fmt.Errorf("providers: clinepass: parse stored credentials: %w", err)
	}
	if stored.AccessToken == "" {
		return "", nil, ErrInvalidCredential
	}
	return stored.AccessToken, stored.UserInfo, nil
}

// RegisterClinePass registers the clinepass OAuth + Identity + Discovery +
// Quota + Health adapters into reg with the native_oauth transport and the
// openai_chat wire schema.
func RegisterClinePass(reg *Registry, postProbe ClinePassPostProbe, getProbe ClinePassGetProbe) error {
	adapter := NewClinePassAdapter(postProbe, getProbe)
	return reg.Register(Definition{
		ID:         ClinePassID,
		AuthMode:   AuthModeOAuth,
		Transport:  TransportKindNativeOAuth,
		WireSchema: WireSchemaOpenAIChat,
		OAuth:      adapter,
		Identity:   adapter,
		Discovery:  adapter,
		Quota:      adapter,
		Health:     adapter,
	})
}

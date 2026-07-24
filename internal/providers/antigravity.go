package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// AntigravityID is the catalog slug this adapter registers under — must
// match BuiltinCatalog's "antigravity" entry (P2b-PROV-007).
const AntigravityID ProviderID = "antigravity"

// The three Google endpoints antigravity's OAuth2 confidential-client
// flow uses (03 §3/§4). internal/providers holds no net/http import
// (01 §3/§8 layering) — these are pure string constants; the actual
// network calls happen through the injected *Probe seams below, whose
// concrete implementation is supplied by the composition root
// (internal/httpapi).
const (
	antigravityAuthorizeURL      = "https://accounts.google.com/o/oauth2/v2/auth"
	antigravityTokenURL          = "https://oauth2.googleapis.com/token"
	antigravityUserInfoURL       = "https://www.googleapis.com/oauth2/v2/userinfo"
	antigravityLoadCodeAssistURL = "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"
)

// antigravityScopes are the documented OAuth scopes antigravity's
// confidential client requests (03 §4): cloud-platform access, the
// owner's email/profile for identity, and the two Gemini/antigravity
// tooling scopes (cclog, experimentsandconfigs).
var antigravityScopes = []string{
	"https://www.googleapis.com/auth/cloud-platform",
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/userinfo.profile",
	"https://www.googleapis.com/auth/cclog",
	"https://www.googleapis.com/auth/experimentsandconfigs",
}

// antigravityConfidenceRecognizedPlan is the confidence stamped for a
// recognized plan (Free or Pro) — a real, provider-reported
// classification, distinct from the 1.0 an owner_override/owner_policy
// stamp uses elsewhere for a fixed/administrative decision (03 §4's
// funding-confidence gap this unit closes).
const antigravityConfidenceRecognizedPlan = 0.95

// antigravityConfidenceUnrecognizedPlan is the confidence stamped when
// currentTier names a plan this adapter does not recognize as Free or
// Pro: zero, since an unrecognized tier name is not real classifying
// evidence — it must never outrank (via
// domain.DecideFundingSupersession's confidence comparison) a
// subsequent, correctly recognized classification.
const antigravityConfidenceUnrecognizedPlan = 0.0

// AntigravityTokenProbe performs a POST to the OAuth2 token endpoint
// with form as the application/x-www-form-urlencoded body (used for
// both the initial authorization_code exchange and a refresh_token
// grant) and returns the raw JSON response body. internal/providers
// holds no net/http import; the concrete HTTP implementation is
// supplied by the composition root (internal/httpapi) and injected here
// as this function type. form (which carries the confidential client's
// secret) must never be logged by any implementation.
type AntigravityTokenProbe func(ctx context.Context, tokenURL string, form url.Values) ([]byte, error)

// AntigravityGetProbe performs an authenticated GET against url with a
// Bearer accessToken and returns the raw JSON response body — used for
// the userinfo identity fetch. accessToken must never be logged.
type AntigravityGetProbe func(ctx context.Context, url, accessToken string) ([]byte, error)

// AntigravityPostProbe performs an authenticated POST with body as the
// JSON request body against url with a Bearer accessToken, and returns
// the raw JSON response body — used for the loadCodeAssist plan/project
// fetch. accessToken must never be logged.
type AntigravityPostProbe func(ctx context.Context, url, accessToken string, body []byte) ([]byte, error)

// antigravityTokenResponse is the subset of Google's OAuth2 token
// endpoint response this adapter reads.
type antigravityTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// antigravityUserInfoResponse is the subset of the userinfo endpoint's
// response this adapter reads.
type antigravityUserInfoResponse struct {
	Email string `json:"email"`
}

// antigravityLoadCodeAssistResponse is the subset of loadCodeAssist's
// response this adapter reads: the project id and the owner's current
// tier (name is the human plan label this adapter classifies funding
// from; id is evidence-only, never a decision input).
type antigravityLoadCodeAssistResponse struct {
	CloudaicompanionProject string `json:"cloudaicompanionProject"`
	CurrentTier             struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"currentTier"`
}

// antigravityStoredToken is the shape this adapter serializes into
// StoredCredentials.Value — an opaque string as far as StoredCredentials
// itself is concerned (01 §8: only internal/secrets ever encrypts it),
// carrying exactly what RefreshCredentials needs to re-mint an access
// token later.
type antigravityStoredToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"` // unix seconds
}

// AntigravityAdapter implements OAuthAdapter for antigravity (P2b-PROV-007):
// a confidential OAuth2 client (Google) whose client id/secret are
// constructor inputs — injected from the environment at the composition
// root (internal/httpapi), never read here. clientSecret is used only
// in the token exchange/refresh request body; it is never logged and
// never returned to any caller.
type AntigravityAdapter struct {
	clientID     string
	clientSecret string

	tokenProbe          AntigravityTokenProbe
	userInfoProbe       AntigravityGetProbe
	loadCodeAssistProbe AntigravityPostProbe
}

// NewAntigravityAdapter builds the adapter over its confidential client
// credentials and the three injected HTTP seams.
func NewAntigravityAdapter(clientID, clientSecret string, tokenProbe AntigravityTokenProbe, userInfoProbe AntigravityGetProbe, loadCodeAssistProbe AntigravityPostProbe) *AntigravityAdapter {
	return &AntigravityAdapter{
		clientID: clientID, clientSecret: clientSecret,
		tokenProbe: tokenProbe, userInfoProbe: userInfoProbe, loadCodeAssistProbe: loadCodeAssistProbe,
	}
}

// BeginOAuth builds the Google authorization URL: pure string
// construction, no HTTP call. redirectURI/state/pkceChallenge are
// echoed exactly as supplied by the OAuth enrollment framework
// (P2b-PROV-006); code_challenge_method is always S256, matching the
// verifier Complete later decrypts and hands to CompleteOAuth.
func (a *AntigravityAdapter) BeginOAuth(_ context.Context, redirectURI, state, pkceChallenge string) (string, error) {
	q := url.Values{}
	q.Set("client_id", a.clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("code_challenge", pkceChallenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	q.Set("scope", strings.Join(antigravityScopes, " "))
	// access_type=offline + prompt=consent: a confidential client needs
	// a refresh_token back (RefreshCredentials depends on one being
	// issued), which Google only reliably returns on a consent-prompted
	// offline-access authorization.
	q.Set("access_type", "offline")
	q.Set("prompt", "consent")
	return antigravityAuthorizeURL + "?" + q.Encode(), nil
}

// CompleteOAuth exchanges code for tokens (via the injected token
// probe), then fetches the owner's email (userinfo) and project/plan
// (loadCodeAssist) to build the reported identity. code and
// pkceVerifier are used only as exchange inputs — code is never
// persisted anywhere (P2b-PROV-006's framework already guarantees this
// at the caller level; this adapter never stores it either).
func (a *AntigravityAdapter) CompleteOAuth(ctx context.Context, code, pkceVerifier, redirectURI string) (IdentityResult, StoredCredentials, error) {
	form := url.Values{}
	form.Set("client_id", a.clientID)
	form.Set("client_secret", a.clientSecret)
	form.Set("code", code)
	form.Set("code_verifier", pkceVerifier)
	form.Set("redirect_uri", redirectURI)
	form.Set("grant_type", "authorization_code")

	tok, err := a.exchangeToken(ctx, form)
	if err != nil {
		return IdentityResult{}, StoredCredentials{}, err
	}

	userinfo, err := a.fetchUserInfo(ctx, tok.AccessToken)
	if err != nil {
		return IdentityResult{}, StoredCredentials{}, err
	}

	codeAssist, err := a.fetchLoadCodeAssist(ctx, tok.AccessToken)
	if err != nil {
		return IdentityResult{}, StoredCredentials{}, err
	}

	funding, confidence := antigravityFundingForPlan(codeAssist.CurrentTier.Name)

	identity := IdentityResult{
		ExternalID: userinfo.Email + ":" + codeAssist.CloudaicompanionProject,
		Email:      userinfo.Email,
		Plan:       codeAssist.CurrentTier.Name,
		Funding:    funding,
		Confidence: confidence,
		Evidence:   map[string]any{"tier_id": codeAssist.CurrentTier.ID},
	}

	stored := antigravityStoredToken{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).Unix(),
	}
	value, err := json.Marshal(stored)
	if err != nil {
		return IdentityResult{}, StoredCredentials{}, fmt.Errorf("providers: antigravity: marshal stored credentials: %w", err)
	}

	return identity, StoredCredentials{Value: string(value)}, nil
}

// RefreshCredentials re-mints an access token at the token endpoint
// using the client secret and the stored refresh_token, via the same
// injected token probe CompleteOAuth uses.
func (a *AntigravityAdapter) RefreshCredentials(ctx context.Context, creds StoredCredentials) (StoredCredentials, error) {
	var stored antigravityStoredToken
	if err := json.Unmarshal([]byte(creds.Value), &stored); err != nil {
		return StoredCredentials{}, fmt.Errorf("providers: antigravity: refresh: parse stored credentials: %w", err)
	}
	if stored.RefreshToken == "" {
		return StoredCredentials{}, errors.New("providers: antigravity: refresh: no refresh token available")
	}

	form := url.Values{}
	form.Set("client_id", a.clientID)
	form.Set("client_secret", a.clientSecret)
	form.Set("refresh_token", stored.RefreshToken)
	form.Set("grant_type", "refresh_token")

	tok, err := a.exchangeToken(ctx, form)
	if err != nil {
		return StoredCredentials{}, err
	}

	// Google's refresh response commonly omits refresh_token (the
	// original stays valid) — keep the prior one in that case rather
	// than dropping it.
	newRefresh := tok.RefreshToken
	if newRefresh == "" {
		newRefresh = stored.RefreshToken
	}

	out := antigravityStoredToken{
		AccessToken:  tok.AccessToken,
		RefreshToken: newRefresh,
		ExpiresAt:    time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).Unix(),
	}
	value, err := json.Marshal(out)
	if err != nil {
		return StoredCredentials{}, fmt.Errorf("providers: antigravity: refresh: marshal credentials: %w", err)
	}
	return StoredCredentials{Value: string(value)}, nil
}

// exchangeToken is the shared token-endpoint call CompleteOAuth and
// RefreshCredentials both use (grant_type is the only difference between
// the two callers' form).
func (a *AntigravityAdapter) exchangeToken(ctx context.Context, form url.Values) (antigravityTokenResponse, error) {
	body, err := a.tokenProbe(ctx, antigravityTokenURL, form)
	if err != nil {
		return antigravityTokenResponse{}, fmt.Errorf("providers: antigravity: token endpoint: %w", err)
	}
	var tok antigravityTokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return antigravityTokenResponse{}, fmt.Errorf("providers: antigravity: parse token response: %w", err)
	}
	if tok.AccessToken == "" {
		return antigravityTokenResponse{}, ErrInvalidCredential
	}
	return tok, nil
}

func (a *AntigravityAdapter) fetchUserInfo(ctx context.Context, accessToken string) (antigravityUserInfoResponse, error) {
	body, err := a.userInfoProbe(ctx, antigravityUserInfoURL, accessToken)
	if err != nil {
		return antigravityUserInfoResponse{}, fmt.Errorf("providers: antigravity: fetch userinfo: %w", err)
	}
	var userinfo antigravityUserInfoResponse
	if err := json.Unmarshal(body, &userinfo); err != nil {
		return antigravityUserInfoResponse{}, fmt.Errorf("providers: antigravity: parse userinfo response: %w", err)
	}
	return userinfo, nil
}

func (a *AntigravityAdapter) fetchLoadCodeAssist(ctx context.Context, accessToken string) (antigravityLoadCodeAssistResponse, error) {
	body, err := a.loadCodeAssistProbe(ctx, antigravityLoadCodeAssistURL, accessToken, []byte(`{}`))
	if err != nil {
		return antigravityLoadCodeAssistResponse{}, fmt.Errorf("providers: antigravity: load code assist: %w", err)
	}
	var ca antigravityLoadCodeAssistResponse
	if err := json.Unmarshal(body, &ca); err != nil {
		return antigravityLoadCodeAssistResponse{}, fmt.Errorf("providers: antigravity: parse load code assist response: %w", err)
	}
	return ca, nil
}

// antigravityFundingForPlan maps loadCodeAssist's currentTier.name to a
// funding classification and this adapter's confidence in it (03 §4's
// exact vocabulary): "Free" -> free, "Pro" -> paid, anything else ->
// unknown. Only Free/Pro are real classifying evidence
// (antigravityConfidenceRecognizedPlan); an unrecognized name is
// deliberately zero-confidence "unknown" rather than a confident
// misclassification. tier_id is never consulted here — it is evidence
// only (see CompleteOAuth's Evidence map), never a decision input.
func antigravityFundingForPlan(planName string) (funding string, confidence float64) {
	// Deliberately if/else, not a switch on string-literal cases: this
	// package's own noslugswitch check (01 §4.5 / 08 §8) forbids that
	// syntactic shape unconditionally, even for a funding classification
	// (not a provider dispatch) like this one.
	if planName == "Free" {
		return "free", antigravityConfidenceRecognizedPlan
	}
	if planName == "Pro" {
		return "paid", antigravityConfidenceRecognizedPlan
	}
	return "unknown", antigravityConfidenceUnrecognizedPlan
}

// RegisterAntigravity registers the antigravity OAuth adapter into reg.
// It does NOT decide whether antigravity is configured — that is the
// composition root's job (internal/httpapi, reading
// platform.AntigravityOAuthClientCredentials): this function is only
// ever called once both clientID and clientSecret are known-present,
// non-empty values.
func RegisterAntigravity(reg *Registry, clientID, clientSecret string, tokenProbe AntigravityTokenProbe, userInfoProbe AntigravityGetProbe, loadCodeAssistProbe AntigravityPostProbe) error {
	adapter := NewAntigravityAdapter(clientID, clientSecret, tokenProbe, userInfoProbe, loadCodeAssistProbe)
	return reg.Register(Definition{
		ID:       AntigravityID,
		AuthMode: AuthModeOAuth,
		OAuth:    adapter,
	})
}

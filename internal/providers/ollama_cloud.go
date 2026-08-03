package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// OllamaCloudID is the catalog slug this adapter registers under (03 §3 /
// PROV-002 BuiltinCatalog).
const OllamaCloudID ProviderID = "ollama-cloud"

// OllamaCloudBaseURL is ollama-cloud's OpenAI-compatible API base (03 §3). It
// MUST equal the BuiltinCatalog entry — asserted by a test.
const OllamaCloudBaseURL = "https://ollama.com/v1"

// OllamaCloudIdentityBaseURL is the NATIVE (non-OpenAI) base the identity /
// health probe uses: POST {base}/me returns the account record (03 §3).
const OllamaCloudIdentityBaseURL = "https://ollama.com/api"

// modelsDevOllamaKey is ollama-cloud's provider key inside the models.dev
// dataset. Verified 2026-08-03: its `api` equals OllamaCloudBaseURL and its
// model ids match /v1/models 1:1.
const modelsDevOllamaKey = "ollama-cloud"

// ErrIdentityMissingStableID is returned when the identity endpoint answered
// successfully but carried no stable external ID. The catalog declares
// ollama-cloud's StableID as "account.ID"; substituting a key fingerprint
// would silently merge two different accounts under one identity, so a missing
// ID fails LOUDLY here rather than falling back.
var ErrIdentityMissingStableID = errors.New("providers: identity response carried no stable external ID")

// OllamaIdentityProbe performs the native identity call: POST
// {OllamaCloudIdentityBaseURL}/me with the credential. It returns the raw
// status + body so the adapter can classify (401/403 invalid; 429/5xx/
// transport unavailable) and parse the account record. The concrete
// net/http implementation is injected by the caller (httpapi) so this package
// holds no net/http import (01 §3/§8). It must never log key.
type OllamaIdentityProbe func(ctx context.Context, key string) (statusCode int, body []byte, err error)

// ollamaMeResponse is the subset of POST /api/me this adapter reads. The live
// record also carries CustomerID / WorkOSUserID as WRAPPED objects
// ({String,Valid}); they are deliberately not modelled here (never as plain
// strings) because the stable identity is account.ID and the funding evidence
// is Plan — nothing this adapter reports needs the wrapped fields.
type ollamaMeResponse struct {
	ID    string `json:"ID"`
	Email string `json:"Email"`
	Name  string `json:"Name"`
	Plan  string `json:"Plan"`
}

// OllamaCloudAdapter implements APIKeyAdapter, IdentityAdapter,
// ModelDiscoveryAdapter and HealthAdapter for ollama-cloud (03 §3). Unlike the
// OpenAI-compatible providers, its authentic validation is the /api/me
// identity call (which returns account data only for a recognized credential),
// NOT the /v1/models listing — that listing is the exact false-green the zen
// adapter documents. Discovery reads the live id list from /v1/models and
// enriches it from the models.dev `ollama-cloud` key. It registers NO quota
// adapter: Ollama exposes no usage API (ollama/ollama#12532; dashboard only),
// and the documented GPU-time windows are documentation, not provider
// evidence, so a QuotaWindow would imply a signal that does not exist.
type OllamaCloudAdapter struct {
	identityProbe OllamaIdentityProbe
	modelsProbe   ModelsProbe
	facts         *ModelsDevSource
	now           func() time.Time
}

// NewOllamaCloudAdapter builds the adapter over the injected identity/models
// probes and the shared models.dev facts source (built from modelsDevProbe +
// now). now defaults to time.Now when nil (every real caller); it is injectable
// so both the models.dev cache TTL and the health observation's CheckedAt stamp
// are testable without a real clock.
func NewOllamaCloudAdapter(identityProbe OllamaIdentityProbe, modelsProbe ModelsProbe, modelsDevProbe ModelsDevProbe, now func() time.Time) *OllamaCloudAdapter {
	if now == nil {
		now = time.Now
	}
	return &OllamaCloudAdapter{
		identityProbe: identityProbe,
		modelsProbe:   modelsProbe,
		facts:         NewModelsDevSource(modelsDevProbe, now),
		now:           now,
	}
}

// ConnectAPIKey validates key by the authentic identity call and, on success,
// reports the account identity. key is never logged. A 2xx whose ID is empty
// is ErrIdentityMissingStableID — never a fingerprint fallback.
func (a *OllamaCloudAdapter) ConnectAPIKey(ctx context.Context, key string) (IdentityResult, StoredCredentials, error) {
	normalized := NormalizeAPIKey(key)
	identity, err := a.fetchIdentity(ctx, normalized)
	if err != nil {
		return IdentityResult{}, StoredCredentials{}, err
	}
	return identity, StoredCredentials{Value: normalized}, nil
}

// FetchIdentity implements IdentityAdapter over the same authentic identity
// call.
func (a *OllamaCloudAdapter) FetchIdentity(ctx context.Context, creds StoredCredentials) (IdentityResult, error) {
	return a.fetchIdentity(ctx, creds.Value)
}

// fetchIdentity runs the /api/me probe and classifies the outcome. 401/403 ->
// ErrInvalidCredential; 429/5xx/transport -> ErrProviderUnavailable; 2xx with
// no ID -> ErrIdentityMissingStableID; 2xx with an ID -> IdentityResult.
func (a *OllamaCloudAdapter) fetchIdentity(ctx context.Context, key string) (IdentityResult, error) {
	status, body, err := a.identityProbe(ctx, key)
	if err != nil {
		return IdentityResult{}, ErrProviderUnavailable
	}
	switch {
	case status == 401 || status == 403:
		return IdentityResult{}, ErrInvalidCredential
	case status == 429 || (status >= 500 && status <= 599):
		return IdentityResult{}, ErrProviderUnavailable
	case status >= 200 && status < 300:
		var me ollamaMeResponse
		if err := json.Unmarshal(body, &me); err != nil {
			return IdentityResult{}, ErrProviderUnavailable
		}
		if me.ID == "" {
			return IdentityResult{}, ErrIdentityMissingStableID
		}
		return a.identityFrom(me), nil
	default:
		return IdentityResult{}, ErrProviderUnavailable
	}
}

// identityFrom builds the IdentityResult from a parsed /api/me record. Funding
// is reported ONLY from what the record actually says (03 §3: provider
// evidence); a plan that does not positively indicate a free tier leaves
// Funding "" — never a default of "free".
func (a *OllamaCloudAdapter) identityFrom(me ollamaMeResponse) IdentityResult {
	funding, confidence := ollamaFundingFromPlan(me.Plan)
	res := IdentityResult{
		ExternalID: me.ID,
		Email:      me.Email,
		Plan:       me.Plan,
		Funding:    funding,
		Confidence: confidence,
	}
	if me.Plan != "" {
		res.Evidence = map[string]any{"plan": me.Plan}
	}
	return res
}

// ollamaFundingFromPlan maps the /api/me Plan string onto funding evidence.
// Only a plan that positively states a free tier yields "free"; an empty plan
// (no evidence) or any other value leaves funding "" with zero confidence —
// the domain then treats it as no evidence, never as a fabricated free label.
func ollamaFundingFromPlan(plan string) (string, float64) {
	if strings.EqualFold(strings.TrimSpace(plan), "free") {
		return string(FundingFree), 0.95
	}
	return "", 0
}

// DiscoverModels reads the live id list from GET /v1/models and enriches it
// with the models.dev `ollama-cloud` facts. If the dataset cannot be fetched
// or parsed, discovery still returns the live ids as chat-only (a facts source
// being down must never make a working account look empty) — see
// modelsFromLiveIDs.
func (a *OllamaCloudAdapter) DiscoverModels(ctx context.Context, creds StoredCredentials) ([]DiscoveredModel, error) {
	body, err := a.modelsProbe(ctx, OllamaCloudBaseURL, creds.Value)
	if err != nil {
		return nil, fmt.Errorf("providers: ollama-cloud discover models: %w", err)
	}
	var list openAICompatModelList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("providers: ollama-cloud discover models: parse response: %w", err)
	}

	facts, ferr := a.facts.Facts(ctx, modelsDevOllamaKey)
	if ferr != nil {
		// Dataset unavailable/unparseable: still list the live ids chat-only.
		facts = nil
	}
	return modelsFromLiveIDs(modelIDsFrom(list), facts), nil
}

// CheckAccountHealth implements HealthAdapter via the same authentic /api/me
// probe (credential-authentic), never /v1/models.
func (a *OllamaCloudAdapter) CheckAccountHealth(ctx context.Context, creds StoredCredentials) (HealthObservation, error) {
	return a.checkHealth(ctx, creds, "account")
}

// CheckOfferingHealth reuses the account-level probe: ollama-cloud has no
// per-model health endpoint.
func (a *OllamaCloudAdapter) CheckOfferingHealth(ctx context.Context, creds StoredCredentials, _ string) (HealthObservation, error) {
	return a.checkHealth(ctx, creds, "offering")
}

// checkHealth stamps CheckedAt from the injected clock on EVERY outcome. An
// unstamped observation would carry CheckedAt == 0, i.e. a real-looking
// 1970-01-01 timestamp for any consumer that reads it — the same 0-as-unknown
// fabrication this project rejects everywhere else.
func (a *OllamaCloudAdapter) checkHealth(ctx context.Context, creds StoredCredentials, scope string) (HealthObservation, error) {
	checkedAt := a.now().Unix()
	status, _, err := a.identityProbe(ctx, creds.Value)
	if err != nil {
		return ollamaUnreachable(scope, checkedAt), nil
	}
	switch {
	case status >= 200 && status < 300:
		return HealthObservation{Status: "healthy", Scope: scope, CredentialValid: true, TransportReachable: true, CheckedAt: checkedAt}, nil
	case status == 401 || status == 403:
		return HealthObservation{
			Status: "expired", Scope: scope, CredentialValid: false, TransportReachable: true, CheckedAt: checkedAt,
			Failure: &HealthFailure{Class: "auth", Retryable: false, SafeMessage: "provider rejected the credential (401/403)"},
		}, nil
	default:
		return ollamaUnreachable(scope, checkedAt), nil
	}
}

func ollamaUnreachable(scope string, checkedAt int64) HealthObservation {
	return HealthObservation{
		Status: "unreachable", Scope: scope, CredentialValid: false, TransportReachable: false, CheckedAt: checkedAt,
		Failure: &HealthFailure{Class: "unavailable", Retryable: true, SafeMessage: "provider unavailable or rate limited"},
	}
}

// RegisterOllamaCloud registers the ollama-cloud APIKey + Identity + Health +
// Discovery adapters into reg. now may be nil (real callers); tests inject a
// fake clock for the models.dev cache TTL. It does NOT wire itself into any
// composition root — that is the caller's job (httpapi's registerOllamaCloud).
func RegisterOllamaCloud(reg *Registry, identityProbe OllamaIdentityProbe, modelsProbe ModelsProbe, modelsDevProbe ModelsDevProbe, now func() time.Time) error {
	adapter := NewOllamaCloudAdapter(identityProbe, modelsProbe, modelsDevProbe, now)
	return reg.Register(Definition{
		ID:        OllamaCloudID,
		AuthMode:  AuthModeAPIKey,
		Transport: TransportKindOpenAICompatible,
		APIKey:    adapter,
		Identity:  adapter,
		Health:    adapter,
		Discovery: adapter,
	})
}

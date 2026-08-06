package providers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// --- fakes ----------------------------------------------------------------

// fakeOllamaIdentity is an OllamaIdentityProbe that returns a fixed
// status/body/err.
type fakeOllamaIdentity struct {
	status int
	body   string
	err    error
}

func (f *fakeOllamaIdentity) probe(_ context.Context, _ string) (int, []byte, error) {
	if f.err != nil {
		return 0, nil, f.err
	}
	return f.status, []byte(f.body), nil
}

// fakeModelsProbe is a ModelsProbe returning a fixed body/err.
type fakeModelsProbe struct {
	body string
	err  error
}

func (f *fakeModelsProbe) probe(_ context.Context, _ /*baseURL*/, _ /*key*/ string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []byte(f.body), nil
}

// staticModelsDevProbe returns a fixed dataset body/err.
func staticModelsDevProbe(body string, err error) ModelsDevProbe {
	return func(context.Context) ([]byte, error) {
		if err != nil {
			return nil, err
		}
		return []byte(body), nil
	}
}

func frozenClock() func() time.Time { return func() time.Time { return time.Unix(1_700_000_000, 0) } }

const ollamaMeOK = `{"ID":"user_abc123","Email":"a@b.com","Name":"Ann","Plan":"free"}`

const ollamaDiscoveryDataset = `{
  "ollama-cloud": {"models": {
    "keeper:1b": {"name": "Keeper", "tool_call": true, "structured_output": true,
      "modalities": {"input": ["text","image"], "output": ["text"]},
      "limit": {"context": 1000, "output": 100}},
    "dep:1b": {"status": "deprecated", "modalities": {"output": ["text"]}},
    "imgout:1b": {"modalities": {"output": ["image"]}},
    "plain:1b": {"modalities": {"output": ["text"]}}
  }}
}`

const ollamaModelsList = `{"data":[
  {"id":"keeper:1b"},{"id":"dep:1b"},{"id":"imgout:1b"},{"id":"plain:1b"},{"id":"uncatalogued:9b"}
]}`

func newOllamaAdapter(id *fakeOllamaIdentity, models *fakeModelsProbe, dataset ModelsDevProbe) *OllamaCloudAdapter {
	return NewOllamaCloudAdapter(id.probe, models.probe, dataset, frozenClock())
}

// --- catalog consistency --------------------------------------------------

// TestOllamaCloud_BaseURLMatchesCatalog is mutation row 9: the base URL const
// must equal the BuiltinCatalog entry.
func TestOllamaCloud_BaseURLMatchesCatalog(t *testing.T) {
	var entry CatalogEntry
	for _, e := range BuiltinCatalog() {
		if e.ID == OllamaCloudID {
			entry = e
		}
	}
	if entry.BaseURL != OllamaCloudBaseURL {
		t.Fatalf("OllamaCloudBaseURL = %q, catalog BaseURL = %q — they must match", OllamaCloudBaseURL, entry.BaseURL)
	}
}

// --- authentic validation -------------------------------------------------

// TestOllamaCloud_ValidKeyIdentityAndHealth proves a recognized key yields the
// account identity (ExternalID = ID) and a healthy observation.
func TestOllamaCloud_ValidKeyIdentityAndHealth(t *testing.T) {
	id := &fakeOllamaIdentity{status: 200, body: ollamaMeOK}
	a := newOllamaAdapter(id, &fakeModelsProbe{}, staticModelsDevProbe("{}", nil))

	res, creds, err := a.ConnectAPIKey(context.Background(), "  key-with-spaces  ")
	if err != nil {
		t.Fatalf("ConnectAPIKey() error = %v", err)
	}
	if res.ExternalID != "user_abc123" || res.Email != "a@b.com" {
		t.Fatalf("identity = %+v, want ExternalID user_abc123 / email a@b.com", res)
	}
	if creds.Value != "key-with-spaces" {
		t.Fatalf("stored creds = %q, want normalized 'key-with-spaces'", creds.Value)
	}
	h, _ := a.CheckAccountHealth(context.Background(), StoredCredentials{Value: "k"})
	if h.Status != "healthy" || !h.CredentialValid {
		t.Fatalf("health = %+v, want healthy/valid", h)
	}
}

// TestOllamaCloud_GarbageKeyCaughtByIdentityNotModels is mutation row 1: a key
// whose /v1/models answers 200 but whose /api/me answers 401 is INVALID — the
// authentic validation is /api/me, never the (false-green) models listing.
func TestOllamaCloud_GarbageKeyCaughtByIdentityNotModels(t *testing.T) {
	id := &fakeOllamaIdentity{status: 401}
	// /v1/models would answer 200 for any token — but discovery/validation must
	// not consult it for validity.
	a := newOllamaAdapter(id, &fakeModelsProbe{body: ollamaModelsList}, staticModelsDevProbe("{}", nil))

	_, _, err := a.ConnectAPIKey(context.Background(), "garbage")
	if !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("ConnectAPIKey() error = %v, want ErrInvalidCredential", err)
	}
}

// TestOllamaCloud_TwoxxWithoutIDIsTypedError is mutation row 2: a 2xx whose ID
// is empty is ErrIdentityMissingStableID, never a fingerprint fallback.
func TestOllamaCloud_TwoxxWithoutIDIsTypedError(t *testing.T) {
	id := &fakeOllamaIdentity{status: 200, body: `{"Email":"a@b.com","Plan":"free"}`}
	a := newOllamaAdapter(id, &fakeModelsProbe{}, staticModelsDevProbe("{}", nil))

	res, _, err := a.ConnectAPIKey(context.Background(), "k")
	if !errors.Is(err, ErrIdentityMissingStableID) {
		t.Fatalf("ConnectAPIKey() error = %v, want ErrIdentityMissingStableID", err)
	}
	if res.ExternalID != "" {
		t.Fatalf("ExternalID = %q, want empty (no fingerprint fallback)", res.ExternalID)
	}
}

// TestOllamaCloud_RetryableStatusesAreUnavailable is mutation row 4: 429 and a
// transport error are unavailable, NEVER invalid.
func TestOllamaCloud_RetryableStatusesAreUnavailable(t *testing.T) {
	t.Run("429", func(t *testing.T) {
		id := &fakeOllamaIdentity{status: 429}
		a := newOllamaAdapter(id, &fakeModelsProbe{}, staticModelsDevProbe("{}", nil))
		_, _, err := a.ConnectAPIKey(context.Background(), "k")
		if !errors.Is(err, ErrProviderUnavailable) {
			t.Fatalf("error = %v, want ErrProviderUnavailable (429 is retryable, not invalid)", err)
		}
	})
	t.Run("transport error", func(t *testing.T) {
		id := &fakeOllamaIdentity{err: errors.New("dial tcp: refused")}
		a := newOllamaAdapter(id, &fakeModelsProbe{}, staticModelsDevProbe("{}", nil))
		_, _, err := a.ConnectAPIKey(context.Background(), "k")
		if !errors.Is(err, ErrProviderUnavailable) {
			t.Fatalf("error = %v, want ErrProviderUnavailable", err)
		}
	})
}

// TestOllamaCloud_NoPlanEvidenceLeavesFundingEmpty is mutation row 3: with no
// free-plan evidence, Funding stays "" — never defaulted to "free".
func TestOllamaCloud_NoPlanEvidenceLeavesFundingEmpty(t *testing.T) {
	id := &fakeOllamaIdentity{status: 200, body: `{"ID":"u1","Email":"a@b.com"}`}
	a := newOllamaAdapter(id, &fakeModelsProbe{}, staticModelsDevProbe("{}", nil))
	res, _, err := a.ConnectAPIKey(context.Background(), "k")
	if err != nil {
		t.Fatalf("ConnectAPIKey() error = %v", err)
	}
	if res.Funding != "" {
		t.Fatalf("Funding = %q, want empty (no plan evidence)", res.Funding)
	}
}

// --- discovery ------------------------------------------------------------

// TestOllamaCloud_Discovery is mutations row 5 (capability grounding) and row 6
// (deprecated-only drop, Task 5 fix round 1): deprecated is dropped, an
// image-output entry is now KEPT and classified via image_generation instead
// of hidden, and — since it declares modalities.output = ["image"] with no
// "text" entry — it gets NO "chat" (chat is grounded in declared text
// output, not asserted unconditionally); tool_call/structured_output/
// image-input mapped, absent limits nil, uncatalogued id chat-only.
func TestOllamaCloud_Discovery(t *testing.T) {
	id := &fakeOllamaIdentity{status: 200, body: ollamaMeOK}
	a := newOllamaAdapter(id, &fakeModelsProbe{body: ollamaModelsList}, staticModelsDevProbe(ollamaDiscoveryDataset, nil))

	models, err := a.DiscoverModels(context.Background(), StoredCredentials{Value: "k"})
	if err != nil {
		t.Fatalf("DiscoverModels() error = %v", err)
	}
	byID := map[string]DiscoveredModel{}
	for _, m := range models {
		byID[m.ProviderModelID] = m
	}
	if _, dropped := byID["dep:1b"]; dropped {
		t.Fatal("deprecated model dep:1b must be dropped")
	}
	if _, ok := byID["imgout:1b"]; !ok {
		t.Fatal("image-output model imgout:1b must be kept — classified via image_generation, not hidden")
	}
	if len(models) != 4 {
		t.Fatalf("survivors = %d (%v), want 4 (keeper, imgout, plain, uncatalogued)", len(models), byID)
	}

	keeper := byID["keeper:1b"]
	if keeper.DisplayName != "Keeper" {
		t.Fatalf("keeper display = %q, want Keeper", keeper.DisplayName)
	}
	if !hasAll(keeper.Capabilities, "chat", "tools", "structured_output", "vision") {
		t.Fatalf("keeper caps = %v, want chat/tools/structured_output/vision", keeper.Capabilities)
	}
	if keeper.ContextLength == nil || *keeper.ContextLength != 1000 || keeper.MaxOutputTokens == nil || *keeper.MaxOutputTokens != 100 {
		t.Fatalf("keeper limits = ctx %v out %v, want 1000/100", keeper.ContextLength, keeper.MaxOutputTokens)
	}

	imgout := byID["imgout:1b"]
	if hasAll(imgout.Capabilities, "chat") {
		t.Fatalf("imgout caps = %v, want NO chat — modalities.output = [\"image\"] declares non-text-only output, so chat is not grounded", imgout.Capabilities)
	}
	if len(imgout.Capabilities) != 1 || imgout.Capabilities[0] != "image_generation" {
		t.Fatalf("imgout caps = %v, want exactly [image_generation] (modalities.output = [\"image\"], no other explicit fields)", imgout.Capabilities)
	}

	plain := byID["plain:1b"]
	if len(plain.Capabilities) != 1 || plain.Capabilities[0] != "chat" {
		t.Fatalf("plain caps = %v, want [chat] only", plain.Capabilities)
	}

	uncat := byID["uncatalogued:9b"]
	if len(uncat.Capabilities) != 1 || uncat.Capabilities[0] != "chat" || uncat.ContextLength != nil {
		t.Fatalf("uncatalogued = %+v, want chat-only with nil limits", uncat)
	}
}

// ollamaHostMismatchDataset is ollamaDiscoveryDataset's "keeper:1b" entry
// carried under a top-level `api` that does NOT match OllamaCloudBaseURL —
// simulating models.dev repointing the "ollama-cloud" key to a different
// host or reusing it for another product.
const ollamaHostMismatchDataset = `{
  "ollama-cloud": {"api": "https://impostor.example.com/v1", "models": {
    "keeper:1b": {"name": "Keeper", "tool_call": true, "structured_output": true,
      "modalities": {"input": ["text","image"], "output": ["text"]},
      "limit": {"context": 1000, "output": 100}}
  }}
}`

// TestOllamaCloud_DiscoverModels_HostMismatchYieldsNoEnrichment proves
// DiscoverModels goes through FactsForProvider (verified against
// OllamaCloudBaseURL), not the raw, unverified Facts lookup: when the
// dataset's "ollama-cloud" entry declares an `api` host that does not match
// our own base URL, the model must come back chat-only with nil limits —
// exactly as if the dataset had no entry at all — never joined onto the
// wrong provider's facts.
//
// MUTATION: reverting DiscoverModels to call a.facts.Facts(ctx,
// modelsDevOllamaKey) directly (bypassing the host check) turns this RED —
// keeper:1b would come back with tools/structured_output/vision and real
// limits instead of chat-only/nil.
func TestOllamaCloud_DiscoverModels_HostMismatchYieldsNoEnrichment(t *testing.T) {
	id := &fakeOllamaIdentity{status: 200, body: ollamaMeOK}
	a := newOllamaAdapter(id, &fakeModelsProbe{body: `{"data":[{"id":"keeper:1b"}]}`}, staticModelsDevProbe(ollamaHostMismatchDataset, nil))

	models, err := a.DiscoverModels(context.Background(), StoredCredentials{Value: "k"})
	if err != nil {
		t.Fatalf("DiscoverModels() error = %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("survivors = %d, want 1", len(models))
	}
	keeper := models[0]
	if len(keeper.Capabilities) != 1 || keeper.Capabilities[0] != "chat" {
		t.Fatalf("keeper caps = %v, want [chat] only — the host mismatch must refuse the whole entry, not just narrow it", keeper.Capabilities)
	}
	if keeper.ContextLength != nil || keeper.MaxOutputTokens != nil {
		t.Fatalf("keeper limits = ctx %v out %v, want both nil — a host mismatch is a refusal, not a partial join", keeper.ContextLength, keeper.MaxOutputTokens)
	}
}

// TestOllamaCloud_DatasetDownStillListsLiveIDs is mutation row 8: when the
// models.dev dataset is unavailable, discovery still returns EVERY live id as
// chat-only — a facts source being down must not make a working account look
// empty.
func TestOllamaCloud_DatasetDownStillListsLiveIDs(t *testing.T) {
	id := &fakeOllamaIdentity{status: 200, body: ollamaMeOK}
	a := newOllamaAdapter(id, &fakeModelsProbe{body: ollamaModelsList}, staticModelsDevProbe("", errors.New("models.dev down")))

	models, err := a.DiscoverModels(context.Background(), StoredCredentials{Value: "k"})
	if err != nil {
		t.Fatalf("DiscoverModels() error = %v, want nil (dataset-down must still list live ids)", err)
	}
	if len(models) != 5 {
		t.Fatalf("survivors = %d, want 5 (all live ids, chat-only)", len(models))
	}
	for _, m := range models {
		if len(m.Capabilities) != 1 || m.Capabilities[0] != "chat" {
			t.Fatalf("%s caps = %v, want [chat] only when dataset is down", m.ProviderModelID, m.Capabilities)
		}
	}
}

// TestOllamaCloud_KeyNeverInError proves the credential never appears in a
// returned error string.
func TestOllamaCloud_KeyNeverInError(t *testing.T) {
	const secret = "sk-ollama-super-secret"
	id := &fakeOllamaIdentity{status: 401}
	a := newOllamaAdapter(id, &fakeModelsProbe{}, staticModelsDevProbe("{}", nil))
	_, _, err := a.ConnectAPIKey(context.Background(), secret)
	if err != nil && strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaks the credential: %v", err)
	}
}

func hasAll(haystack []string, wants ...string) bool {
	set := map[string]bool{}
	for _, h := range haystack {
		set[h] = true
	}
	for _, w := range wants {
		if !set[w] {
			return false
		}
	}
	return true
}

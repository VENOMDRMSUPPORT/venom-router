package execution

import (
	"context"
	"errors"
	"fmt"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
)

// bifrostAccount is the minimal schemas.Account implementation this shim
// needs: exactly one provider, exactly one key whitelisted to exactly
// one model, retries disabled, pointed at a caller-supplied BaseURL —
// configured exactly per 01 §4.5 ("pool size 1, one key whitelisted to
// the one model, and retries disabled. Bifrost holds no authoritative
// catalog."). Venom's ResolvedRoute, not this Account, is the source of
// truth for which provider/account/model is used; BifrostTransport
// itself rejects any route that doesn't match what it was configured
// for (see checkRoute).
type bifrostAccount struct {
	provider schemas.ModelProvider
	config   schemas.ProviderConfig
	key      schemas.Key
}

func newBifrostAccount(provider schemas.ModelProvider, baseURL, apiKey, model string) *bifrostAccount {
	return &bifrostAccount{
		provider: provider,
		config: schemas.ProviderConfig{
			NetworkConfig: schemas.NetworkConfig{
				BaseURL:    baseURL,
				MaxRetries: 0, // retries disabled (01 §4.5)
			},
			ConcurrencyAndBufferSize: schemas.ConcurrencyAndBufferSize{
				Concurrency: 1, // pool size 1 (01 §4.5)
				BufferSize:  1,
			},
		},
		key: schemas.Key{
			ID:     "venom-smoke-key",
			Value:  *schemas.NewSecretVar(apiKey),
			Models: schemas.WhiteList{model}, // whitelisted to exactly one model
			Weight: 100,
		},
	}
}

func (a *bifrostAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	return []schemas.ModelProvider{a.provider}, nil
}

func (a *bifrostAccount) GetKeysForProvider(_ context.Context, _ schemas.ModelProvider) ([]schemas.Key, error) {
	return []schemas.Key{a.key}, nil
}

func (a *bifrostAccount) GetConfigForProvider(_ schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	cfg := a.config
	return &cfg, nil
}

// ErrRouteNotConfigured is returned when a route names a provider/model
// this BifrostTransport instance was not configured for. This is the
// enforcement mechanism behind 01 §4.5's single-route handoff guarantee:
// each BifrostTransport is built for exactly one (provider, model) pair,
// and every interface method rejects anything else before Bifrost is
// ever invoked — Bifrost's own key whitelist (above) is defense in
// depth, not the primary guarantee.
var ErrRouteNotConfigured = errors.New("execution: route not configured for this bifrost transport instance")

// BifrostTransport is the InferenceTransport implementation backed by
// the vendored Bifrost core (01 §4.3's "bifrost" transport type). One
// instance handles exactly one (provider, model) tuple.
type BifrostTransport struct {
	client   *bifrost.Bifrost
	provider ProviderID
	modelID  string
}

// BifrostTransportConfig configures a single-route BifrostTransport.
type BifrostTransportConfig struct {
	Provider ProviderID
	ModelID  string
	APIKey   string
	BaseURL  string
}

// NewBifrostTransport constructs a BifrostTransport configured exactly
// per 01 §4.5: pool size 1, one key whitelisted to one model, retries
// disabled.
func NewBifrostTransport(ctx context.Context, cfg BifrostTransportConfig) (*BifrostTransport, error) {
	provider := schemas.ModelProvider(cfg.Provider)
	account := newBifrostAccount(provider, cfg.BaseURL, cfg.APIKey, cfg.ModelID)

	client, err := bifrost.Init(ctx, schemas.BifrostConfig{
		Account:         account,
		Logger:          bifrost.NewDefaultLogger(schemas.LogLevelError),
		InitialPoolSize: 1,
	})
	if err != nil {
		return nil, fmt.Errorf("execution: init bifrost: %w", err)
	}

	return &BifrostTransport{client: client, provider: cfg.Provider, modelID: cfg.ModelID}, nil
}

// Close releases the underlying Bifrost client's resources.
func (t *BifrostTransport) Close() {
	t.client.Shutdown()
}

func (t *BifrostTransport) checkRoute(route ResolvedRoute) error {
	if route.Provider != t.provider || route.ModelID != t.modelID {
		return fmt.Errorf("%w: got provider=%q model=%q, configured for provider=%q model=%q",
			ErrRouteNotConfigured, route.Provider, route.ModelID, t.provider, t.modelID)
	}
	return nil
}

// Execute sends a single non-streamed chat completion request through
// Bifrost.
func (t *BifrostTransport) Execute(ctx context.Context, route ResolvedRoute, req NormalizedRequest) (*NormalizedResponse, error) {
	if err := t.checkRoute(route); err != nil {
		return nil, err
	}

	messages := make([]schemas.ChatMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		content := m.Content
		messages = append(messages, schemas.ChatMessage{
			Role:    schemas.ChatMessageRole(m.Role),
			Content: &schemas.ChatMessageContent{ContentStr: &content},
		})
	}

	bfCtx := schemas.NewBifrostContext(ctx, schemas.NoDeadline)
	resp, bifrostErr := t.client.ChatCompletionRequest(bfCtx, &schemas.BifrostChatRequest{
		Provider: schemas.ModelProvider(t.provider),
		Model:    t.modelID,
		Input:    messages,
	})
	if bifrostErr != nil {
		msg := "bifrost chat completion failed"
		if bifrostErr.Error != nil {
			msg = bifrostErr.Error.Message
		}
		return nil, fmt.Errorf("execution: %s", msg)
	}
	if len(resp.Choices) == 0 ||
		resp.Choices[0].ChatNonStreamResponseChoice == nil ||
		resp.Choices[0].Message == nil {
		return nil, errors.New("execution: bifrost returned no chat choice")
	}

	message := resp.Choices[0].Message
	content := ""
	if message.Content != nil && message.Content.ContentStr != nil {
		content = *message.Content.ContentStr
	}

	return &NormalizedResponse{
		Message:      Message{Role: string(message.Role), Content: content},
		FinishReason: derefOrEmpty(resp.Choices[0].FinishReason),
	}, nil
}

// Stream is not implemented by this smoke-test shim — streaming is P4.
func (t *BifrostTransport) Stream(_ context.Context, route ResolvedRoute, _ NormalizedRequest) (<-chan Chunk, error) {
	if err := t.checkRoute(route); err != nil {
		return nil, err
	}
	return nil, errors.New("execution: bifrost transport streaming is not implemented by the P0-EXEC-003 smoke shim (P4)")
}

// Cancel is not implemented by this smoke-test shim.
func (t *BifrostTransport) Cancel(_ context.Context, route ResolvedRoute, _ string) error {
	if err := t.checkRoute(route); err != nil {
		return err
	}
	return errors.New("execution: bifrost transport cancel is not implemented by the P0-EXEC-003 smoke shim (P4)")
}

// NormalizeError returns a minimal, generically-safe VenomError. The
// real provider-error-mapping logic (01 §4.2's full scope-classification
// table) is a separate, later task; this deliberately never touches err's
// content, so it trivially never leaks a credential or raw provider text.
func (t *BifrostTransport) NormalizeError(_ error, _ ResolvedRoute) VenomError {
	return VenomError{
		Code:      "internal",
		Message:   "an internal error occurred",
		Retryable: false,
	}
}

// SupportedCapabilities reports plain chat only — this smoke shim never
// claims streaming/tools/vision support.
func (t *BifrostTransport) SupportedCapabilities(_ ResolvedRoute) []Operation {
	return []Operation{OperationChat}
}

func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

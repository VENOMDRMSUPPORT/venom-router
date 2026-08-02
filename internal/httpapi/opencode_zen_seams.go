package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// openCodeZenHTTPTimeout bounds every real network call opencode-zen's
// two probes make, mirroring antigravityHTTPTimeout's rationale
// (antigravity_seams.go): neither the chat-completions nor the models
// endpoint should ever legitimately take longer than this.
const openCodeZenHTTPTimeout = 15 * time.Second

var openCodeZenHTTPClient = &http.Client{Timeout: openCodeZenHTTPTimeout}

// openCodeZenProbeBodyLimit bounds how much of a 401/403 chat-probe body we
// read to classify it. The provider's error envelopes are tiny; this only
// exists so a hostile/huge body can never exhaust memory.
const openCodeZenProbeBodyLimit = 8 << 10

// errOpenCodeZenNonAuth401 is the internal sentinel the chat probe returns when
// the provider answered 401/403 WITHOUT a positive authentication signal — a
// model/request-shape problem, or any error type the classifier does not
// recognise (billing is handled separately, as authenticated). It carries NO
// provider text (the message is read only to classify, then discarded) and
// maps, via providers.ValidateAPIKey's err!=nil branch, to
// ValidationUnavailable. This is the inverted default: falsely rejecting a good
// credential is an unrecoverable dead end for the owner, so only a positive
// auth signal may produce invalid; everything else fails closed to unavailable.
var errOpenCodeZenNonAuth401 = errors.New("httpapi: opencode-zen chat probe: 401/403 without a positive authentication signal (unavailable, not invalid)")

// openCodeZenChatProbeMessage / openCodeZenChatProbeRequest are the minimal
// OpenAI-compatible chat-completions request the probe sends.
type openCodeZenChatProbeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openCodeZenChatProbeRequest struct {
	Model     string                        `json:"model"`
	Messages  []openCodeZenChatProbeMessage `json:"messages"`
	MaxTokens int                           `json:"max_tokens"`
}

// openCodeZenModelList is the subset of GET /v1/models the chat probe reads to
// resolve a real model id.
type openCodeZenModelList struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// openCodeZenChatProbeSeam is the real (net/http-backed) implementation of
// providers.ChatProbe for opencode-zen (P2b-PROV-005/CAPI-003): 03 §1's
// authentic zero-cost chat probe (max_tokens: 1), NOT a host-up check.
//
// It first resolves a real model id from the provider's own catalog (GET
// {baseURL}/v1/models, which is public) and only then POSTs a single short
// user message with that model and max_tokens: 1. This models read is NOT a
// host-up check and does not weaken 03 §1: authentication is still exercised
// by the chat call — opencode-zen validates the model BEFORE the key, so a
// model-less body is rejected 401 (ModelError "Model ... is not supported")
// even with a valid key, which is the exact defect this fix repairs. The
// models read only supplies a valid model NAME so the chat call can reach
// authentication; the model id is NEVER hardcoded because the catalog changes.
//
// If the models read fails or the catalog is empty, the probe reports
// unavailable (returns a non-nil error), NEVER invalid. A 401/403 is triaged
// by body (classifyOpenCodeZen401Body): a billing/credits rejection proves the
// key authenticated (reported via providers.ErrProbeAuthenticated); a positive
// auth failure is invalid; anything else is unavailable. key is sent ONLY as
// the Authorization header value — never logged, never included in any error
// this function returns; provider error text is read solely to classify and is
// then discarded.
func openCodeZenChatProbeSeam(ctx context.Context, baseURL, key string) (int, error) {
	modelID, err := resolveOpenCodeZenModelID(ctx, baseURL, key)
	if err != nil {
		return 0, err
	}

	reqBody, err := json.Marshal(openCodeZenChatProbeRequest{
		Model:     modelID,
		Messages:  []openCodeZenChatProbeMessage{{Role: "user", Content: "ping"}},
		MaxTokens: 1,
	})
	if err != nil {
		return 0, fmt.Errorf("httpapi: opencode-zen chat probe marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return 0, fmt.Errorf("httpapi: opencode-zen chat probe request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := openCodeZenHTTPClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("httpapi: opencode-zen chat probe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// opencode-zen answers 401/403 for at least three distinct conditions, so
	// the wire status alone cannot classify the key. Read the body ONLY to
	// classify (its text never leaves this function) and invert the default:
	// only a POSITIVE authentication signal produces invalid; a billing
	// rejection proves the key authenticated; everything else fails closed to
	// unavailable.
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		probeBody, _ := io.ReadAll(io.LimitReader(resp.Body, openCodeZenProbeBodyLimit))
		switch classifyOpenCodeZen401Body(probeBody) {
		case ocz401Authenticated:
			// The provider answered 401 for BILLING (CreditsError / insufficient
			// balance). It could only look up the workspace balance AFTER
			// recognizing the key, so authentication SUCCEEDED. Report that
			// explicitly via providers.ErrProbeAuthenticated — deliberately NOT
			// a synthesized 200, so the returned value is traceable to this
			// reasoning rather than looking like a real network success.
			return 0, providers.ErrProbeAuthenticated
		case ocz401AuthFailure:
			// A positive auth signal (AuthError, or a clearly invalid/missing
			// key): return the real status so ValidateAPIKey maps it to invalid.
			return resp.StatusCode, nil
		default:
			// No positive auth signal (model/shape, unrecognized type, opaque
			// body): unavailable, never invalid.
			return 0, errOpenCodeZenNonAuth401
		}
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// resolveOpenCodeZenModelID reads GET {baseURL}/v1/models and returns the
// first advertised model id. A transport/status failure or an empty catalog
// returns a non-nil error so the caller reports unavailable, never invalid.
func resolveOpenCodeZenModelID(ctx context.Context, baseURL, key string) (string, error) {
	body, err := openCodeZenModelsProbeSeam(ctx, baseURL, key)
	if err != nil {
		return "", fmt.Errorf("httpapi: opencode-zen chat probe resolve model: %w", err)
	}
	var list openCodeZenModelList
	if err := json.Unmarshal(body, &list); err != nil {
		return "", fmt.Errorf("httpapi: opencode-zen chat probe resolve model: parse models: %w", err)
	}
	if len(list.Data) == 0 || list.Data[0].ID == "" {
		return "", errors.New("httpapi: opencode-zen chat probe resolve model: catalog returned no usable model id")
	}
	return list.Data[0].ID, nil
}

// openCodeZen401Kind is how classifyOpenCodeZen401Body triages a 401/403 body.
type openCodeZen401Kind int

const (
	// ocz401Unavailable: no positive authentication signal (a model/request-
	// shape problem, an unrecognized error type, or an opaque body) — the key
	// cannot be judged, so fail closed to unavailable, never invalid.
	ocz401Unavailable openCodeZen401Kind = iota
	// ocz401AuthFailure: a positive authentication failure (an AuthError type,
	// or a message that clearly states the key is invalid or missing).
	ocz401AuthFailure
	// ocz401Authenticated: a billing/credits rejection — the provider computed
	// a workspace balance, which it could only do after recognizing the key,
	// so authentication succeeded.
	ocz401Authenticated
)

// classifyOpenCodeZen401Body triages a 401/403 response body. It keys on
// byte-level markers of the recorded live envelopes (CreditsError /
// "insufficient balance"; AuthError / "invalid api key" / "missing api key").
// The default is unavailable — the inverted policy: falsely branding a good
// credential invalid is an unrecoverable dead end for the owner and has now
// happened twice against this provider (ModelError, then CreditsError), whereas
// wrongly accepting a bad key surfaces on the very first request and is
// recoverable. So only a positive auth signal yields invalid, and only a
// billing signal yields authenticated; everything else is unavailable.
func classifyOpenCodeZen401Body(body []byte) openCodeZen401Kind {
	hay := strings.ToLower(string(body))
	switch {
	case strings.Contains(hay, "creditserror") || strings.Contains(hay, "insufficient balance"):
		return ocz401Authenticated
	case strings.Contains(hay, "autherror") || strings.Contains(hay, "invalid api key") || strings.Contains(hay, "missing api key"):
		return ocz401AuthFailure
	default:
		return ocz401Unavailable
	}
}

// openCodeZenModelsProbeSeam is the real implementation of
// providers.ModelsProbe for opencode-zen's model-discovery capability
// (P2b-PROV-005): a GET to {baseURL}/v1/models. key is sent ONLY as the
// Authorization header value — never logged.
func openCodeZenModelsProbeSeam(ctx context.Context, baseURL, key string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/models", nil)
	if err != nil {
		return nil, fmt.Errorf("httpapi: opencode-zen models probe request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := openCodeZenHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpapi: opencode-zen models probe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("httpapi: opencode-zen models probe: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("httpapi: opencode-zen models probe returned status %d", resp.StatusCode)
	}
	return respBody, nil
}

// registerOpenCodeZen registers the opencode-zen API-key adapter into
// reg using this file's real HTTP seams. Unlike antigravity
// (registerAntigravityIfConfigured), this is unconditional — opencode-zen
// needs no confidential-client env vars, so there is nothing to gate on.
// The returned error is non-nil only if reg.Register itself rejects the
// registration (e.g. a duplicate "opencode-zen" registration), which
// cannot happen in this composition since this is the only call site
// that ever registers opencode-zen into a given Registry.
func registerOpenCodeZen(reg *providers.Registry) error {
	return providers.RegisterOpenCodeZen(reg, openCodeZenChatProbeSeam, openCodeZenModelsProbeSeam)
}

package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// claudeCodeHTTPTimeout bounds every real network call claude-code's seams make.
const claudeCodeHTTPTimeout = 15 * time.Second

var claudeCodeHTTPClient = &http.Client{Timeout: claudeCodeHTTPTimeout}

// The claude-code required headers on every AUTHENTICATED api.anthropic.com
// call (03 §3): missing any is a 429. These values mirror the ones the
// native_oauth transport's anthropic codec sets on /v1/messages
// (internal/execution/anthropicwire.go) — they are the same wire contract and
// must stay in sync. A claude-code-specific beta string may need to be appended
// after live re-verification (see the batch report).
const (
	claudeCodeAnthropicVersion = "2023-06-01"
	claudeCodeAnthropicBeta    = "oauth-2025-04-20"
	claudeCodeAppHeader        = "cli"
	claudeCodeUserAgent        = "claude-cli/0.1 (venom-router)"
)

// claudeCodeTokenSeam is the real implementation of providers.ClaudeCodeTokenProbe:
// a POST with form as the x-www-form-urlencoded body to the OAuth token
// endpoint. form (carrying the code/verifier/refresh token) is never logged.
func claudeCodeTokenSeam(ctx context.Context, tokenURL string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("httpapi: claude-code token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := claudeCodeHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpapi: claude-code token endpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("httpapi: claude-code token endpoint: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("httpapi: claude-code token endpoint returned status %d", resp.StatusCode)
	}
	return body, nil
}

// claudeCodeGetSeam is the real implementation of providers.ClaudeCodeGetProbe:
// an authenticated GET carrying the required claude-code headers, returning the
// raw status + body so the adapter can classify (identity/health) and parse.
// accessToken is sent only as a header — never logged.
func claudeCodeGetSeam(ctx context.Context, reqURL, accessToken string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("httpapi: claude-code GET request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-version", claudeCodeAnthropicVersion)
	req.Header.Set("anthropic-beta", claudeCodeAnthropicBeta)
	req.Header.Set("X-App", claudeCodeAppHeader)
	req.Header.Set("User-Agent", claudeCodeUserAgent)

	resp, err := claudeCodeHTTPClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("httpapi: claude-code GET: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("httpapi: claude-code GET: read response: %w", err)
	}
	return resp.StatusCode, body, nil
}

// registerClaudeCode registers the claude-code OAuth adapter into reg over the
// real seams. It is a PUBLIC client (no secret), so it registers
// unconditionally — nothing to gate on.
func registerClaudeCode(reg *providers.Registry) error {
	return providers.RegisterClaudeCode(reg, claudeCodeTokenSeam, claudeCodeGetSeam)
}

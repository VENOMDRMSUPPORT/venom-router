package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/platform"
	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// antigravityHTTPTimeout bounds every real network call antigravity's
// three seams make — none of Google's token/userinfo/loadCodeAssist
// endpoints should ever legitimately take longer than this.
const antigravityHTTPTimeout = 15 * time.Second

var antigravityHTTPClient = &http.Client{Timeout: antigravityHTTPTimeout}

// antigravityTokenSeam is the real (net/http-backed) implementation of
// providers.AntigravityTokenProbe (P2b-PROV-007): a POST with form as
// the application/x-www-form-urlencoded body. This is the ONLY place in
// this composition that ever puts the antigravity client secret (carried
// inside form) on the wire — it is never logged here, and this function
// never returns anything beyond the raw response body/a transport error.
func antigravityTokenSeam(ctx context.Context, tokenURL string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("httpapi: antigravity token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := antigravityHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpapi: antigravity token endpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("httpapi: antigravity token endpoint: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("httpapi: antigravity token endpoint returned status %d", resp.StatusCode)
	}
	return body, nil
}

// antigravityUserInfoSeam is the real implementation of
// providers.AntigravityGetProbe used for the userinfo identity fetch.
// accessToken is sent only as a request header — never logged.
func antigravityUserInfoSeam(ctx context.Context, reqURL, accessToken string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("httpapi: antigravity userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := antigravityHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpapi: antigravity userinfo endpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("httpapi: antigravity userinfo endpoint: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("httpapi: antigravity userinfo endpoint returned status %d", resp.StatusCode)
	}
	return body, nil
}

// antigravityLoadCodeAssistSeam is the real implementation of
// providers.AntigravityPostProbe used for the loadCodeAssist plan/
// project fetch. accessToken is sent only as a request header — never
// logged.
func antigravityLoadCodeAssistSeam(ctx context.Context, reqURL, accessToken string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("httpapi: antigravity loadCodeAssist request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := antigravityHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpapi: antigravity loadCodeAssist endpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("httpapi: antigravity loadCodeAssist endpoint: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("httpapi: antigravity loadCodeAssist endpoint returned status %d", resp.StatusCode)
	}
	return respBody, nil
}

// registerAntigravityIfConfigured registers the antigravity OAuth
// adapter into reg using this file's real HTTP seams, but ONLY when
// both VENOM_ANTIGRAVITY_CLIENT_ID/VENOM_ANTIGRAVITY_CLIENT_SECRET are
// present and non-empty (platform.AntigravityOAuthClientCredentials) —
// env is read exactly once, here, at composition time (ControlMux
// construction), mirroring how every other composition-root value is
// resolved once at boot. When either var is absent, this registers
// nothing: that is not an error (a fresh install with antigravity
// simply not yet configured is the expected, common case) — GET
// /providers/antigravity already reports configured=false plus the two
// missing var NAMES via platform.EnvPresent (providers.go), entirely
// independent of whether a live adapter is registered.
//
// The returned error is non-nil only if reg.Register itself rejects the
// registration (e.g. a duplicate "antigravity" registration) — which
// cannot happen in this composition, since this is the only call site
// that ever registers antigravity into a given Registry and it is only
// ever invoked once per ControlMux build. Callers may safely discard a
// nil-returning call's error; the return value exists so a genuine
// programming-error regression surfaces instead of silently vanishing.
func registerAntigravityIfConfigured(reg *providers.Registry) error {
	clientID, clientSecret, ok := platform.AntigravityOAuthClientCredentials()
	if !ok {
		return nil
	}
	return providers.RegisterAntigravity(reg, clientID, clientSecret, antigravityTokenSeam, antigravityUserInfoSeam, antigravityLoadCodeAssistSeam)
}

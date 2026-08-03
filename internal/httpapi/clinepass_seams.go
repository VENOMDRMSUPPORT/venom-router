package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

const clinePassHTTPTimeout = 15 * time.Second

var clinePassHTTPClient = &http.Client{Timeout: clinePassHTTPTimeout}

// clinepass wire decoration on authenticated calls (03 §3): the access token is
// used as Authorization: Bearer workos:<token> (the workos: prefix is a wire
// concern, applied here — the STORED token stays raw), plus the three cline
// headers. These mirror what the native_oauth openai_chat codec applies on the
// chat path (internal/execution/nativeoauth.go); the same contract, two call
// paths (the adapter's identity/discovery/quota GETs, and the chat transport).
const (
	clinePassTokenPrefix = "workos:"
	clinePassReferer     = "https://cline.bot"
	clinePassTitle       = "Cline"
	clinePassClientType  = "venom-router"
)

// clinePassPostSeam is the real implementation of providers.ClinePassPostProbe:
// a JSON POST (token / refresh). It returns the raw status + body so the
// adapter classifies; the body (code / refresh token) is never logged.
func clinePassPostSeam(ctx context.Context, reqURL string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("httpapi: clinepass POST request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	clinePassApplyHeaders(req.Header)

	resp, err := clinePassHTTPClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("httpapi: clinepass POST: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("httpapi: clinepass POST: read response: %w", err)
	}
	return resp.StatusCode, respBody, nil
}

// clinePassGetSeam is the real implementation of providers.ClinePassGetProbe: an
// authenticated GET carrying Authorization: Bearer workos:<token> and the cline
// headers, returning the raw status + body. accessToken is never logged. A
// BLANK accessToken means no Authorization header is sent at all — the
// recommended-models endpoint is public and must not receive a workos:-prefixed
// header (legacy 2026-08-03 fetches it headerless).
func clinePassGetSeam(ctx context.Context, reqURL, accessToken string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("httpapi: clinepass GET request: %w", err)
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+clinePassWorkosPrefixed(accessToken))
	}
	clinePassApplyHeaders(req.Header)

	resp, err := clinePassHTTPClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("httpapi: clinepass GET: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("httpapi: clinepass GET: read response: %w", err)
	}
	return resp.StatusCode, body, nil
}

func clinePassApplyHeaders(h http.Header) {
	h.Set("HTTP-Referer", clinePassReferer)
	h.Set("X-Title", clinePassTitle)
	h.Set("X-CLIENT-TYPE", clinePassClientType)
}

// clinePassWorkosPrefixed applies the workos: prefix idempotently.
func clinePassWorkosPrefixed(token string) string {
	if strings.HasPrefix(token, clinePassTokenPrefix) {
		return token
	}
	return clinePassTokenPrefix + token
}

// registerClinePass registers the clinepass OAuth adapter into reg over the real
// seams. Registered unconditionally (no confidential-client env vars).
func registerClinePass(reg *providers.Registry) error {
	return providers.RegisterClinePass(reg, clinePassPostSeam, clinePassGetSeam)
}

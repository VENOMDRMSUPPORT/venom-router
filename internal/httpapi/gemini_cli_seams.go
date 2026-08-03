package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/VENOMDRMSUPPORT/venom-router/internal/providers"
)

// geminiHTTPTimeout bounds every real network call gemini-cli's probe makes.
const geminiHTTPTimeout = 15 * time.Second

var geminiHTTPClient = &http.Client{Timeout: geminiHTTPTimeout}

// geminiListPageSize is the page size the model listing requests. A single
// page validates the credential (03 §3 uses pageSize=1 for health as a
// bandwidth optimization; the auth semantics are identical either way, and
// discovery wants a large page), so both the validation and discovery calls go
// through this one seam.
const geminiListPageSize = 200

// googleModelsProbeSeam is the real (net/http-backed) implementation of
// providers.GoogleModelsProbe: GET {baseURL}/v1beta/models with the Google
// API-key header `x-goog-api-key` (NOT Bearer, and NO Authorization header),
// paging via the request parameter `pageToken` (the response field is
// `nextPageToken` — a different name). key is sent ONLY as the header value and
// is never logged.
func googleModelsProbeSeam(ctx context.Context, baseURL, key, pageToken string) (int, []byte, error) {
	u := fmt.Sprintf("%s/v1beta/models?pageSize=%d", baseURL, geminiListPageSize)
	if pageToken != "" {
		u += "&pageToken=" + url.QueryEscape(pageToken)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("httpapi: gemini-cli models probe request: %w", err)
	}
	req.Header.Set("x-goog-api-key", key)

	resp, err := geminiHTTPClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("httpapi: gemini-cli models probe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("httpapi: gemini-cli models probe: read response: %w", err)
	}
	return resp.StatusCode, body, nil
}

// registerGeminiCLI registers the gemini-cli API-key adapter into reg over the
// real Google models probe. Always registered unconditionally; the returned
// error is non-nil only if reg rejects the registration (a duplicate).
func registerGeminiCLI(reg *providers.Registry) error {
	return providers.RegisterGeminiCLI(reg, googleModelsProbeSeam, nil)
}

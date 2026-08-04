package providers

import "encoding/json"

// OAuthTokenExpiry reads the unix-seconds `expires_at` field every OAuth
// adapter in this package stores inside its credential JSON (claude-code,
// clinepass, antigravity all persist {"access_token", "refresh_token",
// "expires_at", ...}). It exists so the token-refresh orchestration can decide
// "is this credential close to expiry?" WITHOUT a per-provider interface or a
// slug switch: the stored-token envelope is this package's own convention, so
// this package owns the one reader for it.
//
// ok is false when the stored value is not JSON, carries no expires_at, or
// carries a non-positive one — the caller must then treat expiry as unknown
// (refresh cadence decides what to do), never assume "far future".
func OAuthTokenExpiry(creds StoredCredentials) (unixSeconds int64, ok bool) {
	var stored struct {
		ExpiresAt int64 `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(creds.Value), &stored); err != nil {
		return 0, false
	}
	if stored.ExpiresAt <= 0 {
		return 0, false
	}
	return stored.ExpiresAt, true
}

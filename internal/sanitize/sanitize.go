package sanitize

import (
	"regexp"
	"strings"
)

// Placeholder is the exact string that replaces every redacted secret.
// Redaction is always full: the entire secret value is replaced by this
// constant, never a partial or masked form.
const Placeholder = "[REDACTED]"

// secretIndicators are lowercase substrings that mark a structured field
// key as carrying a secret. Matching is case-insensitive substring
// matching, so "token" also covers "access_token"/"refresh_token" and
// "secret" also covers "client_secret". These deliberately avoid the
// bare word "key" — it appears in non-secret identifiers such as
// window_key and cache_key — so the key category is expressed with the
// specific compounds api_key/apikey/access_key/private_key instead.
var secretIndicators = []string{
	"credential", // credential, credentials
	"password",
	"passwd",
	"passphrase",
	"secret", // secret, client_secret, secret_key
	"token",  // token, access_token, refresh_token, id_token
	"apikey",
	"api_key",
	"access_key",
	"private_key",
	"authorization", // Authorization header
	"cookie",
	"session", // session, session_token, session_id
	"bearer",
	"pkce",
	"code_verifier",
	"code_challenge",
}

// exactSecretKeys are short, ambiguous OAuth parameter names that are
// secret only when they name the whole field. They are matched by exact
// (case-insensitive) equality rather than as substrings, so benign keys
// that merely contain them — status_code, error_code, statement — are
// not redacted.
var exactSecretKeys = map[string]bool{
	"code":  true, // OAuth authorization code
	"state": true, // OAuth state
}

// nonSecretKeys is the allowlist of known-non-secret identifiers that
// must stay readable in logs. It is checked before any secret matching,
// so these are never redacted even if a broader indicator later overlaps
// them. key_id in particular is the public keyring key identifier and is
// NOT a secret.
var nonSecretKeys = map[string]bool{
	"key_id":     true,
	"account_id": true,
	"window_key": true,
	"cache_key":  true,
	"request_id": true,
	"job_id":     true,
	"provider":   true,
	"model":      true,
	"status":     true,
	"stage":      true,
}

// IsSecretKey reports whether key names a secret field.
//
// Resolution order (fail closed within the secret categories, without
// over-redacting known operational fields):
//
//  1. The nonSecretKeys allowlist is consulted first: an exact
//     (case-insensitive) match is never a secret.
//  2. An exact match in exactSecretKeys (the short OAuth names code and
//     state) is a secret.
//  3. Otherwise the key is a secret if it contains any secretIndicators
//     substring (case-insensitive).
func IsSecretKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "" {
		return false
	}
	if nonSecretKeys[k] {
		return false
	}
	if exactSecretKeys[k] {
		return true
	}
	for _, indicator := range secretIndicators {
		if strings.Contains(k, indicator) {
			return true
		}
	}
	return false
}

// Value returns Placeholder when key names a secret field, and otherwise
// returns value unchanged. When it redacts it returns the placeholder in
// full and never any portion of value.
func Value(key, value string) string {
	if IsSecretKey(key) {
		return Placeholder
	}
	return value
}

var (
	// authHeaderRe matches an Authorization header value, keeping the
	// header name and any Bearer/Basic scheme visible while redacting the
	// credential itself.
	authHeaderRe = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*)(bearer\s+|basic\s+)?(\S+)`)

	// schemeTokenRe matches a standalone Bearer/Basic credential that is
	// not attached to an Authorization header. The token character class
	// excludes the '[' and ']' of the placeholder, so a value already
	// redacted by authHeaderRe is not matched a second time.
	schemeTokenRe = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]+`)

	// paramValueRe matches query-style key=value pairs whose key names a
	// secret, redacting only the value and keeping the key visible.
	// Longer keys are listed before their prefixes so the most specific
	// key wins the leftmost-first match.
	paramValueRe = regexp.MustCompile(`(?i)\b(code_verifier|code_challenge|refresh_token|access_token|id_token|client_secret|credentials|credential|api[_-]?key|apikey|password|passwd|passphrase|secret|token|cookie|code|state)=([^&\s]+)`)
)

// Text redacts known secret token shapes embedded in free text: it fully
// replaces the credential in an Authorization header (or a standalone
// Bearer/Basic credential) and the value of any secret-named query-style
// parameter (for example code=, state=, code_verifier=, api_key=). The
// surrounding non-secret text — including the header name, scheme word,
// and each parameter key — is left intact so the output stays readable.
func Text(s string) string {
	s = authHeaderRe.ReplaceAllString(s, "${1}${2}"+Placeholder)
	s = schemeTokenRe.ReplaceAllString(s, "${1} "+Placeholder)
	s = paramValueRe.ReplaceAllString(s, "${1}="+Placeholder)
	return s
}

package httpapi

import (
	"net"
	"net/http"
	"strings"
)

// networkGate fronts next with the loopback + Host-allowlist network
// gate 01 §6a mandates for the control-plane bind:
//
//   - Loopback-only: the request's actual TCP socket RemoteAddr must be
//     loopback (127.0.0.0/8 or ::1). X-Forwarded-For or any other proxy
//     header is never consulted — only the real socket address.
//   - Host header allowlist: only "localhost", "127.0.0.1", "::1", or
//     allowedHost (the composition root's configured bind host:port) are
//     accepted. Anything else is rejected with 403 before next ever
//     runs — this is the DNS-rebinding defense.
//
// Both checks run before any session/CSRF concern (there is none for
// /health) and before next is invoked at all.
func networkGate(allowedHost string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackRemoteAddr(r.RemoteAddr) {
			http.Error(w, "forbidden: not loopback", http.StatusForbidden)
			return
		}
		if !isAllowedHost(r.Host, allowedHost) {
			http.Error(w, "forbidden: host not allowed", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLoopbackRemoteAddr reports whether remoteAddr (an http.Request's
// RemoteAddr, "host:port" from the real accepted TCP connection) is a
// loopback address. It never looks at any header.
func isLoopbackRemoteAddr(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr // no port present; fail closed on the raw value below rather than guessing
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isAllowedHost reports whether host (an http.Request's Host field)
// matches one of the bare loopback hostnames or the exact configured
// bind allowedHost. Comparison is case-insensitive; no port stripping or
// other normalization is applied beyond that, since the exact-match
// requirement is the DNS-rebinding defense itself.
func isAllowedHost(host, allowedHost string) bool {
	h := strings.ToLower(host)
	switch h {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return h == strings.ToLower(allowedHost)
}

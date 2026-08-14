// Package wstunnel holds the pieces both the gateway and agenda-node need to
// carry a WebSocket safely: detecting an upgrade request, admitting it against
// connection limits, applying an idle timeout to the resulting tunnel, and
// draining live tunnels on shutdown.
//
// Deliberately NOT in this package: anything that parses or rewrites WebSocket
// frames. Both proxies stay a byte tunnel after the handshake — the handshake
// is HTTP and is policed here, the payload is opaque.
package wstunnel

import (
	"net"
	"net/http"
	"strings"
)

// ProtocolWebSocket is the only Upgrade token either proxy accepts.
const ProtocolWebSocket = "websocket"

// UpgradeProtocol returns the lowercased protocol token of an HTTP/1.1 upgrade
// request ("websocket", "h2c", …), or "" when the request is not an upgrade.
//
// It mirrors what net/http/httputil.ReverseProxy itself keys off, so a request
// this returns "" for is guaranteed to be proxied as an ordinary HTTP request
// and one it returns non-"" for is the one ReverseProxy will try to tunnel.
// Only HTTP/1.x is considered: HTTP/2 has no Upgrade mechanism, and RFC 8441
// Extended CONNECT is out of scope.
func UpgradeProtocol(r *http.Request) string {
	if r == nil || r.ProtoMajor != 1 {
		return ""
	}
	if !headerHasToken(r.Header, "Connection", "upgrade") {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(r.Header.Get("Upgrade")))
}

// IsWebSocketUpgrade reports whether r is specifically an RFC 6455 handshake.
func IsWebSocketUpgrade(r *http.Request) bool {
	return UpgradeProtocol(r) == ProtocolWebSocket
}

// headerHasToken reports whether a comma-separated header list contains token
// (case-insensitively) as a whole element — "keep-alive, Upgrade" contains
// "upgrade", "upgrade-insecure-requests" does not.
func headerHasToken(h http.Header, key, token string) bool {
	for _, value := range h.Values(key) {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// OriginAllowed checks a browser handshake's Origin against a route's
// allowlist. An empty allowlist allows everything (the default, and the only
// sensible one for non-browser clients, which send no Origin at all). Once an
// allowlist IS configured, a request with no Origin is rejected — otherwise the
// check would be trivially bypassed by any non-browser client, which is exactly
// the CSRF-style attack the allowlist exists to stop.
//
// Entries may be a full origin ("https://app.example.com"), a bare host
// ("app.example.com", matching any scheme), or a wildcard on the leftmost
// labels ("*.example.com", "https://*.example.com"). "*" allows any origin.
func OriginAllowed(origin string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	originScheme, originHost := splitOrigin(origin)
	if originHost == "" {
		return false
	}
	for _, entry := range allowed {
		if entry == "*" {
			return true
		}
		entryScheme, entryHost := splitOrigin(entry)
		if entryHost == "" {
			continue
		}
		if entryScheme != "" && entryScheme != originScheme {
			continue
		}
		if hostMatches(originHost, entryHost) {
			return true
		}
	}
	return false
}

// splitOrigin lowercases an origin and splits it into scheme and host[:port].
// A value with no "://" is treated as a bare host with any scheme.
func splitOrigin(raw string) (scheme, host string) {
	raw = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(raw, "/")))
	if raw == "" || raw == "null" {
		return "", ""
	}
	if i := strings.Index(raw, "://"); i >= 0 {
		return raw[:i], raw[i+3:]
	}
	return "", raw
}

func hostMatches(host, pattern string) bool {
	if host == pattern {
		return true
	}
	if suffix, ok := strings.CutPrefix(pattern, "*."); ok {
		// "*.example.com" matches "a.example.com" and "a.b.example.com" but not
		// the bare "example.com" — a wildcard covers subdomains, not the apex.
		return strings.HasSuffix(host, "."+suffix)
	}
	return false
}

// ClientIP is the peer address of the TCP connection, used as the key for the
// per-IP connection cap. It deliberately ignores X-Forwarded-For: that header
// is caller-controlled, so trusting it would let one client evade the cap by
// making up addresses. Behind an L7 load balancer every tunnel therefore counts
// against the balancer's own address — tune the per-IP cap accordingly, or
// leave it at 0 (off) and rely on the global cap.
func ClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

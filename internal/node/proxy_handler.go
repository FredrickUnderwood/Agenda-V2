package node

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/wstunnel"
)

// ProxyHandler is the data-plane reverse proxy on the proxy listen port. It
// matches "/i/<instance>/<rest>", looks up the instance's current local port in
// the registry, and forwards to backendHost:<port>. This is a dumb forwarder —
// it does no health/circuit logic; routing decisions stay with the gateway.
//
// That includes WebSockets: whether a route may be upgraded at all, which
// Origins are allowed and what the connection caps are were already decided by
// the gateway before the request got here, and re-deciding them at this hop
// would just be a second, weaker copy of the same policy. What this hop does
// own is the fact that a relayed tunnel holds a connection *on this machine*,
// so tunnels are tracked in order to be drained on shutdown.
type ProxyHandler struct {
	registry    *ProxyRegistry
	backendHost string
	tunnels     *wstunnel.Registry

	// Transports are built once and shared across requests: they own the
	// connection pool, which a per-request transport would silently defeat.
	httpTransport http.RoundTripper
	wsTransport   http.RoundTripper
}

// NewProxyHandler builds a ProxyHandler. backendHost is the host apps'
// published ports are reachable at from node's own network namespace —
// "127.0.0.1" when node runs alongside the apps it deploys (bare metal/VM),
// or "host.docker.internal" when node drives a separate host's dockerd over
// docker.sock (docker-outside-of-docker) and apps' ports live on that host,
// not inside node's own container. Empty defaults to "127.0.0.1".
func NewProxyHandler(registry *ProxyRegistry, backendHost string) *ProxyHandler {
	if backendHost == "" {
		backendHost = "127.0.0.1"
	}
	return &ProxyHandler{
		registry:      registry,
		backendHost:   backendHost,
		tunnels:       wstunnel.NewRegistry(),
		httpTransport: newRelayTransport(),
		wsTransport:   newRelayWebSocketTransport(),
	}
}

func newRelayTransport() *http.Transport {
	return &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

// newRelayWebSocketTransport is pinned to HTTP/1.1 (the only HTTP version with
// an Upgrade mechanism) and does not pool connections, since an upgraded one is
// consumed by its tunnel. The short response-header timeout bounds an app that
// accepts the TCP connection but never completes the handshake.
func newRelayWebSocketTransport() *http.Transport {
	return &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     false,
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
		ResponseHeaderTimeout: 10 * time.Second,
		DisableKeepAlives:     true,
	}
}

func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	instance, rest, ok := splitInstancePrefix(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	port, ok := h.registry.Get(instance)
	if !ok {
		http.Error(w, "unknown instance: "+instance, http.StatusBadGateway)
		return
	}
	target := &url.URL{Scheme: "http", Host: h.backendHost + ":" + strconv.Itoa(port)}
	proxy := httputil.NewSingleHostReverseProxy(target)
	r.URL.Path = rest

	if wstunnel.UpgradeProtocol(r) == "" {
		proxy.Transport = h.httpTransport
		proxy.ServeHTTP(w, r)
		return
	}

	proxy.Transport = h.wsTransport
	// No admission limits here — the gateway already applied the route's. The
	// slot exists so shutdown can find this connection; it is never refused,
	// except while already draining.
	slot, err := h.tunnels.Admit(wstunnel.Key{Instance: instance}, wstunnel.ClientIP(r), wstunnel.Limits{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer slot.Release()

	hook := &wstunnel.HijackHook{ResponseWriter: w}
	hook.OnHijack = func(conn net.Conn) net.Conn {
		slot.Attach(conn)
		return conn
	}
	proxy.ServeHTTP(hook, r)
}

// BeginDrain stops the relay accepting new tunnels.
func (h *ProxyHandler) BeginDrain() { h.tunnels.BeginDrain() }

// Drain waits for relayed tunnels to end, force-closing the remainder when ctx
// expires; it returns how many it had to force.
func (h *ProxyHandler) Drain(ctx context.Context) int { return h.tunnels.Drain(ctx) }

// ActiveTunnels is the number of WebSocket tunnels currently relayed.
func (h *ProxyHandler) ActiveTunnels() int { return h.tunnels.Active() }

// splitInstancePrefix parses "/i/<instance>[/<rest>]" into (instance, rest, ok).
// rest always starts with "/" (defaulting to "/" when the path is just the
// instance). A path that is not under /i/ returns ok=false.
func splitInstancePrefix(path string) (instance, rest string, ok bool) {
	const prefix = "/i/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	remainder := path[len(prefix):]
	if remainder == "" {
		return "", "", false
	}
	slash := strings.IndexByte(remainder, '/')
	if slash < 0 {
		return remainder, "/", true
	}
	instance = remainder[:slash]
	if instance == "" {
		return "", "", false
	}
	return instance, remainder[slash:], true
}

package node

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

// ProxyHandler is the data-plane reverse proxy on the proxy listen port. It
// matches "/i/<instance>/<rest>", looks up the instance's current local port in
// the registry, and forwards to backendHost:<port>. This is a dumb forwarder —
// it does no health/circuit logic; routing decisions stay with the gateway.
type ProxyHandler struct {
	registry    *ProxyRegistry
	backendHost string
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
	return &ProxyHandler{registry: registry, backendHost: backendHost}
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
	proxy.ServeHTTP(w, r)
}

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

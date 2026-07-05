package node

import "sync"

// ProxyRegistry maps an instance name to the local port it currently listens on.
// It is in-memory only (like the JobStore) — a node restart empties it, and the
// control plane repopulates it on the next deploy/route sync. This is what lets
// a gateway backend URL stay stable (node proxy port + /i/<instance>) even when
// the instance's real port drifts across redeploys.
type ProxyRegistry struct {
	mu     sync.RWMutex
	routes map[string]int
}

func NewProxyRegistry() *ProxyRegistry {
	return &ProxyRegistry{routes: make(map[string]int)}
}

// Set records that instance currently listens on the given local port.
func (r *ProxyRegistry) Set(instance string, port int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes[instance] = port
}

// Get returns the local port for instance, if registered.
func (r *ProxyRegistry) Get(instance string) (int, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	port, ok := r.routes[instance]
	return port, ok
}

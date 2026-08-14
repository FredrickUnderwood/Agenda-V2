package wstunnel

import (
	"context"
	"errors"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Key identifies the traffic bucket a tunnel belongs to. The gateway fills in
// both fields; agenda-node, which knows nothing about routes, uses Instance
// alone.
type Key struct {
	RouteKey string
	Instance string
}

// Limits caps concurrent tunnels. A zero field means "no limit at this level";
// every non-zero limit is enforced independently, so a route can be capped
// tighter than the process and one client can be capped tighter than a route.
type Limits struct {
	Total    int
	PerRoute int
	PerIP    int
}

// RejectReason explains why Admit refused a handshake. It doubles as the value
// of the `result` metric label, so it is a small closed set of short tokens.
type RejectReason string

const (
	RejectDraining    RejectReason = "draining"
	RejectTotalLimit  RejectReason = "total_limit"
	RejectRouteLimit  RejectReason = "route_limit"
	RejectClientLimit RejectReason = "client_limit"
)

// RejectedError is returned by Admit. Callers translate Reason into a status
// code and a metric label rather than matching on the message.
type RejectedError struct {
	Reason RejectReason
}

func (e RejectedError) Error() string {
	switch e.Reason {
	case RejectDraining:
		return "gateway is draining; not accepting new websocket connections"
	case RejectTotalLimit:
		return "websocket connection limit reached"
	case RejectRouteLimit:
		return "websocket connection limit reached for this route"
	case RejectClientLimit:
		return "websocket connection limit reached for this client"
	default:
		return "websocket connection rejected"
	}
}

// Reason extracts the RejectReason from an Admit error, or "" if err is not a
// rejection.
func Reason(err error) RejectReason {
	var rejected RejectedError
	if errors.As(err, &rejected) {
		return rejected.Reason
	}
	return ""
}

// Registry tracks live tunnels so the process can enforce connection caps,
// report what is currently connected (per route and per instance, which is what
// an instance teardown needs to wait on), and close everything that is left at
// the end of a drain.
//
// A slot is reserved BEFORE the handshake is proxied and attached to its
// connection at hijack time. Reserving early is what makes the cap meaningful:
// admitting first and counting later would let an unbounded number of
// in-flight handshakes blow past the limit together.
type Registry struct {
	mu       sync.Mutex
	draining bool
	seq      int64
	slots    map[int64]*Slot
	byRoute  map[string]int
	byKey    map[Key]int
	byIP     map[string]int
}

func NewRegistry() *Registry {
	return &Registry{
		slots:   make(map[int64]*Slot),
		byRoute: make(map[string]int),
		byKey:   make(map[Key]int),
		byIP:    make(map[string]int),
	}
}

// Slot is one reserved tunnel. Release must be called exactly once, whether or
// not the handshake ever completed — a handshake that fails still holds its
// reservation until the proxy call returns.
type Slot struct {
	reg     *Registry
	id      int64
	key     Key
	ip      string
	started time.Time

	conn     net.Conn // guarded by reg.mu
	forced   atomic.Bool
	released atomic.Bool
}

// Admit reserves a slot for a handshake, or returns a RejectedError.
func (r *Registry) Admit(key Key, ip string, limits Limits) (*Slot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.draining {
		return nil, RejectedError{Reason: RejectDraining}
	}
	if limits.Total > 0 && len(r.slots) >= limits.Total {
		return nil, RejectedError{Reason: RejectTotalLimit}
	}
	if limits.PerRoute > 0 && r.byRoute[key.RouteKey] >= limits.PerRoute {
		return nil, RejectedError{Reason: RejectRouteLimit}
	}
	if limits.PerIP > 0 && ip != "" && r.byIP[ip] >= limits.PerIP {
		return nil, RejectedError{Reason: RejectClientLimit}
	}
	r.seq++
	slot := &Slot{reg: r, id: r.seq, key: key, ip: ip, started: time.Now()}
	r.slots[slot.id] = slot
	r.byRoute[key.RouteKey]++
	r.byKey[key]++
	if ip != "" {
		r.byIP[ip]++
	}
	return slot, nil
}

// Attach records the tunnel's raw connection so a drain can close it. Calling
// it is what promotes a reservation into an established tunnel.
func (s *Slot) Attach(conn net.Conn) {
	if s == nil {
		return
	}
	s.reg.mu.Lock()
	defer s.reg.mu.Unlock()
	s.conn = conn
}

// Release frees the reservation. Safe to call twice (the second is a no-op), so
// it can sit in a defer next to explicit cleanup.
func (s *Slot) Release() {
	if s == nil || !s.released.CompareAndSwap(false, true) {
		return
	}
	s.reg.mu.Lock()
	defer s.reg.mu.Unlock()
	delete(s.reg.slots, s.id)
	decr(s.reg.byRoute, s.key.RouteKey)
	if n := s.reg.byKey[s.key] - 1; n <= 0 {
		delete(s.reg.byKey, s.key)
	} else {
		s.reg.byKey[s.key] = n
	}
	if s.ip != "" {
		decr(s.reg.byIP, s.ip)
	}
}

// Forced reports whether this tunnel was closed by a drain rather than by
// either peer.
func (s *Slot) Forced() bool { return s != nil && s.forced.Load() }

// Age is how long the tunnel has been held, measured from admission.
func (s *Slot) Age() time.Duration {
	if s == nil {
		return 0
	}
	return time.Since(s.started)
}

func decr(m map[string]int, key string) {
	if n := m[key] - 1; n <= 0 {
		delete(m, key)
	} else {
		m[key] = n
	}
}

// BeginDrain stops admitting new tunnels. Existing ones keep running: the point
// of a drain is to let them finish, so callers pair this with Drain.
func (r *Registry) BeginDrain() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.draining = true
}

// Draining reports whether new handshakes are being refused.
func (r *Registry) Draining() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.draining
}

// Active is the number of reserved-or-established tunnels.
func (r *Registry) Active() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.slots)
}

// Stat is one row of Stats: how many tunnels a (route, instance) pair holds.
type Stat struct {
	RouteKey string `json:"route_key"`
	Instance string `json:"instance"`
	Active   int    `json:"active"`
}

// Stats returns the per-(route, instance) tunnel counts, sorted for stable
// output. An instance teardown polls this to know when the instance it is
// about to stop has no live tunnels left.
func (r *Registry) Stats() []Stat {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Stat, 0, len(r.byKey))
	for key, n := range r.byKey {
		out = append(out, Stat{RouteKey: key.RouteKey, Instance: key.Instance, Active: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RouteKey != out[j].RouteKey {
			return out[i].RouteKey < out[j].RouteKey
		}
		return out[i].Instance < out[j].Instance
	})
	return out
}

// Drain waits for every live tunnel to end, then force-closes whatever is left
// once ctx expires. It returns the number it had to force. Callers should
// BeginDrain first, otherwise new tunnels keep arriving and the wait can never
// settle.
//
// Force-closing is not optional politeness: http.Server.Shutdown explicitly
// does not wait for or close hijacked connections, so without this a restart
// would either hang on the process supervisor or sever tunnels with no drain
// period at all.
func (r *Registry) Drain(ctx context.Context) int {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if r.Active() == 0 {
			return 0
		}
		select {
		case <-ctx.Done():
			return r.CloseAll()
		case <-ticker.C:
		}
	}
}

// CloseAll force-closes every established tunnel and returns how many it
// closed. Reserved-but-not-yet-established slots have no connection to close;
// they unwind on their own when the handshake finishes.
func (r *Registry) CloseAll() int {
	r.mu.Lock()
	conns := make([]net.Conn, 0, len(r.slots))
	for _, slot := range r.slots {
		if slot.conn != nil {
			slot.forced.Store(true)
			conns = append(conns, slot.conn)
		}
	}
	r.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
	return len(conns)
}

package metrics

import (
	"regexp"
	"strings"
	"sync"
)

// Endpoint normalization turns a raw request path into a bounded-cardinality
// label for per-endpoint metrics. The gateway is a dumb reverse proxy with no
// knowledge of an app's route templates, so a raw path (e.g. /orders/12345)
// would create an unbounded number of Prometheus series. We collapse
// ID-looking segments to ":id", cap path depth, and cap the total number of
// distinct endpoints — three independent guards so a pathological caller can't
// blow up cardinality.
const (
	// defaultMaxDepth keeps at most this many leading path segments; deeper
	// paths have their tail folded into "/*".
	defaultMaxDepth = 6
	// defaultMaxDistinct caps how many distinct normalized endpoints a single
	// gateway process will emit; once reached, further new endpoints collapse
	// to overflowEndpoint. A backstop for whatever the per-segment rules miss.
	defaultMaxDistinct = 200

	overflowEndpoint = "/__other__"
	idPlaceholder    = ":id"
)

var (
	// uuidRe matches a canonical 8-4-4-4-12 UUID.
	uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	// longHexRe matches a run of >=16 hex chars (object ids, sha/hash tokens).
	longHexRe = regexp.MustCompile(`^[0-9a-fA-F]{16,}$`)
)

// EndpointNormalizer normalizes request paths into bounded-cardinality labels.
// It is safe for concurrent use — the gateway calls Normalize from every proxy
// goroutine.
type EndpointNormalizer struct {
	maxDepth    int
	maxDistinct int

	mu   sync.Mutex
	seen map[string]struct{}
}

// DefaultEndpointNormalizer is the process-wide normalizer used by the gateway.
var DefaultEndpointNormalizer = NewEndpointNormalizer(defaultMaxDepth, defaultMaxDistinct)

func NewEndpointNormalizer(maxDepth, maxDistinct int) *EndpointNormalizer {
	return &EndpointNormalizer{
		maxDepth:    maxDepth,
		maxDistinct: maxDistinct,
		seen:        make(map[string]struct{}),
	}
}

// NormalizeEndpoint is a convenience wrapper over DefaultEndpointNormalizer.
func NormalizeEndpoint(path string) string {
	return DefaultEndpointNormalizer.Normalize(path)
}

// Normalize collapses ID-like segments to ":id", folds anything past maxDepth
// into "/*", and returns overflowEndpoint once maxDistinct distinct results
// have already been produced (and this path is a new one).
func (n *EndpointNormalizer) Normalize(path string) string {
	shaped := n.shape(path)
	return n.admit(shaped)
}

// shape does the pure (stateless) part: ID collapsing + depth capping.
func (n *EndpointNormalizer) shape(path string) string {
	if path == "" || path == "/" {
		return "/"
	}
	// Strip a trailing slash so "/a/" and "/a" normalize alike (but keep root).
	trimmed := strings.TrimRight(path, "/")
	if trimmed == "" {
		return "/"
	}
	segments := strings.Split(strings.TrimPrefix(trimmed, "/"), "/")

	truncated := false
	if n.maxDepth > 0 && len(segments) > n.maxDepth {
		segments = segments[:n.maxDepth]
		truncated = true
	}

	for i, seg := range segments {
		if looksLikeID(seg) {
			segments[i] = idPlaceholder
		}
	}

	out := "/" + strings.Join(segments, "/")
	if truncated {
		out += "/*"
	}
	return out
}

// admit enforces the distinct-endpoint cap. Already-seen endpoints always pass;
// a new one passes only if there is still room, otherwise it is bucketed into
// overflowEndpoint (which itself counts as one of the seen entries).
func (n *EndpointNormalizer) admit(endpoint string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.seen[endpoint]; ok {
		return endpoint
	}
	if n.maxDistinct > 0 && len(n.seen) >= n.maxDistinct {
		n.seen[overflowEndpoint] = struct{}{}
		return overflowEndpoint
	}
	n.seen[endpoint] = struct{}{}
	return endpoint
}

// looksLikeID reports whether a single path segment is almost certainly a
// variable identifier (numeric id, UUID, or long hex token) rather than a
// stable route word.
func looksLikeID(seg string) bool {
	if seg == "" {
		return false
	}
	if isAllDigits(seg) {
		return true
	}
	if uuidRe.MatchString(seg) {
		return true
	}
	if longHexRe.MatchString(seg) {
		return true
	}
	return false
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

package metrics

import (
	"strconv"
	"testing"
)

func TestNormalize_Shape(t *testing.T) {
	n := NewEndpointNormalizer(defaultMaxDepth, defaultMaxDistinct)
	cases := []struct{ in, want string }{
		{"", "/"},
		{"/", "/"},
		{"/healthz", "/healthz"},
		{"/api/orders/12345/items", "/api/orders/:id/items"},
		{"/users/550e8400-e29b-41d4-a716-446655440000", "/users/:id"},
		{"/obj/deadbeefdeadbeef", "/obj/:id"}, // 16 hex
		{"/obj/abc123", "/obj/abc123"},        // short mixed → not an id
		{"/a/", "/a"},                         // trailing slash trimmed
		{"/API/Orders/9", "/API/Orders/:id"},  // case preserved on words
	}
	for _, c := range cases {
		if got := n.shape(c.in); got != c.want {
			t.Errorf("shape(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalize_DepthCap(t *testing.T) {
	n := NewEndpointNormalizer(3, defaultMaxDistinct)
	got := n.shape("/a/b/c/d/e")
	if want := "/a/b/c/*"; got != want {
		t.Fatalf("shape depth cap = %q, want %q", got, want)
	}
	// IDs within the kept depth still collapse.
	if got := n.shape("/a/1/c/d"); got != "/a/:id/c/*" {
		t.Fatalf("shape depth+id = %q, want /a/:id/c/*", got)
	}
}

func TestNormalize_DistinctCap(t *testing.T) {
	n := NewEndpointNormalizer(defaultMaxDepth, 3)
	// 3 distinct allowed, all with non-collapsible segments.
	for _, p := range []string{"/one", "/two", "/three"} {
		if got := n.Normalize(p); got != p {
			t.Fatalf("Normalize(%q) = %q, want passthrough", p, got)
		}
	}
	// A 4th distinct overflows.
	if got := n.Normalize("/four"); got != overflowEndpoint {
		t.Fatalf("Normalize overflow = %q, want %q", got, overflowEndpoint)
	}
	// An already-seen one still passes even after the cap is hit.
	if got := n.Normalize("/one"); got != "/one" {
		t.Fatalf("Normalize(seen) = %q, want /one", got)
	}
}

// Cardinality must stay bounded no matter how many distinct raw ID paths arrive.
func TestNormalize_BoundedUnderIDFlood(t *testing.T) {
	n := NewEndpointNormalizer(defaultMaxDepth, defaultMaxDistinct)
	for i := 0; i < 10000; i++ {
		n.Normalize("/orders/" + strconv.Itoa(i))
	}
	// All 10k collapse to the single "/orders/:id" series.
	if got := n.Normalize("/orders/99999999"); got != "/orders/:id" {
		t.Fatalf("expected /orders/:id, got %q", got)
	}
	n.mu.Lock()
	distinct := len(n.seen)
	n.mu.Unlock()
	if distinct > 2 { // "/orders/:id" (+ maybe overflow, but shouldn't trigger)
		t.Fatalf("cardinality not bounded: %d distinct endpoints", distinct)
	}
}

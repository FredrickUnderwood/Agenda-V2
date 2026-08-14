package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/gatewayclient"
)

// GatewayWSDrainStep waits for an instance's established WebSocket tunnels to
// close before the teardown removes its containers.
//
// It exists because draining a route and draining its connections are two
// different things. The gateway_drain step ahead of it re-points the route at
// the surviving instances, which stops NEW handshakes from reaching this one —
// but a WebSocket that was already open is a live TCP connection pinned to this
// instance, and no amount of route rewriting moves it. Running compose down
// straight after the route drain therefore severs every one of those clients
// mid-session; they reconnect (to another instance, correctly), but a
// reconnect storm and lost in-flight session state are precisely what a
// graceful decommission is supposed to avoid.
//
// The step is a bounded wait, not a guarantee: when the budget runs out it
// reports what is still attached and lets the teardown proceed. A decommission
// that blocks forever on one stubborn client is worse than a few cut
// connections, and the app is expected to close its own sockets with 1001
// Going Away once it sees traffic drop — the gateway never speaks WebSocket
// frames, so it cannot send that on the app's behalf.
type GatewayWSDrainStep struct {
	Client       *gatewayclient.Client
	InstanceName string
	// RouteKeys narrows the query to this app's routes; empty means "any route
	// this instance appears on".
	RouteKeys []string
	Timeout   time.Duration
	// PollInterval defaults to 2s.
	PollInterval time.Duration
}

func (s *GatewayWSDrainStep) Execute(ctx context.Context, rc *RunContext) error {
	timeout := s.Timeout
	if timeout <= 0 {
		return nil
	}
	interval := s.PollInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		active, err := s.activeConnections(ctx)
		if err != nil {
			// A gateway that cannot be queried must not block a decommission:
			// the containers are coming down either way, and the route has
			// already been drained. Report and continue.
			_, _ = fmt.Fprintf(rc.Output, "websocket drain: cannot query gateway (%v); continuing\n", err)
			return nil
		}
		if active == 0 {
			_, _ = fmt.Fprintf(rc.Output, "websocket drain: no tunnels left on instance %q\n", s.InstanceName)
			return nil
		}
		if remaining := time.Until(deadline); remaining <= 0 {
			_, _ = fmt.Fprintf(rc.Output,
				"websocket drain: %d tunnel(s) still open on instance %q after %s; proceeding with teardown\n",
				active, s.InstanceName, timeout)
			return nil
		}
		_, _ = fmt.Fprintf(rc.Output, "websocket drain: waiting for %d tunnel(s) on instance %q\n", active, s.InstanceName)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// activeConnections sums the instance's live tunnels across the app's routes.
// Querying per route rather than instance-only keeps the count scoped to this
// application, since instance names are only unique within an app.
func (s *GatewayWSDrainStep) activeConnections(ctx context.Context) (int, error) {
	if len(s.RouteKeys) == 0 {
		return s.Client.ActiveWebSocketConnections(ctx, "", s.InstanceName)
	}
	total := 0
	for _, routeKey := range s.RouteKeys {
		active, err := s.Client.ActiveWebSocketConnections(ctx, routeKey, s.InstanceName)
		if err != nil {
			return 0, err
		}
		total += active
	}
	return total, nil
}

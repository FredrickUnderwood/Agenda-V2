// Package auth contains the permission points agenda-gateway exposes to user-core.
package auth

const (
	// Access is enforced by user-core middleware before any protected route.
	PermAccess = "access"

	PermRouteRead     = "route.read"     // GET /-/routes
	PermRouteUpdate   = "route.update"   // PUT /-/routes/:routeKey
	PermRouteRollback = "route.rollback" // POST /-/routes/:routeKey/rollback
	PermRouteInvoke   = "route.invoke"   // data-plane request pinning to a specific instance
	PermTLSUpdate     = "tls.update"     // PUT /-/tls (push ACME/DNS credentials)
)

func All() []string {
	return []string{
		PermRouteRead,
		PermRouteUpdate,
		PermRouteRollback,
		PermRouteInvoke,
		PermTLSUpdate,
	}
}

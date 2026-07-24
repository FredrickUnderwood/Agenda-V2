package nodeproxy

import "github.com/FredrickUnderwood/agenda-v2/internal/util"

// ProxyKey is the identifier an instance is registered under in a node's
// reverse-proxy registry, and the path segment in the gateway-facing proxy URL
// (<agent_proxy_base_url>/i/<key>). It MUST be scoped by application: the
// node's registry is a flat map, so a bare instance name ("default") collides
// across applications sharing a machine — whichever app deployed last would
// silently receive the other app's traffic. env is included to match the
// uniqueness of a target (app+env+instance), mirroring targetKey minus the
// port (the port drifts across deploys; this key must stay stable).
//
// The node treats the key as an opaque string, so changing this scheme is a
// control-plane-only concern; registrations and backend URLs are always
// (re)written together by gateway_routes_sync and the proxy resync loop.
func ProxyKey(appName, env, instanceName string) string {
	return util.Slug(appName) + "-" + util.Slug(env) + "-" + util.Slug(instanceName)
}

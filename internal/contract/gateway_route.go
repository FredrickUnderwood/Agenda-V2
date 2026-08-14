// Package contract holds the wire (JSON) types exchanged between the agenda-v2
// control plane and the agenda-gateway data plane. Both the control-plane
// gateway client (internal/gatewayclient) and the gateway's own HTTP handler
// (internal/gateway) marshal/unmarshal these exact types, so the route-sync
// contract is defined once here instead of being duplicated on each side.
package contract

// UpsertRouteRequest is the body of PUT /-/routes/{route_key}.
//
// RouteKey itself travels in the URL path; the gateway overwrites RouteKey from
// the path param, so the control-plane client leaves it empty (hence omitempty).
// Status and InstanceSelectMode are plain strings on the wire — the gateway casts
// them to its typed enums after unmarshaling. TimeoutMs=0 means "use the gateway
// default", so the control plane omits it.
type UpsertRouteRequest struct {
	RouteKey           string         `json:"route_key,omitempty"`
	ApplicationID      int64          `json:"application_id"`
	ServiceName        string         `json:"service_name"`
	Env                string         `json:"env"`
	Host               string         `json:"host"`
	PathPrefix         string         `json:"path_prefix"`
	StripPrefix        bool           `json:"strip_prefix"`
	ReleaseID          string         `json:"release_id"`
	Status             string         `json:"status"`
	InstanceSelectMode string         `json:"instance_select_mode,omitempty"`
	InstanceHeader     string         `json:"instance_header,omitempty"`
	TimeoutMs          int            `json:"timeout_ms,omitempty"`
	Operator           string         `json:"operator"`
	Reason             string         `json:"reason"`
	Backends           []BackendEntry `json:"backends"`

	// UpgradeMode opts a route into protocol upgrades ("websocket"); empty or
	// "none" means Upgrade requests are rejected. The WebsocketXxx fields only
	// apply when UpgradeMode is "websocket":
	//   - WebsocketIdleTimeoutMs: 0 = gateway default, <0 = no idle timeout.
	//   - WebsocketMaxConnections: 0 = unlimited (still under the gateway cap).
	//   - WebsocketAllowedOrigins: comma-separated Origin allowlist, empty = any.
	UpgradeMode             string `json:"upgrade_mode,omitempty"`
	WebsocketIdleTimeoutMs  int    `json:"websocket_idle_timeout_ms,omitempty"`
	WebsocketMaxConnections int    `json:"websocket_max_connections,omitempty"`
	WebsocketAllowedOrigins string `json:"websocket_allowed_origins,omitempty"`
}

// BackendEntry is one backend within an upsert request. Enabled and Healthy are
// pointers so an omitted field ("caller did not specify") is distinguishable from
// an explicit false — the gateway defaults a nil to true. The control plane always
// sends explicit values.
type BackendEntry struct {
	TargetKey    string `json:"target_key"`
	InstanceName string `json:"instance_name,omitempty"`
	URL          string `json:"url"`
	Weight       int    `json:"weight"`
	Enabled      *bool  `json:"enabled"`
	Healthy      *bool  `json:"healthy"`
}

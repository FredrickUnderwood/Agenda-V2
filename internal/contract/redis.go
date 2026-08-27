package contract

// Wire types for agenda-node's read-only Redis relay (POST /v1/redis/command),
// defined here so the control plane and the node share a single definition —
// same arrangement as the SQL relay in db.go.
//
// As there, there is deliberately no Host field: the node connects to its own
// ProxyBackendHost, so a registered Redis must live on the node's machine and
// its port never has to be published to the network.

// NodeRedisDefaultPort is the port a Redis registration defaults to.
const NodeRedisDefaultPort = 6379

// NodeRedisCommandRequest is the body of POST /v1/redis/command.
//
// Command is the raw command line; the node parses and re-validates it rather
// than trusting a pre-split argument list, so the node applies the same guard
// to what it was handed as the control plane applied to what it sent.
type NodeRedisCommandRequest struct {
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`

	// DB is the numeric database index to SELECT before running the command.
	DB int `json:"db"`

	Command   string `json:"command"`
	MaxRows   int    `json:"max_rows"`
	MaxBytes  int    `json:"max_bytes"`
	TimeoutMS int    `json:"timeout_ms"`
}

// NodeRedisCommandResponse is the body of POST /v1/redis/command.
//
// It is the SQL response type rather than one of its own: a Redis reply is
// rendered into the same columns-and-rows shape, so the console grid, the audit
// snapshot and the history viewer all keep working on one result format instead
// of growing a second. The Error convention is the same too — the node
// answering 200 means the command was attempted, and a Redis-side failure
// (WRONGTYPE, NOPERM, wrong password) rides in Error.
type NodeRedisCommandResponse = NodeDBQueryResponse

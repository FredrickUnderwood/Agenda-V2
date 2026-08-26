package contract

// Wire types for agenda-node's read-only SQL relay (POST /v1/db/query),
// defined here so the control plane and the node share a single definition —
// same arrangement as the job and probe contracts in node.go.
//
// There is deliberately no Host field: the node connects to its own
// ProxyBackendHost, so a registered database must live on the node's machine
// and its port never has to be published to the network.

// NodeDBDriverMySQL is the only driver agenda-node implements today.
const NodeDBDriverMySQL = "mysql"

// Node-side limits. The control plane clamps a request to these before
// sending, and the node clamps again on arrival — a node must not depend on
// its caller for its own resource safety.
const (
	NodeDBDefaultMaxRows   = 1000
	NodeDBMaxRows          = 10000
	NodeDBDefaultMaxBytes  = 8 << 20
	NodeDBMaxBytes         = 32 << 20
	NodeDBDefaultTimeoutMS = 15000
	NodeDBMaxTimeoutMS     = 60000
)

// NodeDBQueryRequest is the body of POST /v1/db/query.
type NodeDBQueryRequest struct {
	Driver    string `json:"driver"`
	Port      int    `json:"port"`
	User      string `json:"user"`
	Password  string `json:"password"`
	Database  string `json:"database"`
	SQL       string `json:"sql"`
	MaxRows   int    `json:"max_rows"`
	MaxBytes  int    `json:"max_bytes"`
	TimeoutMS int    `json:"timeout_ms"`
}

// NodeDBColumn describes one result column. Binary is set when at least one
// value in the column was not valid UTF-8 and had to be base64-encoded to
// survive the JSON round trip.
type NodeDBColumn struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Binary bool   `json:"binary"`
}

// NodeDBQueryResponse is the body of POST /v1/db/query.
//
// It follows the same convention as NodeProbeResponse: the node answering 200
// means the query was *attempted*. A failure of the target database itself is
// carried in Error, leaving the control plane to decide what that means — the
// node never judges on the control plane's behalf.
//
// Rows holds one pointer per cell so a SQL NULL can be distinguished from the
// empty string.
type NodeDBQueryResponse struct {
	Columns    []NodeDBColumn `json:"columns"`
	Rows       [][]*string    `json:"rows"`
	RowCount   int            `json:"row_count"`
	Truncated  bool           `json:"truncated"`
	DurationMS int            `json:"duration_ms"`
	Error      string         `json:"error,omitempty"`
}

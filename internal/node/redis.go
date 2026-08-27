package node

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/internal/redisguard"
)

// runLocalRedisCommand opens a connection to <backendHost>:<port> — a Redis on
// this machine, reachable only from here — runs one already-validated
// read-only command, and renders the reply as a result set.
//
// It is the Redis half of runLocalQuery and shares its reasoning: backendHost
// is configurable because under docker-outside-of-docker the published port
// lives on the Docker host rather than in the node's container, and a client is
// built per command and closed afterwards so nothing about one command's
// session can survive into the next.
func runLocalRedisCommand(ctx context.Context, backendHost string, req contract.NodeRedisCommandRequest) (*contract.NodeRedisCommandResponse, error) {
	args, err := redisguard.EnsureReadOnly(req.Command)
	if err != nil {
		return nil, err
	}
	if backendHost == "" {
		backendHost = "127.0.0.1"
	}

	client := redis.NewClient(&redis.Options{
		Addr:     net.JoinHostPort(backendHost, strconv.Itoa(req.Port)),
		Username: req.User,
		Password: req.Password,
		// go-redis issues the SELECT itself when the connection is opened, which
		// is why the guard can refuse SELECT outright: the DB index comes from
		// the request, never from the command line.
		DB:           req.DB,
		DialTimeout:  dbDialTimeout,
		ReadTimeout:  time.Duration(req.TimeoutMS) * time.Millisecond,
		WriteTimeout: dbDialTimeout,
		PoolSize:     1,
		// One attempt. A retry would silently spend a second helping of the
		// caller's timeout on a server that has already failed to answer.
		MaxRetries: -1,
		// Pin RESP2. The reply shapes then do not depend on which Redis version
		// answered — RESP3 would return a map for HGETALL and a flat array for
		// the same command on an older server, and the renderer below would
		// have to handle both to produce one stable result.
		Protocol: 2,
	})
	defer client.Close()

	cmdCtx, cancel := context.WithTimeout(ctx, time.Duration(req.TimeoutMS)*time.Millisecond)
	defer cancel()

	started := time.Now()
	reply, err := client.Do(cmdCtx, toAnyArgs(args)...).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	if errors.Is(err, redis.Nil) {
		// A null reply is an answer, not a failure: the key does not exist.
		reply = nil
	}

	resp := renderRedisReply(args, reply, req.MaxRows, req.MaxBytes)
	resp.DurationMS = int(time.Since(started).Milliseconds())
	return resp, nil
}

func toAnyArgs(args []string) []any {
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = a
	}
	return out
}

// redisLeaf is one scalar of a reply, already turned into what a grid cell
// needs: a value (nil for a null reply), whether it had to be base64-encoded to
// survive JSON, and the Redis type it came back as.
type redisLeaf struct {
	value  *string
	binary bool
	kind   string
}

// renderRedisReply turns a reply into the columns-and-rows shape the console
// already knows how to display.
//
// A reply is flattened to its scalar leaves in reply order, which keeps one
// rendering rule for every command instead of a special case per command
// family: a scalar becomes one row, an array becomes one row per element, and a
// nested array (SCAN's cursor-plus-keys, XRANGE's entries) contributes its own
// elements in order. The map-shaped replies are the one exception — see
// pairShaped.
func renderRedisReply(args []string, reply any, maxRows, maxBytes int) *contract.NodeRedisCommandResponse {
	pairs := pairShaped(args)

	// Each row of a pair-shaped reply consumes two leaves, so the leaf budget
	// has to be twice the row budget or the result would be cut in half.
	leafBudget := maxRows
	if pairs {
		leafBudget = maxRows * 2
	}

	leaves, truncated := flattenRedisReply(reply, leafBudget, maxBytes)

	if pairs {
		return pairRows(leaves, truncated)
	}
	if _, isArray := reply.([]any); !isArray && len(leaves) <= 1 {
		return scalarRow(leaves, truncated)
	}
	return indexedRows(leaves, truncated)
}

// pairShaped reports whether a reply is a flat field/value sequence that reads
// far better as two columns than as an alternating single column. Under RESP2
// nothing in the reply itself says so, so it is decided by the command.
func pairShaped(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch strings.ToUpper(args[0]) {
	case "HGETALL":
		return true
	case "CONFIG":
		return len(args) > 1 && strings.EqualFold(args[1], "GET")
	case "XINFO":
		return len(args) > 1 && strings.EqualFold(args[1], "STREAM")
	}
	return false
}

func scalarRow(leaves []redisLeaf, truncated bool) *contract.NodeRedisCommandResponse {
	kind := "nil"
	var row []*string
	var binary bool
	if len(leaves) == 1 {
		kind = leaves[0].kind
		row = []*string{leaves[0].value}
		binary = leaves[0].binary
	} else {
		row = []*string{nil}
	}
	return &contract.NodeRedisCommandResponse{
		Columns:   []contract.NodeDBColumn{{Name: "value", Type: kind, Binary: binary}},
		Rows:      [][]*string{row},
		RowCount:  1,
		Truncated: truncated,
	}
}

func indexedRows(leaves []redisLeaf, truncated bool) *contract.NodeRedisCommandResponse {
	cols := []contract.NodeDBColumn{{Name: "#", Type: "index"}, {Name: "value", Type: "reply"}}
	rows := make([][]*string, 0, len(leaves))
	mixed := false
	for i, leaf := range leaves {
		index := strconv.Itoa(i)
		rows = append(rows, []*string{&index, leaf.value})
		if leaf.binary {
			cols[1].Binary = true
		}
		// The column is only labelled with a Redis type when every element
		// shares one; an array of mixed kinds stays the generic "reply".
		switch {
		case mixed:
		case i == 0:
			cols[1].Type = leaf.kind
		case leaf.kind != cols[1].Type:
			cols[1].Type = "reply"
			mixed = true
		}
	}
	return &contract.NodeRedisCommandResponse{Columns: cols, Rows: rows, RowCount: len(rows), Truncated: truncated}
}

func pairRows(leaves []redisLeaf, truncated bool) *contract.NodeRedisCommandResponse {
	cols := []contract.NodeDBColumn{{Name: "field", Type: "string"}, {Name: "value", Type: "string"}}
	rows := make([][]*string, 0, len(leaves)/2)
	for i := 0; i+1 < len(leaves); i += 2 {
		rows = append(rows, []*string{leaves[i].value, leaves[i+1].value})
		if leaves[i].binary {
			cols[0].Binary = true
		}
		if leaves[i+1].binary {
			cols[1].Binary = true
		}
	}
	// An odd leaf left over means the cap cut the reply mid-pair; the row it
	// would belong to is incomplete, so it is dropped rather than shown with a
	// missing half.
	if len(leaves)%2 == 1 {
		truncated = true
	}
	return &contract.NodeRedisCommandResponse{Columns: cols, Rows: rows, RowCount: len(rows), Truncated: truncated}
}

// flattenRedisReply walks a reply depth-first and returns its scalar leaves,
// stopping as soon as either cap is reached. The caps are applied while walking
// rather than afterwards: a KEYS over a large keyspace must not be materialized
// in full and then trimmed, or the cap protects nothing.
func flattenRedisReply(reply any, maxLeaves, maxBytes int) (leaves []redisLeaf, truncated bool) {
	bytesUsed := 0

	var walk func(v any) bool // returns false once a cap is hit
	walk = func(v any) bool {
		if len(leaves) >= maxLeaves || bytesUsed >= maxBytes {
			return false
		}
		switch typed := v.(type) {
		case nil:
			leaves = append(leaves, redisLeaf{kind: "nil"})
		case []any:
			for _, item := range typed {
				if !walk(item) {
					return false
				}
			}
		case map[any]any:
			// Only reachable if a server negotiates RESP3 despite Protocol: 2;
			// flattened field-then-value so it renders like the RESP2 form.
			for key, value := range typed {
				if !walk(key) || !walk(value) {
					return false
				}
			}
		case string:
			leaves = append(leaves, textLeaf([]byte(typed), "string"))
			bytesUsed += len(typed)
		case []byte:
			leaves = append(leaves, textLeaf(typed, "string"))
			bytesUsed += len(typed)
		case int64:
			leaves = append(leaves, stringLeaf(strconv.FormatInt(typed, 10), "integer"))
		case float64:
			leaves = append(leaves, stringLeaf(strconv.FormatFloat(typed, 'f', -1, 64), "double"))
		case bool:
			leaves = append(leaves, stringLeaf(strconv.FormatBool(typed), "boolean"))
		default:
			// Anything the driver hands back that is not modelled above still
			// has to reach the operator rather than vanish.
			leaves = append(leaves, stringLeaf(fmt.Sprintf("%v", typed), "unknown"))
		}
		return true
	}

	completed := walk(reply)
	return leaves, !completed
}

func textLeaf(raw []byte, kind string) redisLeaf {
	if utf8.Valid(raw) {
		return stringLeaf(string(raw), kind)
	}
	// Non-text bytes cannot ride in JSON as-is; encode them and flag the column
	// so the console can label what it is showing. Same rule as scanRows.
	leaf := stringLeaf(base64.StdEncoding.EncodeToString(raw), kind)
	leaf.binary = true
	return leaf
}

func stringLeaf(v, kind string) redisLeaf {
	value := v
	return redisLeaf{value: &value, kind: kind}
}

// clampRedisCommandRequest applies the node's own defaults and ceilings, for
// the same reason clampDBQueryRequest does: this endpoint is reachable by
// anything holding the node token, so it cannot take its caller's clamping on
// trust.
func clampRedisCommandRequest(req *contract.NodeRedisCommandRequest) error {
	if req.Port <= 0 || req.Port > 65535 {
		return errors.New("port must be a valid TCP port")
	}
	if req.DB < 0 {
		return errors.New("db must be a non-negative database index")
	}
	if req.MaxRows <= 0 {
		req.MaxRows = contract.NodeDBDefaultMaxRows
	}
	if req.MaxRows > contract.NodeDBMaxRows {
		req.MaxRows = contract.NodeDBMaxRows
	}
	if req.MaxBytes <= 0 {
		req.MaxBytes = contract.NodeDBDefaultMaxBytes
	}
	if req.MaxBytes > contract.NodeDBMaxBytes {
		req.MaxBytes = contract.NodeDBMaxBytes
	}
	if req.TimeoutMS <= 0 {
		req.TimeoutMS = contract.NodeDBDefaultTimeoutMS
	}
	if req.TimeoutMS > contract.NodeDBMaxTimeoutMS {
		req.TimeoutMS = contract.NodeDBMaxTimeoutMS
	}
	return nil
}

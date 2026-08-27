package node

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
)

func postRedisCommand(t *testing.T, srv *httptest.Server, token string, req contract.NodeRedisCommandRequest) (int, contract.NodeRedisCommandResponse, string) {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	httpReq, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/redis/command", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if token != "" {
		httpReq.Header.Set(contract.HeaderNodeToken, token)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	var out contract.NodeRedisCommandResponse
	raw := new(bytes.Buffer)
	_, _ = raw.ReadFrom(resp.Body)
	_ = json.Unmarshal(raw.Bytes(), &out)
	return resp.StatusCode, out, raw.String()
}

func TestRedisCommandRequiresTheNodeToken(t *testing.T) {
	srv, _ := newDBTestServer(t)
	status, _, _ := postRedisCommand(t, srv, "", contract.NodeRedisCommandRequest{Port: 6379, Command: "GET k"})
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 — the command endpoint must sit behind the node token", status)
	}
}

// The control plane already ran the guard; the node re-runs it for the same
// reason it re-runs sqlguard.
func TestRedisCommandRejectsAWrite(t *testing.T) {
	srv, token := newDBTestServer(t)
	for _, command := range []string{
		"SET k v",
		"DEL k",
		"FLUSHALL",
		"CONFIG SET maxmemory 1gb",
		"EVAL \"return redis.call('set','k','v')\" 0",
		"SELECT 3",
	} {
		t.Run(command, func(t *testing.T) {
			status, _, body := postRedisCommand(t, srv, token, contract.NodeRedisCommandRequest{
				Port: freePort(t), Command: command,
			})
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", status, body)
			}
		})
	}
}

// Same split as the SQL relay: a Redis that is down is not the node being down.
func TestRedisCommandReportsAnUnreachableServerAs200WithError(t *testing.T) {
	srv, token := newDBTestServer(t)
	status, out, body := postRedisCommand(t, srv, token, contract.NodeRedisCommandRequest{
		Port: freePort(t), Command: "PING", TimeoutMS: 2000,
	})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, body)
	}
	if out.Error == "" {
		t.Fatal("a failed connection must be reported in the response Error, not as a node failure")
	}
}

func TestClampRedisCommandRequest(t *testing.T) {
	req := contract.NodeRedisCommandRequest{Port: 6379}
	if err := clampRedisCommandRequest(&req); err != nil {
		t.Fatalf("clamp: %v", err)
	}
	if req.MaxRows != contract.NodeDBDefaultMaxRows || req.TimeoutMS != contract.NodeDBDefaultTimeoutMS {
		t.Fatalf("defaults not applied: %+v", req)
	}

	over := contract.NodeRedisCommandRequest{Port: 6379, MaxRows: 1 << 20, MaxBytes: 1 << 30, TimeoutMS: 3600000}
	if err := clampRedisCommandRequest(&over); err != nil {
		t.Fatalf("clamp: %v", err)
	}
	if over.MaxRows != contract.NodeDBMaxRows || over.MaxBytes != contract.NodeDBMaxBytes || over.TimeoutMS != contract.NodeDBMaxTimeoutMS {
		t.Fatalf("ceilings not enforced: %+v", over)
	}

	for name, bad := range map[string]contract.NodeRedisCommandRequest{
		"port zero":     {Port: 0},
		"port too high": {Port: 70000},
		"negative db":   {Port: 6379, DB: -1},
	} {
		t.Run(name, func(t *testing.T) {
			if err := clampRedisCommandRequest(&bad); err == nil {
				t.Fatal("expected the request to be refused")
			}
		})
	}
}

func TestRenderRedisReplyScalar(t *testing.T) {
	resp := renderRedisReply([]string{"GET", "k"}, "hello", 1000, 1<<20)
	if resp.RowCount != 1 || len(resp.Columns) != 1 || resp.Columns[0].Name != "value" {
		t.Fatalf("scalar reply rendered as %+v", resp)
	}
	if got := *resp.Rows[0][0]; got != "hello" {
		t.Fatalf("value = %q, want hello", got)
	}

	// An integer reply is still one row, labelled with the Redis type.
	resp = renderRedisReply([]string{"LLEN", "q"}, int64(7), 1000, 1<<20)
	if *resp.Rows[0][0] != "7" || resp.Columns[0].Type != "integer" {
		t.Fatalf("integer reply rendered as %+v", resp)
	}
}

// A missing key is an answer, so it comes back as a NULL cell rather than an
// error — the same distinction the SQL grid draws between NULL and "".
func TestRenderRedisReplyNil(t *testing.T) {
	resp := renderRedisReply([]string{"GET", "missing"}, nil, 1000, 1<<20)
	if resp.RowCount != 1 {
		t.Fatalf("row count = %d, want 1", resp.RowCount)
	}
	if resp.Rows[0][0] != nil {
		t.Fatalf("value = %q, want a NULL cell", *resp.Rows[0][0])
	}
}

func TestRenderRedisReplyArray(t *testing.T) {
	resp := renderRedisReply([]string{"LRANGE", "l", "0", "-1"}, []any{"a", "b", "c"}, 1000, 1<<20)
	if resp.RowCount != 3 {
		t.Fatalf("row count = %d, want 3", resp.RowCount)
	}
	if len(resp.Columns) != 2 || resp.Columns[0].Name != "#" {
		t.Fatalf("array reply should be indexed, got %+v", resp.Columns)
	}
	if *resp.Rows[2][0] != "2" || *resp.Rows[2][1] != "c" {
		t.Fatalf("third row = %v", resp.Rows[2])
	}

	// An empty array is zero rows, not one NULL row.
	if got := renderRedisReply([]string{"KEYS", "nope:*"}, []any{}, 1000, 1<<20); got.RowCount != 0 {
		t.Fatalf("empty array row count = %d, want 0", got.RowCount)
	}
}

// SCAN answers with a cursor and a nested array of keys. Flattening keeps every
// element visible in reply order rather than showing "[...]" for the nesting.
func TestRenderRedisReplyNestedArray(t *testing.T) {
	reply := []any{"17", []any{"user:1", "user:2"}}
	resp := renderRedisReply([]string{"SCAN", "0"}, reply, 1000, 1<<20)
	if resp.RowCount != 3 {
		t.Fatalf("row count = %d, want 3 (cursor + two keys)", resp.RowCount)
	}
	if *resp.Rows[0][1] != "17" || *resp.Rows[1][1] != "user:1" {
		t.Fatalf("rows = %v", resp.Rows)
	}
}

func TestRenderRedisReplyPairShaped(t *testing.T) {
	reply := []any{"name", "ada", "role", "admin"}
	resp := renderRedisReply([]string{"HGETALL", "user:1"}, reply, 1000, 1<<20)
	if resp.RowCount != 2 {
		t.Fatalf("row count = %d, want 2 field/value rows", resp.RowCount)
	}
	if resp.Columns[0].Name != "field" || resp.Columns[1].Name != "value" {
		t.Fatalf("columns = %+v", resp.Columns)
	}
	if *resp.Rows[1][0] != "role" || *resp.Rows[1][1] != "admin" {
		t.Fatalf("second row = %v", resp.Rows[1])
	}

	// The row budget counts rows, so a pair-shaped reply must not lose half of
	// them to the leaf-level cap.
	capped := renderRedisReply([]string{"HGETALL", "user:1"}, reply, 2, 1<<20)
	if capped.RowCount != 2 {
		t.Fatalf("row count = %d under a 2-row cap, want 2", capped.RowCount)
	}
}

func TestRenderRedisReplyCapsRows(t *testing.T) {
	reply := make([]any, 0, 100)
	for i := 0; i < 100; i++ {
		reply = append(reply, "k")
	}
	resp := renderRedisReply([]string{"KEYS", "*"}, reply, 10, 1<<20)
	if resp.RowCount != 10 {
		t.Fatalf("row count = %d, want it capped at 10", resp.RowCount)
	}
	if !resp.Truncated {
		t.Fatal("a capped reply must say it was truncated")
	}
}

// Bytes that are not valid UTF-8 cannot ride in JSON, so they are base64-encoded
// and the column says so — the same rule the SQL scanner follows.
func TestRenderRedisReplyEncodesBinaryValues(t *testing.T) {
	resp := renderRedisReply([]string{"GET", "blob"}, string([]byte{0xff, 0xfe}), 1000, 1<<20)
	if !resp.Columns[0].Binary {
		t.Fatal("column should be flagged binary")
	}
	if strings.ContainsRune(*resp.Rows[0][0], '�') {
		t.Fatalf("value %q should be base64, not lossy text", *resp.Rows[0][0])
	}
}

// fakeRedis is enough of a RESP2 server to exercise the real client: it speaks
// the handshake go-redis performs (HELLO, then AUTH/SELECT) and answers one
// command with a canned reply. Running the whole path against it is the only
// way to check that the driver, the DB selection and the renderer agree — the
// renderer tests above start from a reply that has already been decoded.
type fakeRedis struct {
	port     int
	mu       sync.Mutex
	received [][]string
}

func (f *fakeRedis) commands() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]string, len(f.received))
	copy(out, f.received)
	return out
}

func startFakeRedis(t *testing.T, reply func(args []string) string) *fakeRedis {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeRedis{port: listener.Addr().(*net.TCPAddr).Port}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go f.serve(conn, reply)
		}
	}()
	return f
}

func (f *fakeRedis) serve(conn net.Conn, reply func(args []string) string) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	for {
		args, err := readRESPCommand(r)
		if err != nil {
			return
		}
		f.mu.Lock()
		f.received = append(f.received, args)
		f.mu.Unlock()

		var out string
		switch strings.ToUpper(args[0]) {
		case "HELLO":
			// Answering with an error is what an older server does, and sends
			// go-redis down its RESP2 path.
			out = "-ERR unknown command 'HELLO'\r\n"
		case "AUTH", "SELECT":
			out = "+OK\r\n"
		default:
			out = reply(args)
		}
		if _, err := conn.Write([]byte(out)); err != nil {
			return
		}
	}
}

func readRESPCommand(r *bufio.Reader) ([]string, error) {
	header, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(header, "*") {
		return nil, errors.New("expected a RESP array, got " + header)
	}
	count, err := strconv.Atoi(strings.TrimSpace(header[1:]))
	if err != nil || count <= 0 {
		return nil, errors.New("bad array header " + header)
	}
	args := make([]string, 0, count)
	for i := 0; i < count; i++ {
		sizeLine, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		size, err := strconv.Atoi(strings.TrimSpace(sizeLine[1:]))
		if err != nil {
			return nil, errors.New("bad bulk header " + sizeLine)
		}
		buf := make([]byte, size+2) // value + CRLF
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:size]))
	}
	return args, nil
}

func bulk(v string) string { return "$" + strconv.Itoa(len(v)) + "\r\n" + v + "\r\n" }

func array(items ...string) string {
	out := "*" + strconv.Itoa(len(items)) + "\r\n"
	for _, item := range items {
		out += bulk(item)
	}
	return out
}

func TestRedisCommandAgainstAServer(t *testing.T) {
	srv, token := newDBTestServer(t)
	fake := startFakeRedis(t, func(args []string) string {
		switch strings.ToUpper(args[0]) {
		case "GET":
			if args[1] == "missing" {
				return "$-1\r\n" // a key that does not exist
			}
			return bulk("hello")
		case "HGETALL":
			return array("name", "ada", "role", "admin")
		case "LLEN":
			return ":7\r\n"
		case "SCAN":
			return "*2\r\n" + bulk("17") + array("user:1", "user:2")
		case "TYPE":
			return "-WRONGTYPE Operation against a key holding the wrong kind of value\r\n"
		}
		return "+OK\r\n"
	})

	run := func(t *testing.T, command string, db int) contract.NodeRedisCommandResponse {
		t.Helper()
		status, out, body := postRedisCommand(t, srv, token, contract.NodeRedisCommandRequest{
			Port: fake.port, DB: db, Command: command, TimeoutMS: 5000,
		})
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", status, body)
		}
		return out
	}

	t.Run("bulk string", func(t *testing.T) {
		out := run(t, "GET greeting", 0)
		if out.Error != "" {
			t.Fatalf("error = %q", out.Error)
		}
		if out.RowCount != 1 || *out.Rows[0][0] != "hello" {
			t.Fatalf("rows = %v", out.Rows)
		}
	})

	t.Run("missing key is a NULL cell", func(t *testing.T) {
		out := run(t, "GET missing", 0)
		if out.Error != "" || out.RowCount != 1 || out.Rows[0][0] != nil {
			t.Fatalf("out = %+v", out)
		}
	})

	t.Run("integer", func(t *testing.T) {
		out := run(t, "LLEN q", 0)
		if *out.Rows[0][0] != "7" {
			t.Fatalf("rows = %v", out.Rows)
		}
	})

	t.Run("hash renders as field/value", func(t *testing.T) {
		out := run(t, "HGETALL user:1", 0)
		if out.RowCount != 2 || *out.Rows[0][0] != "name" || *out.Rows[0][1] != "ada" {
			t.Fatalf("rows = %v", out.Rows)
		}
	})

	t.Run("nested reply is flattened in order", func(t *testing.T) {
		out := run(t, "SCAN 0", 0)
		if out.RowCount != 3 || *out.Rows[0][1] != "17" || *out.Rows[2][1] != "user:2" {
			t.Fatalf("rows = %v", out.Rows)
		}
	})

	// A Redis-side failure is a 200 carrying the error, not a node failure.
	t.Run("redis error", func(t *testing.T) {
		out := run(t, "TYPE k", 0)
		if !strings.Contains(out.Error, "WRONGTYPE") {
			t.Fatalf("error = %q, want the server's own message", out.Error)
		}
	})

	// The DB index comes from the request, which is what lets the guard refuse
	// SELECT outright — so the connection really must issue it.
	t.Run("selects the requested db", func(t *testing.T) {
		run(t, "GET greeting", 3)
		var selected bool
		for _, args := range fake.commands() {
			if strings.EqualFold(args[0], "SELECT") && args[1] == "3" {
				selected = true
			}
		}
		if !selected {
			t.Fatalf("no SELECT 3 was issued; commands were %v", fake.commands())
		}
	})
}

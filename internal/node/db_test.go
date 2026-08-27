package node

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
)

func newDBTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	const token = "node-token"
	s := NewServer(token, NewJobStore(1024, time.Hour), NewProxyRegistry(), "127.0.0.1")
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv, token
}

func postDBQuery(t *testing.T, srv *httptest.Server, token string, req contract.NodeDBQueryRequest) (int, contract.NodeDBQueryResponse, string) {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	httpReq, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/db/query", bytes.NewReader(body))
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

	var out contract.NodeDBQueryResponse
	raw := new(bytes.Buffer)
	_, _ = raw.ReadFrom(resp.Body)
	_ = json.Unmarshal(raw.Bytes(), &out)
	return resp.StatusCode, out, raw.String()
}

// freePort returns a port nothing is listening on, so a connection attempt
// fails immediately and deterministically instead of hanging.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func TestDBQueryRequiresTheNodeToken(t *testing.T) {
	srv, _ := newDBTestServer(t)
	status, _, _ := postDBQuery(t, srv, "", contract.NodeDBQueryRequest{Port: 3306, User: "ro", SQL: "SELECT 1"})
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 — the query endpoint must sit behind the node token", status)
	}
}

// The control plane already ran the guard, but the node re-runs it: this
// endpoint is reachable by anything holding the node token, so a check that
// only happened on the caller's side would not be a check at all.
func TestDBQueryRejectsAWriteStatement(t *testing.T) {
	srv, token := newDBTestServer(t)
	for _, stmt := range []string{
		"DELETE FROM orders",
		"UPDATE orders SET total = 0",
		"SELECT 1; DROP TABLE orders",
		"/*!INSERT INTO orders VALUES (1)*/",
		"SELECT * FROM orders INTO OUTFILE '/tmp/x'",
	} {
		t.Run(stmt, func(t *testing.T) {
			status, _, body := postDBQuery(t, srv, token, contract.NodeDBQueryRequest{
				Port: freePort(t), User: "ro", SQL: stmt,
			})
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", status, body)
			}
		})
	}
}

func TestDBQueryRejectsAnUnsupportedDriver(t *testing.T) {
	srv, token := newDBTestServer(t)
	status, _, body := postDBQuery(t, srv, token, contract.NodeDBQueryRequest{
		Driver: "postgres", Port: 5432, User: "ro", SQL: "SELECT 1",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", status, body)
	}
	if !strings.Contains(body, "postgres") {
		t.Fatalf("body %q should name the unsupported driver", body)
	}
}

// A database that is down is not the node being down. The control plane tells
// them apart by this exact split, so it has to hold: reachable node, 200, with
// the database's own failure in the body.
func TestDBQueryReportsAnUnreachableDatabaseAs200WithError(t *testing.T) {
	srv, token := newDBTestServer(t)
	status, out, body := postDBQuery(t, srv, token, contract.NodeDBQueryRequest{
		Port: freePort(t), User: "ro", SQL: "SELECT 1", TimeoutMS: 2000,
	})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", status, body)
	}
	if out.Error == "" {
		t.Fatal("a failed connection must be reported in the response Error, not as a node failure")
	}
	if len(out.Rows) != 0 {
		t.Fatalf("failed query returned %d rows", len(out.Rows))
	}
}

func TestClampDBQueryRequestAppliesDefaults(t *testing.T) {
	req := contract.NodeDBQueryRequest{Port: 3306, User: "ro"}
	if err := clampDBQueryRequest(&req); err != nil {
		t.Fatalf("clamp: %v", err)
	}
	if req.Driver != contract.NodeDBDriverMySQL {
		t.Fatalf("driver = %q, want the mysql default", req.Driver)
	}
	if req.MaxRows != contract.NodeDBDefaultMaxRows {
		t.Fatalf("max_rows = %d, want %d", req.MaxRows, contract.NodeDBDefaultMaxRows)
	}
	if req.MaxBytes != contract.NodeDBDefaultMaxBytes {
		t.Fatalf("max_bytes = %d, want %d", req.MaxBytes, contract.NodeDBDefaultMaxBytes)
	}
	if req.TimeoutMS != contract.NodeDBDefaultTimeoutMS {
		t.Fatalf("timeout_ms = %d, want %d", req.TimeoutMS, contract.NodeDBDefaultTimeoutMS)
	}
}

// The node clamps even though the control plane already did: its own resource
// safety cannot depend on its caller being well behaved.
func TestClampDBQueryRequestEnforcesCeilings(t *testing.T) {
	req := contract.NodeDBQueryRequest{
		Port: 3306, User: "ro",
		MaxRows: 1 << 20, MaxBytes: 1 << 30, TimeoutMS: 3600000,
	}
	if err := clampDBQueryRequest(&req); err != nil {
		t.Fatalf("clamp: %v", err)
	}
	if req.MaxRows != contract.NodeDBMaxRows {
		t.Fatalf("max_rows = %d, want it capped at %d", req.MaxRows, contract.NodeDBMaxRows)
	}
	if req.MaxBytes != contract.NodeDBMaxBytes {
		t.Fatalf("max_bytes = %d, want it capped at %d", req.MaxBytes, contract.NodeDBMaxBytes)
	}
	if req.TimeoutMS != contract.NodeDBMaxTimeoutMS {
		t.Fatalf("timeout_ms = %d, want it capped at %d", req.TimeoutMS, contract.NodeDBMaxTimeoutMS)
	}
}

func TestClampDBQueryRequestRejectsBadTargets(t *testing.T) {
	cases := map[string]contract.NodeDBQueryRequest{
		"port zero":     {Port: 0, User: "ro"},
		"port too high": {Port: 70000, User: "ro"},
		"no user":       {Port: 3306},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			if err := clampDBQueryRequest(&req); err == nil {
				t.Fatal("expected the request to be refused")
			}
		})
	}
}

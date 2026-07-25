package application

import (
	"net/http"
	"net/http/httptest"
	"testing"

	alog "github.com/FredrickUnderwood/agenda-v2/sdk/go/log"
)

// TestServeProxy_GeneratesTraceIDAndEchoes: a request without a trace id gets
// one minted by the gateway, forwarded to the backend, and echoed on the
// response so the caller can correlate.
func TestServeProxy_GeneratesTraceIDAndEchoes(t *testing.T) {
	var forwarded string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = r.Header.Get(alog.TraceHeader)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	app := newTestApp(t, backend.URL, "trace-gen", "default")
	req := httptest.NewRequest(http.MethodGet, "http://api.example.com/orders", nil)
	rec := httptest.NewRecorder()
	app.ServeProxy(rec, req, "")

	if forwarded == "" {
		t.Fatal("backend received no trace id; gateway should mint one")
	}
	if echo := rec.Header().Get(alog.TraceHeader); echo != forwarded {
		t.Errorf("response trace header %q != forwarded id %q", echo, forwarded)
	}
}

// TestServeProxy_ReusesIncomingTraceID: an upstream-supplied trace id is
// propagated unchanged to the backend and echoed, so a whole call chain shares
// one id.
func TestServeProxy_ReusesIncomingTraceID(t *testing.T) {
	var forwarded string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = r.Header.Get(alog.TraceHeader)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	app := newTestApp(t, backend.URL, "trace-reuse", "default")
	req := httptest.NewRequest(http.MethodGet, "http://api.example.com/orders", nil)
	req.Header.Set(alog.TraceHeader, "caller-trace")
	rec := httptest.NewRecorder()
	app.ServeProxy(rec, req, "")

	if forwarded != "caller-trace" {
		t.Errorf("backend trace id = %q, want caller-trace (reused)", forwarded)
	}
	if echo := rec.Header().Get(alog.TraceHeader); echo != "caller-trace" {
		t.Errorf("response trace header = %q, want caller-trace", echo)
	}
}

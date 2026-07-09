package node

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
)

func TestServer_GetMetrics_RelaysBody(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.Write([]byte("my_counter 42\n"))
	}))
	defer app.Close()
	port := appPort(t, app)

	s := NewServer("tok", NewJobStore(1024, 0), NewProxyRegistry())
	req := httptest.NewRequest(http.MethodGet, "/v1/metrics/myapp/default?"+url.Values{
		contract.NodeMetricsQueryPort: {strconv.Itoa(port)},
	}.Encode(), nil)
	req.Header.Set(contract.HeaderNodeToken, "tok")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "my_counter 42\n" {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("content-type = %q", rec.Header().Get("Content-Type"))
	}
}

func TestServer_GetMetrics_CustomPath(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/custom" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Write([]byte("ok"))
	}))
	defer app.Close()
	port := appPort(t, app)

	s := NewServer("tok", NewJobStore(1024, 0), NewProxyRegistry())
	req := httptest.NewRequest(http.MethodGet, "/v1/metrics/myapp/default?"+url.Values{
		contract.NodeMetricsQueryPort: {strconv.Itoa(port)},
		contract.NodeMetricsQueryPath: {"/custom"},
	}.Encode(), nil)
	req.Header.Set(contract.HeaderNodeToken, "tok")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestServer_GetMetrics_MissingPort(t *testing.T) {
	s := NewServer("tok", NewJobStore(1024, 0), NewProxyRegistry())
	req := httptest.NewRequest(http.MethodGet, "/v1/metrics/myapp/default", nil)
	req.Header.Set(contract.HeaderNodeToken, "tok")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

func TestServer_GetMetrics_InvalidPath(t *testing.T) {
	s := NewServer("tok", NewJobStore(1024, 0), NewProxyRegistry())
	req := httptest.NewRequest(http.MethodGet, "/v1/metrics/myapp/default?"+url.Values{
		contract.NodeMetricsQueryPort: {"9464"},
		contract.NodeMetricsQueryPath: {"no-leading-slash"},
	}.Encode(), nil)
	req.Header.Set(contract.HeaderNodeToken, "tok")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

func TestServer_GetMetrics_AppUnreachable_BadGateway(t *testing.T) {
	s := NewServer("tok", NewJobStore(1024, 0), NewProxyRegistry())
	req := httptest.NewRequest(http.MethodGet, "/v1/metrics/myapp/default?"+url.Values{
		contract.NodeMetricsQueryPort: {"1"}, // nothing listens on port 1
	}.Encode(), nil)
	req.Header.Set(contract.HeaderNodeToken, "tok")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502, body = %s", rec.Code, rec.Body.String())
	}
}

func TestServer_GetMetrics_RequiresToken(t *testing.T) {
	s := NewServer("tok", NewJobStore(1024, 0), NewProxyRegistry())
	req := httptest.NewRequest(http.MethodGet, "/v1/metrics/myapp/default?"+url.Values{
		contract.NodeMetricsQueryPort: {"9464"},
	}.Encode(), nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body = %s", rec.Code, rec.Body.String())
	}
}

func appPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return port
}

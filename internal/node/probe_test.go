package node

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/bytedance/sonic"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
)

func TestServer_Probe_RelaysUpstreamStatus(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer app.Close()
	port := appPort(t, app)

	s := NewServer("tok", NewJobStore(1024, 0), NewProxyRegistry(), "")
	req := httptest.NewRequest(http.MethodGet, "/v1/probe/myapp/default?"+url.Values{
		contract.NodeProbeQueryPort: {strconv.Itoa(port)},
		contract.NodeProbeQueryPath: {"/healthz"},
	}.Encode(), nil)
	req.Header.Set(contract.HeaderNodeToken, "tok")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("node status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp contract.NodeProbeResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.HTTPStatus != http.StatusOK {
		t.Fatalf("upstream status = %d, want 200", resp.HTTPStatus)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected probe error: %s", resp.Error)
	}
}

// A non-2xx upstream is still a *successful* probe: the node reports the real
// status (e.g. 500) so the control plane applies its own expected-status rule.
func TestServer_Probe_NonSuccessStatusRelayedNotAnError(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer app.Close()
	port := appPort(t, app)

	s := NewServer("tok", NewJobStore(1024, 0), NewProxyRegistry(), "")
	req := httptest.NewRequest(http.MethodGet, "/v1/probe/myapp/default?"+url.Values{
		contract.NodeProbeQueryPort: {strconv.Itoa(port)},
	}.Encode(), nil)
	req.Header.Set(contract.HeaderNodeToken, "tok")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("node status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp contract.NodeProbeResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.HTTPStatus != http.StatusInternalServerError {
		t.Fatalf("upstream status = %d, want 500", resp.HTTPStatus)
	}
	if resp.Error != "" {
		t.Fatalf("a reachable non-2xx app must not be reported as a probe error: %s", resp.Error)
	}
}

// An unreachable app yields status 0 with Error set — the node completed the
// probe (HTTP 200 from itself) but the upstream refused the connection.
func TestServer_Probe_AppUnreachable_ReportsError(t *testing.T) {
	s := NewServer("tok", NewJobStore(1024, 0), NewProxyRegistry(), "")
	req := httptest.NewRequest(http.MethodGet, "/v1/probe/myapp/default?"+url.Values{
		contract.NodeProbeQueryPort:      {"1"}, // nothing listens on port 1
		contract.NodeProbeQueryTimeoutMS: {"500"},
	}.Encode(), nil)
	req.Header.Set(contract.HeaderNodeToken, "tok")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("node status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp contract.NodeProbeResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.HTTPStatus != 0 {
		t.Fatalf("upstream status = %d, want 0", resp.HTTPStatus)
	}
	if resp.Error == "" {
		t.Fatal("expected a probe error for an unreachable app")
	}
}

func TestServer_Probe_UsesConfiguredBackendHost(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer app.Close()
	port := appPort(t, app)

	s := NewServer("tok", NewJobStore(1024, 0), NewProxyRegistry(), "localhost")
	req := httptest.NewRequest(http.MethodGet, "/v1/probe/myapp/default?"+url.Values{
		contract.NodeProbeQueryPort: {strconv.Itoa(port)},
	}.Encode(), nil)
	req.Header.Set(contract.HeaderNodeToken, "tok")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("node status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestServer_Probe_MissingPort(t *testing.T) {
	s := NewServer("tok", NewJobStore(1024, 0), NewProxyRegistry(), "")
	req := httptest.NewRequest(http.MethodGet, "/v1/probe/myapp/default", nil)
	req.Header.Set(contract.HeaderNodeToken, "tok")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

func TestServer_Probe_RequiresToken(t *testing.T) {
	s := NewServer("tok", NewJobStore(1024, 0), NewProxyRegistry(), "")
	req := httptest.NewRequest(http.MethodGet, "/v1/probe/myapp/default?"+url.Values{
		contract.NodeProbeQueryPort: {"9464"},
	}.Encode(), nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body = %s", rec.Code, rec.Body.String())
	}
}

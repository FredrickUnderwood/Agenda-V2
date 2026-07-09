package metric

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestNewCounterVec_ExposedByHandler(t *testing.T) {
	c := NewCounterVec(prometheus.CounterOpts{Name: "metric_test_requests_total"}, []string{"outcome"})
	c.WithLabelValues("ok").Inc()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	Handler().ServeHTTP(rr, req)

	body := rr.Body.String()
	if !strings.Contains(body, "metric_test_requests_total") {
		t.Fatalf("expected exposition text to contain the counter name, got: %s", body)
	}
	if !strings.Contains(body, `outcome="ok"`) {
		t.Fatalf("expected exposition text to contain the label, got: %s", body)
	}
}

func TestInit_NoAddr_NoOp(t *testing.T) {
	if err := Init(Config{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestInit_ListenAddr_Serves(t *testing.T) {
	addr := freeAddr(t)
	g := NewGauge(prometheus.GaugeOpts{Name: "metric_test_listen_gauge"})
	g.Set(42)

	if err := Init(Config{ListenAddr: addr}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() {
		if err := Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	}()

	body := getWithRetry(t, "http://"+addr+"/metrics")
	if !strings.Contains(body, "metric_test_listen_gauge 42") {
		t.Fatalf("expected exposition text to contain the gauge value, got: %s", body)
	}
}

func TestInit_CalledTwice_Errors(t *testing.T) {
	addr := freeAddr(t)
	if err := Init(Config{ListenAddr: addr}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() {
		if err := Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	}()

	if err := Init(Config{}); err == nil {
		t.Fatal("expected second Init call to error")
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return addr
}

func getWithRetry(t *testing.T, url string) string {
	t.Helper()
	var lastErr error
	for i := 0; i < 20; i++ {
		resp, err := http.Get(url)
		if err == nil {
			defer resp.Body.Close()
			b, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			return string(b)
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("GET %s: %v", url, lastErr)
	return ""
}

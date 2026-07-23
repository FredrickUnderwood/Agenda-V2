package ginmetric

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/FredrickUnderwood/agenda-v2/sdk/go/metric"
)

// TestMiddleware_ScrapesRouteTemplate drives requests through a gin router with
// the middleware and asserts the exported metrics carry the route *template*
// (not the raw path), by scraping the core registry's exposition output.
func TestMiddleware_ScrapesRouteTemplate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Middleware())
	r.GET("/orders/:id", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	for _, id := range []string{"1", "2", "3"} {
		req := httptest.NewRequest(http.MethodGet, "/orders/"+id, nil)
		r.ServeHTTP(httptest.NewRecorder(), req)
	}
	// An unmatched route (404) must not leak the raw path as a label.
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/nope/42", nil))

	body := scrapeCoreMetrics(t)
	if !strings.Contains(body, `route="/orders/:id"`) {
		t.Errorf("expected route=\"/orders/:id\" series in metrics, got:\n%s", body)
	}
	if strings.Contains(body, `route="/orders/1"`) || strings.Contains(body, `route="/orders/2"`) {
		t.Errorf("raw path leaked as a route label:\n%s", body)
	}
	if !strings.Contains(body, `route="unmatched"`) {
		t.Errorf("expected unmatched-route series, got:\n%s", body)
	}
}

func scrapeCoreMetrics(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(metric.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read scrape body: %v", err)
	}
	return string(raw)
}

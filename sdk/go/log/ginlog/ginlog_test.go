package ginlog

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/FredrickUnderwood/agenda-v2/sdk/go/log"
)

func newRouter(seen *string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Middleware())
	r.GET("/", func(c *gin.Context) {
		*seen = log.TraceIDFromContext(c.Request.Context())
		c.Status(http.StatusOK)
	})
	return r
}

func TestMiddleware_GeneratesAndEchoesTraceID(t *testing.T) {
	var seen string
	w := httptest.NewRecorder()
	newRouter(&seen).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if seen == "" {
		t.Fatal("handler context had no trace id")
	}
	if got := w.Header().Get(log.TraceHeader); got != seen {
		t.Errorf("response header %q != ctx id %q", got, seen)
	}
}

func TestMiddleware_ReusesIncomingTraceID(t *testing.T) {
	var seen string
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(log.TraceHeader, "upstream-id")
	newRouter(&seen).ServeHTTP(w, req)

	if seen != "upstream-id" {
		t.Errorf("ctx trace id = %q, want upstream-id (reused)", seen)
	}
	if got := w.Header().Get(log.TraceHeader); got != "upstream-id" {
		t.Errorf("response header = %q, want upstream-id", got)
	}
}

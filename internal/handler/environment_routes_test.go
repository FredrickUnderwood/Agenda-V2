package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestEnvironmentRoutesCoexist guards the batch matrix route against the
// existing per-environment one: gin's router panics on conflicting registrations,
// and both are needed (the console saves the whole matrix in one call, while the
// single-environment endpoint stays for API clients).
func TestEnvironmentRoutesCoexist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	apps := r.Group("/api/v1/applications")
	apps.GET("/:appID/environments", func(c *gin.Context) { c.String(http.StatusOK, "all") })
	apps.PUT("/:appID/environments", func(c *gin.Context) { c.String(http.StatusOK, "all") })
	apps.GET("/:appID/environments/:env", func(c *gin.Context) { c.String(http.StatusOK, c.Param("env")) })

	for _, tc := range []struct{ path, want string }{
		{"/api/v1/applications/7/environments", "all"},
		{"/api/v1/applications/7/environments/stage", "stage"},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if w.Code != http.StatusOK || w.Body.String() != tc.want {
			t.Fatalf("%s -> %d %q, want 200 %q", tc.path, w.Code, w.Body.String(), tc.want)
		}
	}
}

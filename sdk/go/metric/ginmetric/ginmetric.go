// Package ginmetric is the gin adapter for sdk/go/metric's HTTP server
// instrumentation. It lives in its own package so that importing the core
// metric package does not pull in gin — only apps that actually use gin (and
// import this package) take on the gin dependency.
package ginmetric

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/FredrickUnderwood/agenda-v2/sdk/go/metric"
)

// Middleware returns a gin middleware that records http_requests_total and
// http_request_duration_seconds for every request, using gin's matched route
// template (c.FullPath(), e.g. "/orders/:id") as the route label — empty for
// unmatched routes, which the core maps to "unmatched". Register it early:
//
//	r := gin.New()
//	r.Use(ginmetric.Middleware())
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		metric.ObserveHTTP(c.FullPath(), c.Request.Method, c.Writer.Status(), time.Since(start))
	}
}

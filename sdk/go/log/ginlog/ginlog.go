// Package ginlog is the gin adapter for sdk/go/log's request trace propagation.
// It lives in its own package so importing the core log package does not pull in
// gin — only apps that actually use gin (and import this package) take on the
// gin dependency, mirroring metric/ginmetric.
package ginlog

import (
	"github.com/gin-gonic/gin"

	"github.com/FredrickUnderwood/agenda-v2/sdk/go/log"
)

// Middleware propagates the agenda trace id for every request. It reuses the
// incoming X-Agenda-Trace-Id header (set by the agenda gateway) or generates one
// when absent (a direct call not routed through the gateway), stores it on the
// request context so log.Info(c.Request.Context(), ...) and friends emit
// trace_id, and echoes it on the response so the caller can correlate. Register
// it early, before any handler that logs:
//
//	r := gin.New()
//	r.Use(ginlog.Middleware())
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := log.TraceIDFromRequest(c.Request)
		if id == "" {
			id = log.NewTraceID()
		}
		if id != "" {
			c.Request = c.Request.WithContext(log.ContextWithTraceID(c.Request.Context(), id))
			c.Header(log.TraceHeader, id)
		}
		c.Next()
	}
}

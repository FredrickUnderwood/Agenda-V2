package handler

import (
	"net/http"
	"strings"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/gateway/service"
	"github.com/gin-gonic/gin"
)

func (s *Server) listRoutes(c *gin.Context) {
	routes, err := s.routes.ListRoutes(c.Request.Context())
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": routes, "total": len(routes)})
}

func (s *Server) getRoute(c *gin.Context) {
	route, err := s.routes.GetRoute(c.Request.Context(), routeKey(c))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": route})
}

func (s *Server) upsertRoute(c *gin.Context) {
	var req contract.UpsertRouteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, domain.NewInvalidParamError("invalid json"))
		return
	}
	if req.Operator == "" {
		req.Operator = operator(c)
	}
	route, err := s.routes.UpsertRoute(c.Request.Context(), routeKey(c), req)
	if err != nil {
		writeError(c, err)
		return
	}
	if err := s.gateway.Refresh(c.Request.Context()); err != nil {
		writeError(c, err)
		return
	}
	logCaller(c, "route upsert requested")
	c.JSON(http.StatusOK, gin.H{"data": route})
}

func (s *Server) rollbackRoute(c *gin.Context) {
	var req service.RollbackRouteRequest
	_ = c.ShouldBindJSON(&req)
	if req.Operator == "" {
		req.Operator = operator(c)
	}
	route, err := s.routes.RollbackRoute(c.Request.Context(), routeKey(c), req)
	if err != nil {
		writeError(c, err)
		return
	}
	if err := s.gateway.Refresh(c.Request.Context()); err != nil {
		writeError(c, err)
		return
	}
	logCaller(c, "route rollback requested")
	c.JSON(http.StatusOK, gin.H{"data": route})
}

func routeKey(c *gin.Context) string {
	return strings.TrimSpace(c.Param("routeKey"))
}

package handler

import (
	"errors"
	"net/http"

	"github.com/FredrickUnderwood/agenda-v2/internal/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func (s *Server) listApplicationRoutes(c *gin.Context) {
	appID, ok := paramInt64(c, "appID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid application ID")
		return
	}
	routes, err := s.appSvc.ListGatewayRoutes(c.Request.Context(), appID)
	if err != nil {
		applicationRouteError(c, err)
		return
	}
	Success(c, gin.H{"data": routes, "total": len(routes)})
}

func (s *Server) deleteApplicationRoute(c *gin.Context) {
	s.changeApplicationRoute(c, true)
}

func (s *Server) disableApplicationRoute(c *gin.Context) {
	s.changeApplicationRoute(c, false)
}

func (s *Server) changeApplicationRoute(c *gin.Context, remove bool) {
	appID, ok := paramInt64(c, "appID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid application ID")
		return
	}
	routeID, ok := paramInt64(c, "routeID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid route ID")
		return
	}
	var err error
	if remove {
		err = s.appRouteApp.Delete(c.Request.Context(), appID, routeID)
	} else {
		err = s.appRouteApp.Disable(c.Request.Context(), appID, routeID)
	}
	if err != nil {
		applicationRouteError(c, err)
		return
	}
	NoContent(c)
}

func applicationRouteError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		FailMessage(c, http.StatusNotFound, "application or route not found")
	case errors.Is(err, service.ErrDeployLocked):
		FailWith(c, http.StatusConflict, err)
	default:
		FailWith(c, http.StatusInternalServerError, err)
	}
}

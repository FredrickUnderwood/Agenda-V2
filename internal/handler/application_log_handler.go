package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func (s *Server) getApplicationInstanceLogs(c *gin.Context) {
	appID, ok := paramInt64(c, "appID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid application ID")
		return
	}
	targetID, ok := paramInt64(c, "targetID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid instance ID")
		return
	}
	tail := 0
	if v := c.Query("tail"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			tail = n
		}
	}
	logs, err := s.appLogSvc.GetInstanceLogs(c.Request.Context(), appID, targetID, c.Query("service"), tail)
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, logs)
}

package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// getLog is a direct debug/inspection endpoint for a pipeline execution
// record. Normal operation goes through /releases/:releaseID — this exists
// for looking at raw step output without knowing which release owns it.
func (s *Server) getLog(c *gin.Context) {
	logID, ok := paramInt64(c, "logID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid log ID")
		return
	}
	log, err := s.logSvc.GetWithSteps(c.Request.Context(), logID)
	if err != nil {
		FailWith(c, http.StatusNotFound, err)
		return
	}
	Success(c, log)
}

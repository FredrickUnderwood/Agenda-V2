package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

func (s *Server) listAlertRules(c *gin.Context) {
	rules, err := s.alertRuleSvc.List(c.Request.Context())
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"data": rules, "total": len(rules)})
}

func (s *Server) createAlertRule(c *gin.Context) {
	var req service.UpsertAlertRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailMessage(c, http.StatusBadRequest, err.Error())
		return
	}
	rule, err := s.alertRuleSvc.Create(c.Request.Context(), req)
	if err != nil {
		FailWith(c, http.StatusBadRequest, err)
		return
	}
	Created(c, rule)
}

func (s *Server) getAlertRule(c *gin.Context) {
	id, ok := paramInt64(c, "id")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid alert rule ID")
		return
	}
	rule, err := s.alertRuleSvc.Get(c.Request.Context(), id)
	if err != nil {
		FailWith(c, http.StatusNotFound, err)
		return
	}
	Success(c, rule)
}

func (s *Server) updateAlertRule(c *gin.Context) {
	id, ok := paramInt64(c, "id")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid alert rule ID")
		return
	}
	var req service.UpsertAlertRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailMessage(c, http.StatusBadRequest, err.Error())
		return
	}
	rule, err := s.alertRuleSvc.Update(c.Request.Context(), id, req)
	if err != nil {
		FailWith(c, http.StatusBadRequest, err)
		return
	}
	Success(c, rule)
}

func (s *Server) deleteAlertRule(c *gin.Context) {
	id, ok := paramInt64(c, "id")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid alert rule ID")
		return
	}
	if err := s.alertRuleSvc.Delete(c.Request.Context(), id); err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	NoContent(c)
}

// testAlertRule evaluates the rule's PromQL immediately, bypassing the
// ticker — a pure preview, matching testAlert's bypass-and-preview UX. It
// does not persist state or send an alert.
func (s *Server) testAlertRule(c *gin.Context) {
	id, ok := paramInt64(c, "id")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid alert rule ID")
		return
	}
	result, firing, err := s.alertRuleSvc.EvaluateNow(c.Request.Context(), id)
	if err != nil {
		Success(c, gin.H{"firing": false, "error": err.Error()})
		return
	}
	Success(c, gin.H{"firing": firing, "result": result})
}

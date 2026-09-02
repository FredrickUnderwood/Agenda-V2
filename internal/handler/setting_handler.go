package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/FredrickUnderwood/agenda-v2/internal/auth"
	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

func (s *Server) listSettings(c *gin.Context) {
	items, err := s.settingSvc.List(c.Request.Context())
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"data": items, "total": len(items)})
}

type upsertSettingBody struct {
	Value    string             `json:"value"`
	Type     domain.SettingType `json:"type"`
	IsSecret bool               `json:"is_secret"`
}

func (s *Server) upsertSetting(c *gin.Context) {
	key := strings.TrimSpace(c.Param("key"))
	if key == "" {
		FailMessage(c, http.StatusBadRequest, "setting key is required")
		return
	}
	var body upsertSettingBody
	if err := c.ShouldBindJSON(&body); err != nil {
		FailMessage(c, http.StatusBadRequest, err.Error())
		return
	}
	setting, err := s.settingSvc.Set(c.Request.Context(), service.SetSettingRequest{
		Key:      key,
		Value:    body.Value,
		Type:     body.Type,
		IsSecret: body.IsSecret,
	}, currentUserID(c))
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	// Never echo a secret value back.
	if setting.IsSecret {
		setting.Value = "***"
	}
	Success(c, setting)
}

func (s *Server) deleteSetting(c *gin.Context) {
	key := strings.TrimSpace(c.Param("key"))
	if key == "" {
		FailMessage(c, http.StatusBadRequest, "setting key is required")
		return
	}
	if err := s.settingSvc.Delete(c.Request.Context(), key); err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	NoContent(c)
}

// currentOperator returns the authenticated caller's username, for the
// "who did this" field on records the platform writes on their behalf. Empty
// when auth is not configured (dev mode) or the caller is a service token —
// callers fall back to their own default rather than inventing a name.
func currentOperator(c *gin.Context) string {
	if id, ok := auth.GetIdentity(c); ok {
		return id.Username
	}
	return ""
}

// currentUserID returns the authenticated user's id, or 0 when built-in auth is
// not yet wired (the identity layer lands in a later milestone).
func currentUserID(c *gin.Context) int64 {
	if v, ok := c.Get("user_id"); ok {
		if id, ok := v.(int64); ok {
			return id
		}
	}
	return 0
}

package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	alertsdk "github.com/FredrickUnderwood/agenda-v2/sdk/go/alert"
)

type sendAlertRequest struct {
	Title    string   `json:"title" binding:"required"`
	Content  string   `json:"content"`
	Level    string   `json:"level"`
	Channels []string `json:"channels,omitempty"`
}

// sendAlert fans out to every enabled channel configured in Settings (or, if
// Channels is non-empty, only the named ones). Never returns a 4xx/5xx for a
// downstream webhook failure — same "ok/error in a 200" shape as
// testMachineConnection — since a partial failure across channels isn't a
// request-level error.
func (s *Server) sendAlert(c *gin.Context) {
	var req sendAlertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailMessage(c, http.StatusBadRequest, err.Error())
		return
	}
	level := alertsdk.Level(req.Level)
	if level == "" {
		level = alertsdk.LevelInfo
	}
	msg := alertsdk.Message{Title: req.Title, Content: req.Content, Level: level}
	results := s.alertSvc.SendToAll(c.Request.Context(), msg, req.Channels...)

	out := make([]gin.H, 0, len(results))
	for _, r := range results {
		item := gin.H{"channel": r.Channel, "type": r.Type, "ok": r.Err == nil}
		if r.Err != nil {
			item["error"] = r.Err.Error()
		}
		out = append(out, item)
	}
	Success(c, gin.H{"results": out})
}

type testAlertRequest struct {
	Type       string `json:"type" binding:"required"`
	WebhookURL string `json:"webhook_url" binding:"required"`
	Secret     string `json:"secret"`
	Title      string `json:"title"`
	Content    string `json:"content"`
}

// testAlert sends one ad-hoc message to a channel supplied directly in the
// request body — not persisted — so an operator can validate a webhook
// before saving it as a Setting.
func (s *Server) testAlert(c *gin.Context) {
	var req testAlertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		FailMessage(c, http.StatusBadRequest, err.Error())
		return
	}
	title := req.Title
	if title == "" {
		title = "agenda test alert"
	}
	content := req.Content
	if content == "" {
		content = "This is a test alert from agenda-v2."
	}
	ch := alertsdk.Channel{
		Type:       alertsdk.ChannelType(req.Type),
		Name:       "test",
		WebhookURL: req.WebhookURL,
		Secret:     req.Secret,
		Enabled:    true,
	}
	if err := alertsdk.Send(c.Request.Context(), ch, alertsdk.Message{Title: title, Content: content, Level: alertsdk.LevelInfo}); err != nil {
		Success(c, gin.H{"ok": false, "error": err.Error()})
		return
	}
	Success(c, gin.H{"ok": true})
}

package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// listNotifications returns the in-app inbox, newest first. Available to any
// authenticated user (not admin-gated) — unlike Settings/Alerts, an inbox of
// past alerts is normal for any team member to see, not a secrets surface.
func (s *Server) listNotifications(c *gin.Context) {
	unreadOnly := c.Query("unread_only") == "true"
	limit := queryInt(c, "limit", 50)
	offset := queryInt(c, "offset", 0)

	items, err := s.notificationSvc.List(c.Request.Context(), unreadOnly, limit, offset)
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"data": items, "total": len(items)})
}

func (s *Server) getNotificationUnreadCount(c *gin.Context) {
	count, err := s.notificationSvc.CountUnread(c.Request.Context())
	if err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"count": count})
}

func (s *Server) markNotificationRead(c *gin.Context) {
	id, ok := paramInt64(c, "notificationID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid notification ID")
		return
	}
	if err := s.notificationSvc.MarkRead(c.Request.Context(), id); err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"ok": true})
}

func (s *Server) markAllNotificationsRead(c *gin.Context) {
	if err := s.notificationSvc.MarkAllRead(c.Request.Context()); err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	Success(c, gin.H{"ok": true})
}

func (s *Server) deleteNotification(c *gin.Context) {
	id, ok := paramInt64(c, "notificationID")
	if !ok {
		FailMessage(c, http.StatusBadRequest, "invalid notification ID")
		return
	}
	if err := s.notificationSvc.Delete(c.Request.Context(), id); err != nil {
		FailWith(c, http.StatusInternalServerError, err)
		return
	}
	NoContent(c)
}

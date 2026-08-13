package handler

import (
	"strconv"
	"strings"

	"sec_monitor/internal/service"

	"github.com/gin-gonic/gin"
)

func (h *AppHandler) ListInAppNotifications(c *gin.Context) {
	if h.InAppNotifications == nil {
		Error(c, service.ErrNotFound)
		return
	}
	page, pageSize := pageParams(c)
	result, err := h.InAppNotifications.List(c.Request.Context(), service.InAppNotificationFilter{
		UnreadOnly: strings.EqualFold(c.Query("unread_only"), "true") || c.Query("unread_only") == "1",
		Page:       page, PageSize: pageSize,
	})
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) GetInAppNotificationUnreadCount(c *gin.Context) {
	if h.InAppNotifications == nil {
		Error(c, service.ErrNotFound)
		return
	}
	count, err := h.InAppNotifications.UnreadCount(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, gin.H{"unread_count": count})
}

func (h *AppHandler) MarkInAppNotificationRead(c *gin.Context) {
	if h.InAppNotifications == nil {
		Error(c, service.ErrNotFound)
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		Error(c, service.ErrValidation)
		return
	}
	changed, err := h.InAppNotifications.MarkRead(c.Request.Context(), uint(id))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, gin.H{"changed": changed})
}

func (h *AppHandler) MarkAllInAppNotificationsRead(c *gin.Context) {
	if h.InAppNotifications == nil {
		Error(c, service.ErrNotFound)
		return
	}
	changed, err := h.InAppNotifications.MarkAllRead(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, gin.H{"changed": changed})
}

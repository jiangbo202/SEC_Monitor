package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"sec_monitor/internal/sec"
	"sec_monitor/internal/service"
	"sec_monitor/internal/telegram"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type AppHandler struct {
	DB           *gorm.DB
	Targets      *service.WatchTargetService
	Configs      *service.ConfigService
	Tasks        *service.TaskConfigService
	Filings      *service.FilingService
	SEC          sec.Client
	Audit        *service.AuditService
	Notification *service.NotificationService
	Scheduler    SchedulerController
}

type SchedulerController interface {
	Reload(ctx context.Context) error
	RunOnce(ctx context.Context) error
}

func (h *AppHandler) LookupTicker(c *gin.Context) {
	ticker := strings.ToUpper(strings.TrimSpace(c.Param("ticker")))
	if ticker == "" {
		Error(c, service.ErrValidation)
		return
	}
	cik, companyName, err := h.SEC.LookupCIK(c.Request.Context(), ticker)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, gin.H{
		"ticker":       ticker,
		"cik":          cik,
		"company_name": companyName,
		"target_type":  "stock",
	})
}

func (h *AppHandler) ListWatchTargets(c *gin.Context) {
	page, pageSize := pageParams(c)
	result, err := h.Targets.List(c.Request.Context(), service.WatchTargetFilter{
		Ticker:     c.Query("ticker"),
		Status:     c.Query("status"),
		TargetType: c.Query("target_type"),
		Page:       page,
		PageSize:   pageSize,
	})
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) CreateWatchTarget(c *gin.Context) {
	var input service.WatchTargetInput
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, err)
		return
	}
	target, err := h.Targets.Create(c.Request.Context(), input, operator(c))
	if err != nil {
		Error(c, err)
		return
	}
	Created(c, target)
}

func (h *AppHandler) GetWatchTarget(c *gin.Context) {
	target, err := h.Targets.Get(c.Request.Context(), uintParam(c, "id"))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, target)
}

func (h *AppHandler) UpdateWatchTarget(c *gin.Context) {
	var input service.WatchTargetInput
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, err)
		return
	}
	target, err := h.Targets.Update(c.Request.Context(), uintParam(c, "id"), input, operator(c))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, target)
}

func (h *AppHandler) DeleteWatchTarget(c *gin.Context) {
	if err := h.Targets.Delete(c.Request.Context(), uintParam(c, "id"), operator(c)); err != nil {
		Error(c, err)
		return
	}
	NoContent(c)
}

func (h *AppHandler) SetWatchTargetStatus(c *gin.Context) {
	var input struct {
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, err)
		return
	}
	target, err := h.Targets.SetStatus(c.Request.Context(), uintParam(c, "id"), input.Status, operator(c))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, target)
}

func (h *AppHandler) SyncWatchTarget(c *gin.Context) {
	result, err := h.Filings.RefreshTarget(c.Request.Context(), uintParam(c, "id"))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ListWatchTargetSyncDetails(c *gin.Context) {
	details, err := h.Filings.ListTargetSyncDetails(c.Request.Context(), uintParam(c, "id"), 3)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, details)
}

func (h *AppHandler) ListFilings(c *gin.Context) {
	page, pageSize := pageParams(c)
	filter := service.FilingFilter{
		Ticker:             c.Query("ticker"),
		CompanyName:        c.Query("company_name"),
		FilingType:         c.Query("filing_type"),
		NotificationStatus: c.Query("notification_status"),
		SortBy:             c.Query("sort_by"),
		SortOrder:          c.Query("sort_order"),
		Page:               page,
		PageSize:           pageSize,
	}
	if value := c.Query("date_from"); value != "" {
		if t, err := time.Parse("2006-01-02", value); err == nil {
			filter.DateFrom = &t
		}
	}
	if value := c.Query("date_to"); value != "" {
		if t, err := time.Parse("2006-01-02", value); err == nil {
			filter.DateTo = &t
		}
	}
	result, err := h.Filings.List(c.Request.Context(), filter)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) GetFiling(c *gin.Context) {
	filing, err := h.Filings.Get(c.Request.Context(), uintParam(c, "id"))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, filing)
}

func (h *AppHandler) RefreshFilings(c *gin.Context) {
	result, err := h.Filings.Refresh(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ListSyncRuns(c *gin.Context) {
	page, pageSize := pageParams(c)
	result, err := h.Filings.ListSyncRuns(c.Request.Context(), service.SyncRunFilter{
		Status:   c.Query("status"),
		Trigger:  c.Query("trigger"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ListSyncRunDetails(c *gin.Context) {
	details, err := h.Filings.ListSyncRunDetails(c.Request.Context(), uintParam(c, "id"))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, details)
}

func (h *AppHandler) PreviewFilingCleanup(c *gin.Context) {
	days, err := h.retentionDays(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	preview, err := h.Filings.CleanupPreview(c.Request.Context(), days, time.Now().UTC())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, preview)
}

func (h *AppHandler) CleanupFilings(c *gin.Context) {
	days, err := h.retentionDays(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	deleted, err := h.Filings.Cleanup(c.Request.Context(), days, time.Now().UTC())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, gin.H{"deleted": deleted})
}

func (h *AppHandler) ListSystemConfigs(c *gin.Context) {
	configs, err := h.Configs.List(c.Request.Context(), c.Query("category"), true)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, configs)
}

func (h *AppHandler) UpdateSystemConfigs(c *gin.Context) {
	var input []service.ConfigInput
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, err)
		return
	}
	if err := h.Configs.UpsertMany(c.Request.Context(), input, operator(c)); err != nil {
		Error(c, err)
		return
	}
	configs, err := h.Configs.List(c.Request.Context(), "", true)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, configs)
}

func (h *AppHandler) GetTelegramConfig(c *gin.Context) {
	configs, err := h.Configs.List(c.Request.Context(), "telegram", true)
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, configs)
}

func (h *AppHandler) UpdateTelegramConfig(c *gin.Context) {
	var input struct {
		BotToken string `json:"bot_token"`
		ChatID   string `json:"chat_id"`
		Enabled  bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, err)
		return
	}
	configs := []service.ConfigInput{
		{Key: "telegram.chat_id", Value: input.ChatID, ValueType: "string", Category: "telegram"},
		{Key: "telegram.enabled", Value: strconv.FormatBool(input.Enabled), ValueType: "bool", Category: "telegram"},
	}
	if !service.IsMaskedSecret(input.BotToken) {
		configs = append(configs, service.ConfigInput{Key: "telegram.bot_token", Value: input.BotToken, ValueType: "string", Category: "telegram", Encrypted: true})
	}
	err := h.Configs.UpsertMany(c.Request.Context(), configs, operator(c))
	if err != nil {
		Error(c, err)
		return
	}
	h.GetTelegramConfig(c)
}

func (h *AppHandler) TestTelegram(c *gin.Context) {
	cfg, err := h.Configs.Telegram(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	if service.IsMaskedSecret(cfg.BotToken) {
		Error(c, fmt.Errorf("%w: Bot Token 已被脱敏值覆盖，请重新输入真实 Token 并保存", service.ErrValidation))
		return
	}
	err = telegram.NewHTTPNotifier(cfg.BotToken, cfg.ChatID, 10*time.Second).Send(c.Request.Context(), telegram.Message{Text: "SEC Monitor test message"})
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, gin.H{"sent": true})
}

func (h *AppHandler) ListOperationLogs(c *gin.Context) {
	page, pageSize := pageParams(c)
	result, err := h.Audit.List(c.Request.Context(), service.AuditLogFilter{
		Action:     c.Query("action"),
		ObjectType: c.Query("object_type"),
		Page:       page,
		PageSize:   pageSize,
	})
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ListNotificationLogs(c *gin.Context) {
	page, pageSize := pageParams(c)
	result, err := h.Notification.List(c.Request.Context(), service.NotificationLogFilter{
		Status:   c.Query("status"),
		Channel:  c.Query("channel"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ListTaskConfigs(c *gin.Context) {
	tasks, err := h.Tasks.List(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, tasks)
}

func (h *AppHandler) UpdateTaskConfig(c *gin.Context) {
	id := uintParam(c, "id")
	var input service.TaskConfigInput
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, err)
		return
	}
	task, err := h.Tasks.Update(c.Request.Context(), id, input, operator(c))
	if err != nil {
		Error(c, err)
		return
	}
	if h.Scheduler != nil {
		if err := h.Scheduler.Reload(c.Request.Context()); err != nil {
			Error(c, err)
			return
		}
	}
	OK(c, task)
}

func (h *AppHandler) RunTask(c *gin.Context) {
	if h.Scheduler != nil {
		if err := h.Scheduler.RunOnce(context.Background()); err != nil {
			Error(c, err)
			return
		}
		OK(c, gin.H{"started": true})
		return
	}
	result, err := h.Filings.Refresh(context.Background())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}

func (h *AppHandler) ListHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func pageParams(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	return page, pageSize
}

func uintParam(c *gin.Context, name string) uint {
	value, _ := strconv.ParseUint(c.Param(name), 10, 64)
	return uint(value)
}

func operator(c *gin.Context) string {
	value := c.GetHeader("X-Operator")
	if value == "" {
		return "anonymous"
	}
	return value
}

func (h *AppHandler) retentionDays(ctx context.Context) (int, error) {
	raw, ok, err := h.Configs.GetValue(ctx, "system.data_retention_days")
	if err != nil {
		return 0, err
	}
	if !ok || strings.TrimSpace(raw) == "" {
		return 30, nil
	}
	days, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: system.data_retention_days must be a number", service.ErrValidation)
	}
	return days, nil
}

package handler

import (
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"net/http"
	"sec_monitor/internal/discovery"
	"sec_monitor/internal/service"
)

func (h *AppHandler) GetFutuCredentials(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	view, err := h.APIManagement.Futu.Credentials(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, view)
}
func (h *AppHandler) SaveFutuCredentials(c *gin.Context) {
	var in service.FutuCredentialsInput
	if c.ShouldBindJSON(&in) != nil {
		Error(c, service.ErrValidation)
		return
	}
	if err := h.APIManagement.Futu.SaveCredentials(c.Request.Context(), in); err != nil {
		Error(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	OK(c, gin.H{"saved": true})
}

func (h *AppHandler) GetAPIManagementOverview(c *gin.Context) {
	result, err := h.APIManagement.Overview(c.Request.Context(), c.Query("provider"), c.Query("ticker"), c.Query("failures_only") == "true")
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, result)
}
func (h *AppHandler) UpdateAPIModule(c *gin.Context) {
	var in service.APIModuleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		Error(c, service.ErrValidation)
		return
	}
	if err := h.APIManagement.UpdateModule(c.Request.Context(), c.Param("key"), in, operator(c)); err != nil {
		Error(c, err)
		return
	}
	OK(c, gin.H{"saved": true})
}
func (h *AppHandler) UpdateAPIPriceRoute(c *gin.Context) {
	var in service.APIPriceRouteInput
	if err := c.ShouldBindJSON(&in); err != nil {
		Error(c, service.ErrValidation)
		return
	}
	if err := h.APIManagement.UpdatePriceRoute(c.Request.Context(), in, operator(c)); err != nil {
		Error(c, err)
		return
	}
	OK(c, gin.H{"saved": true})
}
func (h *AppHandler) UpdateAPIProviderPolicy(c *gin.Context) {
	var input discovery.APIProviderPolicy
	if err := c.ShouldBindJSON(&input); err != nil {
		Error(c, service.ErrValidation)
		return
	}
	if err := h.APIManagement.UpdatePolicy(c.Request.Context(), c.Param("provider"), input, operator(c)); err != nil {
		Error(c, err)
		return
	}
	OK(c, gin.H{"saved": true})
}
func (h *AppHandler) UpdateAPICapability(c *gin.Context) {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.Enabled == nil {
		Error(c, service.ErrValidation)
		return
	}
	if err := h.APIManagement.SetCapability(c.Request.Context(), c.Param("key"), *input.Enabled, operator(c)); err != nil {
		Error(c, err)
		return
	}
	OK(c, gin.H{"saved": true})
}
func (h *AppHandler) StartFutuAuthorization(c *gin.Context) {
	url, err := h.APIManagement.Futu.BeginAuthorization(c.Request.Context())
	if err != nil {
		Error(c, fmt.Errorf("Futu 授权: %s", service.SanitizeSensitiveError(err.Error())))
		return
	}
	c.Header("Cache-Control", "no-store")
	OK(c, gin.H{"authorization_url": url, "scope": "quote:read", "callback": "http://127.0.0.1:9090/api/providers/futu/oauth/callback"})
}
func (h *AppHandler) CompleteFutuAuthorization(c *gin.Context) {
	state, code := c.Query("state"), c.Query("code")
	c.Request.URL.RawQuery = ""
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	if err := h.APIManagement.Futu.CompleteAuthorization(c.Request.Context(), state, code); err != nil {
		c.Data(http.StatusBadRequest, "text/html; charset=utf-8", []byte(`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>授权未完成</title><p>富途只读授权未完成：可能已取消、会话过期、权限并非纯 quote:read，或网络请求失败。请重新授权。</p><a href="/api-management">返回数据源管理</a></html>`))
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>只读授权完成</title><p>富途行情只读授权已加密保存。富途仍保持暂停；请返回管理页读取最新状态，再显式启用并试查。</p><a href="/api-management">返回数据源管理</a></html>`))
}
func (h *AppHandler) DisconnectFutu(c *gin.Context) {
	if err := h.APIManagement.Futu.Disconnect(c.Request.Context()); err != nil {
		Error(c, err)
		return
	}
	OK(c, gin.H{"disconnected": true, "cached_history_preserved": true})
}
func (h *AppHandler) RefreshFutuOwnership(c *gin.Context) {
	result, err := h.APIManagement.Futu.RefreshOwnership(c.Request.Context(), c.Param("ticker"))
	if err != nil {
		Error(c, fmt.Errorf("Futu 历史查询: %s", service.SanitizeSensitiveError(err.Error())))
		return
	}
	view, err := discovery.GetTickerInstitutionalHoldingHistory(c.Request.Context(), h.DiscoveryDB, c.Param("ticker"))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, gin.H{"refresh": result, "research": view})
}
func (h *AppHandler) ProbeAPIProvider(c *gin.Context) {
	switch c.Param("provider") {
	case "longbridge":
		result := h.discoverySyncService().ProbeLongbridgeQuote(c.Request.Context())
		OK(c, result)
	case "futu":
		result, err := h.APIManagement.Futu.Probe(c.Request.Context())
		if err != nil {
			Error(c, err)
			return
		}
		OK(c, result)
	default:
		Error(c, errors.Join(service.ErrValidation, errors.New("unsupported provider")))
	}
}

package handler

import (
	"errors"
	"github.com/gin-gonic/gin"
	"net/http"
	"sec_monitor/internal/service"
)

func (h *AppHandler) thesisService() *service.ResearchThesisService {
	return service.NewResearchThesisService(h.DB, h.DiscoveryDB)
}
func (h *AppHandler) GetResearchThesis(c *gin.Context) {
	out, err := h.thesisService().Get(c.Request.Context(), c.Param("ticker"))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, out)
}
func (h *AppHandler) ListDueResearchTheses(c *gin.Context) {
	out, err := h.thesisService().Due(c.Request.Context())
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, out)
}
func (h *AppHandler) ListThesisSources(c *gin.Context) {
	out, err := h.thesisService().Sources(c.Request.Context(), c.Param("ticker"))
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, out)
}
func (h *AppHandler) SaveResearchThesis(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10)
	var in service.ThesisWrite
	if err := c.ShouldBindJSON(&in); err != nil {
		Error(c, service.ErrValidation)
		return
	}
	out, err := h.thesisService().Save(c.Request.Context(), c.Param("ticker"), in)
	if errors.Is(err, service.ErrThesisConflict) {
		c.JSON(http.StatusConflict, gin.H{"code": "version_conflict", "message": err.Error()})
		return
	}
	if err != nil {
		Error(c, err)
		return
	}
	OK(c, out)
}

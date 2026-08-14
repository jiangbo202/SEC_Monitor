package handler

import (
	"errors"
	"net/http"

	"sec_monitor/internal/sec"
	"sec_monitor/internal/service"

	"github.com/gin-gonic/gin"
)

func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
}

func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, gin.H{"code": 0, "message": "ok", "data": data})
}

func Accepted(c *gin.Context, data any) {
	c.JSON(http.StatusAccepted, gin.H{"code": 0, "message": "accepted", "data": data})
}

func NoContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

func Error(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	code := "internal_error"
	if errors.Is(err, service.ErrNotFound) {
		status = http.StatusNotFound
		code = "not_found"
	}
	if errors.Is(err, service.ErrValidation) {
		status = http.StatusBadRequest
		code = "validation_failed"
	}
	if errors.Is(err, service.ErrTaskAlreadyRunning) || errors.Is(err, service.ErrTaskResourceBusy) {
		status = http.StatusConflict
		code = "task_busy"
	}
	message := err.Error()
	if providerMessage := sec.UserMessage(err); providerMessage != "" {
		message = providerMessage
	}
	c.JSON(status, gin.H{"code": code, "message": message})
}

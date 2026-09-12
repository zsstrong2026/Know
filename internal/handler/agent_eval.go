package handler

import (
	stderrors "errors"
	"net/http"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type AgentEvalHandler struct {
	service interfaces.AgentEvalService
}

func NewAgentEvalHandler(service interfaces.AgentEvalService) *AgentEvalHandler {
	return &AgentEvalHandler{service: service}
}

func (h *AgentEvalHandler) TriggerRun(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	creds := types.AgentEvalCredentials{
		Authorization: c.GetHeader("Authorization"),
		XAPIKey:       c.GetHeader("X-API-Key"),
		XTenantID:     c.GetHeader("X-Tenant-ID"),
	}

	run, err := h.service.TriggerRun(ctx, tenantID, c.Param("id"), creds)
	if err != nil {
		var conflict *service.AgentEvalConflictError
		switch {
		case stderrors.As(err, &conflict):
			c.JSON(http.StatusConflict, gin.H{
				"success": false,
				"message": conflict.Error(),
				"run_id":  conflict.RunID,
			})
		case stderrors.Is(err, service.ErrAgentEvalNotFound):
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "agent not found"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		}
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": run})
}

func (h *AgentEvalHandler) ListRuns(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	runs, err := h.service.ListRuns(ctx, tenantID, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": runs})
}

func (h *AgentEvalHandler) GetRunDetail(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	run, caseResults, err := h.service.GetRunDetail(ctx, tenantID, c.Param("id"), c.Param("run_id"))
	if err != nil {
		if stderrors.Is(err, service.ErrAgentEvalNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "eval run not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"run": run, "cases": caseResults}})
}

func (h *AgentEvalHandler) DeleteRun(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	if err := h.service.DeleteRun(ctx, tenantID, c.Param("id"), c.Param("run_id")); err != nil {
		if stderrors.Is(err, service.ErrAgentEvalNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "eval run not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

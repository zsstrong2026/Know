package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
)

type pmCreateRequirementRequest struct {
	Title       string     `json:"title" binding:"required"`
	Description string     `json:"description"`
	Priority    string     `json:"priority"`
	Source      string     `json:"source"`
	Proposer    string     `json:"proposer"`
	OwnerID     string     `json:"owner_id"`
	AIAnalysis  types.JSON `json:"ai_analysis"`
}

// CreateRequirement POST /api/v1/pm/projects/:id/requirements
func (h *PMHandler) CreateRequirement(c *gin.Context) {
	var req pmCreateRequirementRequest
	if !pmBindJSON(c, &req) {
		return
	}
	requirement, err := h.requirementService.CreateRequirement(
		c.Request.Context(), pmTenantID(c), &types.PMRequirement{
			ProjectID:   c.Param("id"),
			Title:       req.Title,
			Description: req.Description,
			Priority:    req.Priority,
			Source:      req.Source,
			Proposer:    req.Proposer,
			OwnerID:     req.OwnerID,
			AIAnalysis:  req.AIAnalysis,
			CreatedBy:   pmUserID(c),
		},
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMProjectNotFound, "project not found")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": requirement})
}

// ListRequirements GET /api/v1/pm/projects/:id/requirements?status=
func (h *PMHandler) ListRequirements(c *gin.Context) {
	requirements, err := h.requirementService.ListRequirements(
		c.Request.Context(), pmTenantID(c), c.Param("id"), c.Query("status"),
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMProjectNotFound, "project not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": requirements})
}

// GetRequirement GET /api/v1/pm/requirements/:id
func (h *PMHandler) GetRequirement(c *gin.Context) {
	requirement, err := h.requirementService.GetRequirement(
		c.Request.Context(), pmTenantID(c), c.Param("id"),
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMRequirementNotFound, "requirement not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": requirement})
}

type pmUpdateRequirementRequest struct {
	// 指针字段区分「未传」与显式赋值；状态走 UpdateStatus 状态机
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Priority    *string `json:"priority"`
	Source      *string `json:"source"`
	Proposer    *string `json:"proposer"`
	OwnerID     *string `json:"owner_id"`
	Status      *string `json:"status"`
	// 分析结果允许在任意状态回写（AI 弹窗在非分析中状态下保存时使用）
	AIAnalysis types.JSON `json:"ai_analysis"`
	DesignPlan *string    `json:"design_plan"`
}

// UpdateRequirement PATCH /api/v1/pm/requirements/:id
func (h *PMHandler) UpdateRequirement(c *gin.Context) {
	var req pmUpdateRequirementRequest
	if !pmBindJSON(c, &req) {
		return
	}
	ctx, tenantID := c.Request.Context(), pmTenantID(c)
	requirement, err := h.requirementService.GetRequirement(ctx, tenantID, c.Param("id"))
	if err != nil {
		pmFail(c, err, apprepo.ErrPMRequirementNotFound, "requirement not found")
		return
	}

	// 服务层要求全量对象，这里按入参逐字段合并当前值
	if req.Title != nil {
		requirement.Title = *req.Title
	}
	if req.Description != nil {
		requirement.Description = *req.Description
	}
	if req.Priority != nil {
		requirement.Priority = *req.Priority
	}
	if req.Source != nil {
		requirement.Source = *req.Source
	}
	if req.Proposer != nil {
		requirement.Proposer = *req.Proposer
	}
	if req.OwnerID != nil {
		requirement.OwnerID = *req.OwnerID
	}
	if req.AIAnalysis != nil {
		requirement.AIAnalysis = req.AIAnalysis
	}
	if req.DesignPlan != nil {
		requirement.DesignPlan = *req.DesignPlan
	}
	if err := h.requirementService.UpdateRequirement(ctx, tenantID, requirement); err != nil {
		pmFail(c, err, apprepo.ErrPMRequirementNotFound, "requirement not found")
		return
	}
	if req.Status != nil && *req.Status != "" && *req.Status != requirement.Status {
		if err := h.requirementService.UpdateStatus(ctx, tenantID, requirement.ID, *req.Status); err != nil {
			pmFail(c, err, apprepo.ErrPMRequirementNotFound, "requirement not found")
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// AnalyzeRequirement POST /api/v1/pm/requirements/:id/analyze
func (h *PMHandler) AnalyzeRequirement(c *gin.Context) {
	sessionID, err := h.requirementService.StartAnalysis(
		c.Request.Context(), pmTenantID(c), c.Param("id"),
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMRequirementNotFound, "requirement not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"session_id": sessionID}})
}

type pmSaveRequirementAnalysisRequest struct {
	AIAnalysis types.JSON `json:"ai_analysis"`
	DesignPlan string     `json:"design_plan"`
}

// SaveRequirementAnalysis POST /api/v1/pm/requirements/:id/analysis
// 对话结束后回写结构化分析结论与具体设计方案（仅分析中状态可写）。
func (h *PMHandler) SaveRequirementAnalysis(c *gin.Context) {
	var req pmSaveRequirementAnalysisRequest
	if !pmBindJSON(c, &req) {
		return
	}
	if err := h.requirementService.SaveAnalysis(
		c.Request.Context(), pmTenantID(c), c.Param("id"), req.AIAnalysis, req.DesignPlan,
	); err != nil {
		pmFail(c, err, apprepo.ErrPMRequirementNotFound, "requirement not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ReactivateRequirement POST /api/v1/pm/requirements/:id/reactivate
func (h *PMHandler) ReactivateRequirement(c *gin.Context) {
	if err := h.requirementService.Reactivate(
		c.Request.Context(), pmTenantID(c), c.Param("id"),
	); err != nil {
		pmFail(c, err, apprepo.ErrPMRequirementNotFound, "requirement not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DeleteRequirement DELETE /api/v1/pm/requirements/:id
func (h *PMHandler) DeleteRequirement(c *gin.Context) {
	if err := h.requirementService.DeleteRequirement(
		c.Request.Context(), pmTenantID(c), c.Param("id"),
	); err != nil {
		pmFail(c, err, apprepo.ErrPMRequirementNotFound, "requirement not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// VerifyRequirementLaunch POST /api/v1/pm/requirements/:id/launch-verification
func (h *PMHandler) VerifyRequirementLaunch(c *gin.Context) {
	var in types.LaunchVerificationInput
	if !pmBindJSON(c, &in) {
		return
	}
	requirement, err := h.requirementService.VerifyLaunch(
		c.Request.Context(), pmTenantID(c), c.Param("id"), in,
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMRequirementNotFound, "requirement not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": requirement})
}

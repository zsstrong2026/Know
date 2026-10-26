package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
)

// ListFeatures GET /api/v1/pm/projects/:id/features
func (h *PMHandler) ListFeatures(c *gin.Context) {
	nodes, err := h.featureService.ListTree(
		c.Request.Context(), pmTenantID(c), c.Param("id"),
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMProjectNotFound, "project not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": nodes})
}

type pmCreateFeatureRequest struct {
	// 该端点不带项目路径参数，project_id 由请求体传入
	ProjectID     string `json:"project_id" binding:"required"`
	ModuleName    string `json:"module_name" binding:"required"`
	FeatureName   string `json:"feature_name"`
	OperationName string `json:"operation_name"`
	ParentID      string `json:"parent_id"`
	RequirementID string `json:"requirement_id"`
	// VersionID 非空时同时写一条 add 迭代
	VersionID string `json:"version_id"`
}

// CreateFeature POST /api/v1/pm/features
func (h *PMHandler) CreateFeature(c *gin.Context) {
	var req pmCreateFeatureRequest
	if !pmBindJSON(c, &req) {
		return
	}
	feature, err := h.featureService.CreateManual(
		c.Request.Context(), pmTenantID(c), types.CreatePMFeatureInput{
			Feature: &types.PMFeature{
				ProjectID:     req.ProjectID,
				ParentID:      req.ParentID,
				ModuleName:    req.ModuleName,
				FeatureName:   req.FeatureName,
				OperationName: req.OperationName,
				RequirementID: req.RequirementID,
			},
			VersionID: req.VersionID,
		},
	)
	if err != nil {
		pmFail(c, err, nil, "")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": feature})
}

// ListFeatureIterations GET /api/v1/pm/features/:id/iterations
func (h *PMHandler) ListFeatureIterations(c *gin.Context) {
	iterations, err := h.featureService.ListIterations(c.Request.Context(), c.Param("id"))
	if err != nil {
		pmFail(c, err, nil, "")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": iterations})
}

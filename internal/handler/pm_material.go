package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
)

// ListMaterials GET /api/v1/pm/projects/:id/materials?type=
func (h *PMHandler) ListMaterials(c *gin.Context) {
	list, err := h.materialService.ListMaterials(
		c.Request.Context(), pmTenantID(c), c.Param("id"), c.Query("type"),
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMProjectNotFound, "project not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

// GetMaterial GET /api/v1/pm/materials/:id
func (h *PMHandler) GetMaterial(c *gin.Context) {
	m, err := h.materialService.GetMaterial(c.Request.Context(), pmTenantID(c), c.Param("id"))
	if err != nil {
		pmFail(c, err, apprepo.ErrPMMaterialNotFound, "material not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": m})
}

type pmCreateMaterialRequest struct {
	// 该端点不带项目路径参数，project_id 由请求体传入
	ProjectID    string `json:"project_id" binding:"required"`
	Title        string `json:"title" binding:"required"`
	MaterialType string `json:"material_type" binding:"required"`
	VersionID    string `json:"version_id" binding:"required"`
}

// CreateMaterial POST /api/v1/pm/materials
func (h *PMHandler) CreateMaterial(c *gin.Context) {
	var req pmCreateMaterialRequest
	if !pmBindJSON(c, &req) {
		return
	}
	m, err := h.materialService.CreateMaterial(c.Request.Context(), pmTenantID(c), &types.PMMaterial{
		ProjectID:    req.ProjectID,
		Title:        req.Title,
		MaterialType: req.MaterialType,
		VersionID:    req.VersionID,
		CreatedBy:    pmUserID(c),
	})
	if err != nil {
		pmFail(c, err, nil, "")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": m})
}

// GenerateMaterial POST /api/v1/pm/materials/:id/generate
func (h *PMHandler) GenerateMaterial(c *gin.Context) {
	task, err := h.materialService.GenerateMaterial(
		c.Request.Context(), pmTenantID(c), c.Param("id"),
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMMaterialNotFound, "material not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": task})
}

// PublishMaterial POST /api/v1/pm/materials/:id/publish
func (h *PMHandler) PublishMaterial(c *gin.Context) {
	m, err := h.materialService.Publish(c.Request.Context(), pmTenantID(c), c.Param("id"))
	if err != nil {
		pmFail(c, err, apprepo.ErrPMMaterialNotFound, "material not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": m})
}

// UpdateMaterial POST /api/v1/pm/materials/:id/update
func (h *PMHandler) UpdateMaterial(c *gin.Context) {
	task, err := h.materialService.UpdateToLatest(
		c.Request.Context(), pmTenantID(c), c.Param("id"),
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMMaterialNotFound, "material not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": task})
}

// DeleteMaterial DELETE /api/v1/pm/materials/:id
func (h *PMHandler) DeleteMaterial(c *gin.Context) {
	if err := h.materialService.DeleteMaterial(
		c.Request.Context(), pmTenantID(c), c.Param("id"),
	); err != nil {
		pmFail(c, err, apprepo.ErrPMMaterialNotFound, "material not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DownloadMaterial GET /api/v1/pm/materials/:id/download
// 按资料类型导出 docx/pptx，直接以附件形式返回，不落库
func (h *PMHandler) DownloadMaterial(c *gin.Context) {
	file, err := h.materialService.DownloadMaterial(
		c.Request.Context(), pmTenantID(c), c.Param("id"),
	)
	if err != nil {
		pmFail(c, err, apprepo.ErrPMMaterialNotFound, "material not found")
		return
	}
	c.Header("Content-Disposition", exportContentDisposition(file.FileName))
	c.Data(http.StatusOK, file.ContentType, file.Data)
}

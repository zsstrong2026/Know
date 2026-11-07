package handler

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// 标签端点（P4-2）。
//
// 挂在 WorkbenchItemHandler 上而不是新建一个 handler：标签是条目级组织资源，
// 复用同一个 service，也就不必为 6 个端点再加一条 DI 链路与一处参数装配。
// 归属口径与条目完全一致（tenant_id + owner_id），越权一律 2400。

// ListTags GET /api/v1/workbench/tags
func (h *WorkbenchItemHandler) ListTags(c *gin.Context) {
	tags, err := h.service.ListTags(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"tags": tags}})
}

// CreateTag POST /api/v1/workbench/tags
func (h *WorkbenchItemHandler) CreateTag(c *gin.Context) {
	var req types.WorkbenchTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	tag, err := h.service.CreateTag(c.Request.Context(), &req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": tag})
}

// UpdateTag PUT /api/v1/workbench/tags/:id
func (h *WorkbenchItemHandler) UpdateTag(c *gin.Context) {
	var req types.WorkbenchTagRequest
	if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	tag, err := h.service.UpdateTag(c.Request.Context(), c.Param("id"), &req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": tag})
}

// DeleteTag DELETE /api/v1/workbench/tags/:id
func (h *WorkbenchItemHandler) DeleteTag(c *gin.Context) {
	if err := h.service.DeleteTag(c.Request.Context(), c.Param("id")); err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetBacklinks GET /api/v1/workbench/items/:id/backlinks
//
// 反向链接（P4-2「页内提及」）：列出正文里出现过本条目标题的其它条目。
func (h *WorkbenchItemHandler) GetBacklinks(c *gin.Context) {
	links, err := h.service.ListBacklinks(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"backlinks": links}})
}

// ListItemTags GET /api/v1/workbench/items/:id/tags
func (h *WorkbenchItemHandler) ListItemTags(c *gin.Context) {
	tags, err := h.service.ListItemTags(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"tags": tags}})
}

// SetItemTags PUT /api/v1/workbench/items/:id/tags
//
// 覆盖式：请求里的 tag_ids 就是最终集合（空数组 = 清空标签）。
func (h *WorkbenchItemHandler) SetItemTags(c *gin.Context) {
	var req types.WorkbenchItemTagsRequest
	if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	tags, err := h.service.SetItemTags(c.Request.Context(), c.Param("id"), req.TagIDs)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"tags": tags}})
}

package handler

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service/workbench"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// WorkbenchTemplateHandler 文档模板的 HTTP 入口（§9.16）。
//
// 模板是个人资源：owner 由服务端从 ctx 取，路径里没有 owner 参数；
// 他人模板与不存在同样返回 2400，不泄露存在性。
type WorkbenchTemplateHandler struct {
	service *workbench.Service
}

// NewWorkbenchTemplateHandler 构造 handler。
func NewWorkbenchTemplateHandler(service *workbench.Service) *WorkbenchTemplateHandler {
	return &WorkbenchTemplateHandler{service: service}
}

// ListTemplates GET /api/v1/workbench/templates
func (h *WorkbenchTemplateHandler) ListTemplates(c *gin.Context) {
	items, err := h.service.ListTemplates(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"templates": items}})
}

// CreateTemplate POST /api/v1/workbench/templates
// 从当前已保存的文档复制；服务端不接受客户端上传快照。
func (h *WorkbenchTemplateHandler) CreateTemplate(c *gin.Context) {
	var req types.CreateWorkbenchTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	tpl, err := h.service.CreateTemplate(c.Request.Context(), &req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": tpl})
}

// UpdateTemplate PATCH /api/v1/workbench/templates/:id
func (h *WorkbenchTemplateHandler) UpdateTemplate(c *gin.Context) {
	var req types.UpdateWorkbenchTemplateRequest
	// 允许空 body 之外的一切：至少给一个字段，由 Validate 判定。
	if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	tpl, err := h.service.UpdateTemplate(c.Request.Context(), c.Param("id"), &req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": tpl})
}

// UpdateTemplateContent PUT /api/v1/workbench/templates/:id/content
//
// 模板正文是客户端编辑后提交的快照；服务端只做 schema/体积校验后覆盖存储，
// 不解析、不合并 Y.Doc。
func (h *WorkbenchTemplateHandler) UpdateTemplateContent(c *gin.Context) {
	var req types.UpdateWorkbenchTemplateContentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	tpl, err := h.service.UpdateTemplateContent(c.Request.Context(), c.Param("id"), &req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": tpl})
}

// DeleteTemplate DELETE /api/v1/workbench/templates/:id
func (h *WorkbenchTemplateHandler) DeleteTemplate(c *gin.Context) {
	if err := h.service.DeleteTemplate(c.Request.Context(), c.Param("id")); err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetTemplateContent GET /api/v1/workbench/templates/:id/content
func (h *WorkbenchTemplateHandler) GetTemplateContent(c *gin.Context) {
	content, err := h.service.GetTemplateContent(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": content})
}

// fail 统一错误出口，与服务层保持同一套 AppError 语义。
func (h *WorkbenchTemplateHandler) fail(c *gin.Context, err error) {
	if _, ok := apperrors.IsAppError(err); ok {
		c.Error(err)
		return
	}
	c.Error(apperrors.NewInternalServerError("Failed to process workbench template request").
		WithDetails(err.Error()))
}

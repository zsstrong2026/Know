package handler

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service/workbench"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// WorkbenchArchiveHandler 工作台归档的 HTTP 入口。
//
// 归档是“个人私有资源”：source 的归属由服务端从 ctx 取，body 里没有 owner。
// 这里沿用条目 handler 的错误出口，业务错误全部由服务层翻成 AppError。
type WorkbenchArchiveHandler struct {
	service *workbench.Service
}

// NewWorkbenchArchiveHandler 构造 handler。
func NewWorkbenchArchiveHandler(service *workbench.Service) *WorkbenchArchiveHandler {
	return &WorkbenchArchiveHandler{service: service}
}

// GetArchiveState GET /api/v1/workbench/archives/state?source_type=item&source_id=...
func (h *WorkbenchArchiveHandler) GetArchiveState(c *gin.Context) {
	sourceType := c.Query("source_type")
	sourceID := c.Query("source_id")
	if sourceID == "" {
		c.Error(apperrors.NewValidationError("source_id is required"))
		return
	}
	state, err := h.service.GetArchiveState(c.Request.Context(), sourceType, sourceID)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": state})
}

// Archive POST /api/v1/workbench/archives
func (h *WorkbenchArchiveHandler) Archive(c *gin.Context) {
	var req types.WorkbenchArchiveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	state, err := h.service.Archive(c.Request.Context(), &req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": state})
}

// RefreshArchive POST /api/v1/workbench/archives/refresh
//
// 与归档同一条路径，只是强制覆盖：即使版本与标题都没变也重写知识内容。
func (h *WorkbenchArchiveHandler) RefreshArchive(c *gin.Context) {
	var req types.WorkbenchArchiveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	state, err := h.service.RefreshArchive(c.Request.Context(), &req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": state})
}

// DeleteArchive POST /api/v1/workbench/archives/delete
//
// 不用 DELETE + body：代理与网关可能丢弃 body，导致误删全部 part 或删错来源。
func (h *WorkbenchArchiveHandler) DeleteArchive(c *gin.Context) {
	var req types.WorkbenchArchiveDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	state, err := h.service.DeleteArchive(c.Request.Context(), &req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": state})
}

// fail 统一错误出口。
func (h *WorkbenchArchiveHandler) fail(c *gin.Context, err error) {
	// 2404 需要 Retry-After；先设头再交给 ErrorHandler 渲染。
	if appErr, ok := apperrors.IsAppError(err); ok && appErr.Code == apperrors.ErrWorkbenchArchiveBusy {
		c.Header("Retry-After", "2")
	}
	if _, ok := apperrors.IsAppError(err); ok {
		c.Error(err)
		return
	}
	c.Error(apperrors.NewInternalServerError("Failed to process workbench archive request").
		WithDetails(err.Error()))
}

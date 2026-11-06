package handler

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service/workbench"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// WorkbenchFolderHandler 工作台文件夹的 HTTP 入口。
// 文件夹与条目共用同一套 owner 隔离：不存在与越权都是 2400。
type WorkbenchFolderHandler struct {
	service *workbench.Service
}

// NewWorkbenchFolderHandler 构造 handler。
func NewWorkbenchFolderHandler(service *workbench.Service) *WorkbenchFolderHandler {
	return &WorkbenchFolderHandler{service: service}
}

// ListFolders GET /api/v1/workbench/folders
func (h *WorkbenchFolderHandler) ListFolders(c *gin.Context) {
	folders, err := h.service.ListFolders(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"folders": folders}})
}

// CreateFolder POST /api/v1/workbench/folders
func (h *WorkbenchFolderHandler) CreateFolder(c *gin.Context) {
	var req types.WorkbenchFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	folder, err := h.service.CreateFolder(c.Request.Context(), &req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": folder})
}

// UpdateFolder PUT /api/v1/workbench/folders/:id
func (h *WorkbenchFolderHandler) UpdateFolder(c *gin.Context) {
	var req types.WorkbenchFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	folder, err := h.service.UpdateFolder(c.Request.Context(), c.Param("id"), &req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": folder})
}

// MoveFolder PUT /api/v1/workbench/folders/:id/move
//
// 拖拽排序与跨层移动共用这一个入口：parent_id 决定层级，position 决定层内位置。
// 只改 parent_id 与 sort_order，不动名字，也不搬条目。
func (h *WorkbenchFolderHandler) MoveFolder(c *gin.Context) {
	var req types.WorkbenchFolderMoveRequest
	if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	folder, err := h.service.MoveFolder(c.Request.Context(), c.Param("id"), req.ParentID, req.Position)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": folder})
}

// DeleteFolder DELETE /api/v1/workbench/folders/:id
func (h *WorkbenchFolderHandler) DeleteFolder(c *gin.Context) {
	if err := h.service.DeleteFolder(c.Request.Context(), c.Param("id")); err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// fail 统一错误出口，与服务层保持同一套 AppError 语义。
func (h *WorkbenchFolderHandler) fail(c *gin.Context, err error) {
	if _, ok := apperrors.IsAppError(err); ok {
		c.Error(err)
		return
	}
	c.Error(apperrors.NewInternalServerError("Failed to process workbench request").WithDetails(err.Error()))
}

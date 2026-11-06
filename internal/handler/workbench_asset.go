package handler

import (
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service/workbench"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// WorkbenchAssetHandler 工作台条目资产的 HTTP 入口（§12.1）。
//
// 与条目 handler 的差异：
//   - 上传是 multipart，不走 JSON 绑定；
//   - 下载直接写流，不套统一的 {success,data} 包（前端要的是 Blob）；
//   - 错误仍统一交给全局 ErrorHandler 渲染，不手拼状态码。
type WorkbenchAssetHandler struct {
	service *workbench.Service
}

// NewWorkbenchAssetHandler 构造 handler。
func NewWorkbenchAssetHandler(service *workbench.Service) *WorkbenchAssetHandler {
	return &WorkbenchAssetHandler{service: service}
}

// UploadItemAsset POST /api/v1/workbench/items/:id/assets
//
// 限制在服务层做（20MB + 类型白名单 + 单条目数量），这里只负责取文件。
// 用 MaxMultipartMemory 之外的方式读取：文件本体直接交给存储层流式落盘，
// 不在 handler 里整块读进内存。
func (h *WorkbenchAssetHandler) UploadItemAsset(c *gin.Context) {
	header, err := c.FormFile("file")
	if err != nil {
		c.Error(apperrors.NewValidationError("file is required").WithDetails(err.Error()))
		return
	}
	asset, err := h.service.UploadAsset(c.Request.Context(), c.Param("id"), header)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": asset})
}

// ListItemAssets GET /api/v1/workbench/items/:id/assets
func (h *WorkbenchAssetHandler) ListItemAssets(c *gin.Context) {
	assets, err := h.service.ListAssets(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"assets": assets}})
}

// DownloadAsset GET /api/v1/workbench/assets/:asset_id
//
// 只接受 asset id：存储路径由服务端从数据库行重新拼装，前端拿不到也传不进路径。
// 图片 inline（BlockSuite 需要能直接渲染），其余 attachment 并带原始文件名。
func (h *WorkbenchAssetHandler) DownloadAsset(c *gin.Context) {
	asset, reader, err := h.service.OpenAsset(c.Request.Context(), c.Param("asset_id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	defer func() {
		if closeErr := reader.Close(); closeErr != nil {
			logger.Warnf(c.Request.Context(), "workbench asset: close stream failed: %v", closeErr)
		}
	}()

	// nosniff 必须带：否则浏览器可能把 octet-stream 嗅探成 html 并执行。
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Type", workbench.AssetContentTypeForHeader(asset))
	c.Header("Content-Disposition", workbench.AssetDispositionForHeader(asset))
	if asset.SizeBytes > 0 {
		c.Header("Content-Length", strconv.FormatInt(asset.SizeBytes, 10))
	}
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, reader); err != nil {
		// 响应头已发出，无法再改状态码；记日志即可（多为客户端断开）。
		logger.Warnf(c.Request.Context(), "workbench asset: stream %s aborted: %v", asset.ID, err)
	}
}

// DeleteItemAsset DELETE /api/v1/workbench/items/:id/assets/:asset_id
func (h *WorkbenchAssetHandler) DeleteItemAsset(c *gin.Context) {
	if err := h.service.DeleteAsset(c.Request.Context(), c.Param("id"), c.Param("asset_id")); err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// TouchItemAssets POST /api/v1/workbench/items/:id/assets/touch
//
// 前端打开文档后上报实际引用的 asset，服务端据此刷新 last_seen_at。
// 这是“服务端不解析 Y.Doc”前提下的引用追踪手段，失败不影响编辑与保存。
func (h *WorkbenchAssetHandler) TouchItemAssets(c *gin.Context) {
	var req types.WorkbenchAssetTouchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	touched, err := h.service.TouchAssets(c.Request.Context(), c.Param("id"), req.AssetIDs)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"touched": touched}})
}

// fail 与条目 handler 同一套错误出口。
func (h *WorkbenchAssetHandler) fail(c *gin.Context, err error) {
	if _, ok := apperrors.IsAppError(err); ok {
		c.Error(err)
		return
	}
	c.Error(apperrors.NewInternalServerError("Failed to process workbench asset request").
		WithDetails(err.Error()))
}

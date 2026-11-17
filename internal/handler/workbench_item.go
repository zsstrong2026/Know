package handler

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service/workbench"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// WorkbenchItemHandler 工作台条目、内容与版本的 HTTP 入口。
// 归档端点由 WorkbenchArchiveHandler 承担；模板、资产等属后续阶段，不预留空实现。
type WorkbenchItemHandler struct {
	service *workbench.Service
}

// NewWorkbenchItemHandler 构造 handler。
func NewWorkbenchItemHandler(service *workbench.Service) *WorkbenchItemHandler {
	return &WorkbenchItemHandler{service: service}
}

// Bootstrap GET /api/v1/workbench/bootstrap
//
// 首屏聚合：确保工作知识库存在，返回条目/文件夹/归档分片计数与配额上限。
// 前端进入工作台时先调它，再拉列表；失败说明工作库不可用，直接显示空态。
func (h *WorkbenchItemHandler) Bootstrap(c *gin.Context) {
	bootstrap, err := h.service.Bootstrap(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": bootstrap})
}

// ListItems GET /api/v1/workbench/items
// 支持 kind / exclude_kind / folder_id / favorite / trashed / q / journal / sort / cursor / limit。
// owner 由服务端从 ctx 取，路径与 query 都不接受 owner 参数。
func (h *WorkbenchItemHandler) ListItems(c *gin.Context) {
	filter := types.WorkbenchItemFilter{
		Kind:        c.Query("kind"),
		ExcludeKind: c.Query("exclude_kind"),
		Cursor:      c.Query("cursor"),
		Query:       strings.TrimSpace(c.Query("q")),
		Sort:        strings.TrimSpace(c.Query("sort")),
		Trashed:     parseBoolQuery(c, "trashed"),
		Limit:       parseIntQuery(c, "limit"),
	}
	if folderID := c.Query("folder_id"); folderID != "" {
		filter.FolderID = &folderID
	}
	// 按标签过滤（P4-2）：空串表示不筛
	filter.TagID = strings.TrimSpace(c.Query("tag_id"))
	if raw, ok := c.GetQuery("favorite"); ok && raw != "" {
		favorite := raw == "1" || strings.EqualFold(raw, "true")
		filter.Favorite = &favorite
	}
	if parseBoolQuery(c, "journal") {
		journal := true
		filter.Journal = &journal
	}

	page, err := h.service.ListItems(c.Request.Context(), filter)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": page})
}

// CreateItem POST /api/v1/workbench/items
func (h *WorkbenchItemHandler) CreateItem(c *gin.Context) {
	var req types.CreateWorkbenchItemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	item, err := h.service.CreateItem(c.Request.Context(), &req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": item})
}

// GetItem GET /api/v1/workbench/items/:id
func (h *WorkbenchItemHandler) GetItem(c *gin.Context) {
	item, err := h.service.GetItem(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": item})
}

// UpdateItem PUT /api/v1/workbench/items/:id
func (h *WorkbenchItemHandler) UpdateItem(c *gin.Context) {
	var req types.UpdateWorkbenchItemRequest
	if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	item, err := h.service.UpdateItem(c.Request.Context(), c.Param("id"), &req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": item})
}

// DeleteItem DELETE /api/v1/workbench/items/:id?delete_archives=1&purge=1
//
// 两种模式对归档的处理不同：
//   - purge=false（进回收站）：delete_archives=1 只把映射标 deleted，知识保留；
//   - purge=true（永久删除）：delete_archives=1 必须先成功删掉归档知识再硬删条目，
//     顺序反过来一旦归档删除失败，条目已经没了、知识却残留，重试无从下手。
func (h *WorkbenchItemHandler) DeleteItem(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	purge := parseBoolQuery(c, "purge")
	deleteArchives := parseBoolQuery(c, "delete_archives")

	if purge && deleteArchives {
		if _, err := h.service.DeleteArchive(ctx, &types.WorkbenchArchiveDeleteRequest{
			SourceType:      types.WorkbenchSourceItem,
			SourceID:        id,
			DeleteKnowledge: true,
		}); err != nil {
			h.fail(c, err)
			return
		}
	}

	if err := h.service.DeleteItem(ctx, id, purge); err != nil {
		h.fail(c, err)
		return
	}
	if !purge && deleteArchives {
		if _, err := h.service.MarkSourceDeleted(ctx, types.WorkbenchSourceItem, id); err != nil {
			// 条目已进回收站，归档标记失败不回滚；记日志交由对账任务收敛。
			logger.Warnf(ctx, "workbench: failed to mark archives deleted for item %s: %v", id, err)
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// RestoreItem POST /api/v1/workbench/items/:id/restore
func (h *WorkbenchItemHandler) RestoreItem(c *gin.Context) {
	item, err := h.service.RestoreItem(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": item})
}

// EmptyTrash POST /api/v1/workbench/trash/empty
//
// 一键清空：硬删当前用户回收站里的全部条目，已归档知识保持不动。
// 幂等：回收站为空时返回 purged=0。
func (h *WorkbenchItemHandler) EmptyTrash(c *gin.Context) {
	purged, err := h.service.EmptyTrash(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"purged": purged}})
}

// GetContent GET /api/v1/workbench/items/:id/content
func (h *WorkbenchItemHandler) GetContent(c *gin.Context) {
	content, err := h.service.GetContent(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": content})
}

// SaveContent PUT /api/v1/workbench/items/:id/content
//
// body 复用 types.SaveWorkbenchContentRequest（base_version 为乐观锁基线）。
// 版本冲突由服务层映射为 2401，details 带 current_version 供前端重发。
func (h *WorkbenchItemHandler) SaveContent(c *gin.Context) {
	var req types.SaveWorkbenchContentRequest
	if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	result, err := h.service.SaveContent(c.Request.Context(), c.Param("id"), req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// GetThumbnail GET /api/v1/workbench/items/:id/thumbnail
//
// 返回原始图片字节。没有封面时 404，前端回退到骨架封面；
// 图片按版本号更新，这里只做短缓存，避免浏览器缓存住旧图。
func (h *WorkbenchItemHandler) GetThumbnail(c *gin.Context) {
	image, contentType, err := h.service.GetItemThumbnail(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	if len(image) == 0 || contentType == "" {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "private, max-age=300")
	c.Data(http.StatusOK, contentType, image)
}

// SaveThumbnail PUT /api/v1/workbench/items/:id/thumbnail?version=N
//
// body 是原始图片字节（前端已压缩成 JPEG），version 为截取时的 snapshot_version。
// 用原始字节而不是 multipart：单张图很小，少一层解析；服务端仍会嗅探真实类型。
func (h *WorkbenchItemHandler) SaveThumbnail(c *gin.Context) {
	// 多留 64KB 给请求体，超过上限由 MaxBytesReader 直接截断。
	c.Request.Body = http.MaxBytesReader(
		c.Writer, c.Request.Body, types.WorkbenchMaxThumbnailBytes+64*1024)
	image, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.Error(apperrors.NewValidationError("Invalid thumbnail data").WithDetails(err.Error()))
		return
	}
	version, _ := strconv.Atoi(c.Query("version"))
	savedVersion, err := h.service.SaveItemThumbnail(c.Request.Context(), c.Param("id"), image, version)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"thumbnail_version": savedVersion},
	})
}

// ListVersions GET /api/v1/workbench/items/:id/versions?limit=20
func (h *WorkbenchItemHandler) ListVersions(c *gin.Context) {
	versions, err := h.service.ListVersions(
		c.Request.Context(), c.Param("id"), parseIntQuery(c, "limit"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"versions": versions}})
}

// RestoreVersion POST /api/v1/workbench/items/:id/versions/:version/restore
func (h *WorkbenchItemHandler) RestoreVersion(c *gin.Context) {
	version, err := strconv.Atoi(c.Param("version"))
	if err != nil || version <= 0 {
		c.Error(apperrors.NewValidationError("version must be a positive integer"))
		return
	}
	var req struct {
		BaseVersion int `json:"base_version"`
	}
	if bindErr := c.ShouldBindJSON(&req); bindErr != nil && bindErr != io.EOF {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(bindErr.Error()))
		return
	}
	result, err := h.service.RestoreVersion(
		c.Request.Context(), c.Param("id"), version, req.BaseVersion)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// EnableSemanticIndex POST /api/v1/workbench/kb/semantic-index
//
// 只操作当前用户的工作知识库：不接受 kb_id，避免变成任意知识库重建入口（§7.8）。
// 没有可用 Embedding 模型时返回 2406，前端据此提示“先去配置模型”。
func (h *WorkbenchItemHandler) EnableSemanticIndex(c *gin.Context) {
	var req struct {
		ModelID string `json:"model_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	result, err := h.service.EnableSemanticIndex(c.Request.Context(), req.ModelID)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": result})
}

// fail 统一错误出口：服务层已把业务错误翻译成 AppError，
// 这里直接交给全局 ErrorHandler 渲染，避免 handler 再写一套状态码映射。
func (h *WorkbenchItemHandler) fail(c *gin.Context, err error) {
	if _, ok := apperrors.IsAppError(err); ok {
		c.Error(err)
		return
	}
	c.Error(apperrors.NewInternalServerError("Failed to process workbench request").WithDetails(err.Error()))
}

// parseBoolQuery 把 "1"/"true" 视为 true，其余（含缺省）为 false。
func parseBoolQuery(c *gin.Context, name string) bool {
	raw, ok := c.GetQuery(name)
	if !ok || raw == "" {
		return false
	}
	return raw == "1" || strings.EqualFold(raw, "true")
}

// parseIntQuery 解析整型 query；缺省或非法都返回 0，由服务层兜底默认值。
func parseIntQuery(c *gin.Context, name string) int {
	raw := c.Query(name)
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return value
}

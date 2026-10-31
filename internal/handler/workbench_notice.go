package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service/workbench"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// WorkbenchNoticeHandler 用户级工作台通知（§5.7、§7.7）。
//
// 这三个端点必须允许 tenantless：用户被移出最后一个 workspace 后
// 仍要能读到“数据保留 30 天”的提醒，因此挂在 /api/v1/me 下，
// 并把前缀加入 middleware.isTenantOptionalAPI 的放行清单。
type WorkbenchNoticeHandler struct {
	service *workbench.Service
}

// NewWorkbenchNoticeHandler 构造 handler。
func NewWorkbenchNoticeHandler(service *workbench.Service) *WorkbenchNoticeHandler {
	return &WorkbenchNoticeHandler{service: service}
}

// ListMyNotices GET /api/v1/me/workbench-notices?unread_only=1&limit=20
func (h *WorkbenchNoticeHandler) ListMyNotices(c *gin.Context) {
	unreadOnly := c.Query("unread_only") == "1" || c.Query("unread_only") == "true"
	limit := types.WorkbenchNoticesDefaultLimit
	if raw := c.Query("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	notices, unread, err := h.service.ListMyNotices(c.Request.Context(), unreadOnly, limit)
	if err != nil {
		h.fail(c, err)
		return
	}
	// 空数组而不是 null：前端直接 .map 不会炸。
	if notices == nil {
		notices = []*types.WorkbenchNotice{}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"items": notices, "unread": unread},
	})
}

// MarkNoticeRead POST /api/v1/me/workbench-notices/:id/read
func (h *WorkbenchNoticeHandler) MarkNoticeRead(c *gin.Context) {
	if err := h.service.MarkNoticeRead(c.Request.Context(), c.Param("id")); err != nil {
		h.fail(c, err)
		return
	}
	// 不返回受影响行数，避免被用来探测他人通知是否存在。
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// MarkAllNoticesRead POST /api/v1/me/workbench-notices/read-all
func (h *WorkbenchNoticeHandler) MarkAllNoticesRead(c *gin.Context) {
	if err := h.service.MarkAllNoticesRead(c.Request.Context()); err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// LeavePreview GET /api/v1/workbench/leave-preview
func (h *WorkbenchNoticeHandler) LeavePreview(c *gin.Context) {
	preview, err := h.service.LeavePreview(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": preview})
}

// fail 统一错误出口，与其余工作台 handler 保持一致。
func (h *WorkbenchNoticeHandler) fail(c *gin.Context, err error) {
	if _, ok := apperrors.IsAppError(err); ok {
		c.Error(err)
		return
	}
	c.Error(apperrors.NewInternalServerError("Failed to process workbench notice request").
		WithDetails(err.Error()))
}

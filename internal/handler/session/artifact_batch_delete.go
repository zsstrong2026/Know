package session

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// batchArtifactDeleteRequest addresses artifacts the same way the artifact
// library rows do: session + message + index. Each item re-runs the full
// ownership check inside DeleteSessionArtifact, so a stale or forged list
// cannot destroy anyone else's files.
type batchArtifactDeleteRequest struct {
	Items []batchArtifactDeleteItem `json:"items"`
}

type batchArtifactDeleteItem struct {
	SessionID string `json:"session_id"`
	MessageID string `json:"message_id"`
	Index     int    `json:"index"`
}

// BatchDeleteArtifacts godoc
// @Summary      批量删除产物
// @Description  删除多个产物库文件；每个条目单独校验会话归属，不可删除的条目会被跳过
// @Tags         会话
// @Accept       json
// @Produce      json
// @Param        request  body  batchArtifactDeleteRequest  true  "批量删除请求"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  errors.AppError  "请求参数错误"
// @Failure      404  {object}  errors.AppError  "没有任何可删除的产物"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /artifacts/batch-delete [post]
//
// The endpoint backs the artifact library's "batch delete" action: the client
// sends the rows the user ticked. Like batch download it is per-item, not
// per-request — an artifact deleted or revoked between listing and confirming
// is skipped, and the rest still go. Only when nothing is deletable does the
// request fail outright.
//
// A library row is the file, not one regeneration of it, so every item deletes
// with all_versions, matching the single-row library delete.
func (h *Handler) BatchDeleteArtifacts(c *gin.Context) {
	ctx := c.Request.Context()

	var req batchArtifactDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("invalid request"))
		return
	}
	if len(req.Items) == 0 {
		_ = c.Error(errors.NewBadRequestError("items are required"))
		return
	}
	// 与批量下载共用上限：删除同样是逐条遍历的写操作，上限防止一次请求
	// 长时间占用连接。
	if len(req.Items) > maxBatchArtifactDownloads {
		_ = c.Error(errors.NewBadRequestError("too many items"))
		return
	}

	deleted := make([]gin.H, 0, len(req.Items))
	reclaim := make([]types.ArtifactBlobRef, 0, len(req.Items))
	for _, item := range req.Items {
		sessionID := secutils.SanitizeForLog(item.SessionID)
		messageID := secutils.SanitizeForLog(item.MessageID)
		if sessionID == "" || messageID == "" || item.Index < 0 {
			continue
		}
		result, err := h.messageService.DeleteSessionArtifact(ctx, &types.ArtifactDeleteRequest{
			SessionID:   sessionID,
			MessageID:   messageID,
			Index:       item.Index,
			AllVersions: true,
		})
		if err != nil {
			// 单条失败（已删除、会话不属于调用者等）只跳过该条，不中断整批。
			logger.Warnf(ctx, "artifact batch delete: item %s/%s/%d skipped: %v",
				sessionID, messageID, item.Index, err)
			continue
		}
		reclaim = append(reclaim, result.Reclaim...)
		deleted = append(deleted, gin.H{
			"session_id": item.SessionID,
			"message_id": item.MessageID,
			"index":      item.Index,
			"file_name":  result.FileName,
		})
	}
	if len(deleted) == 0 {
		_ = c.Error(errors.NewNotFoundError("no deletable artifacts"))
		return
	}

	// 所有条目标记完成后再统一回收字节：与单个删除一致，回收失败只记日志。
	h.reclaimArtifactBlobs(ctx, types.MustTenantIDFromContext(ctx), reclaim)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"requested": len(req.Items),
			"deleted":   deleted,
		},
	})
}

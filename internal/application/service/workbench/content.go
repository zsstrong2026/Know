package workbench

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// checkpointInterval 是自动检查点的最小间隔：最近一条版本距今不足这个时长就不重复写，
// 避免 2 秒一次的自动保存把版本表刷爆。
const checkpointInterval = 10 * time.Minute

// SaveContent 保存条目内容。返回的新版本号由仓储侧回读，不做本地推算。
//
// 校验顺序固定：身份 → schema → base_version → 体积 → base64，最后才落库。
// 任何一步失败都不会触碰数据库，避免“校验失败但版本号已被 +1”。
//
// 检查点（版本行）由服务端在这里统一判定，前端不直接写版本表：
//   - force 覆盖前先把当前状态落成版本（覆盖后旧内容仍可找回）；
//   - 普通保存后按「首版 / 显式 checkpoint / 距上次 ≥10 分钟」决定是否写版本。
func (s *Service) SaveContent(
	ctx context.Context, itemID string, req types.SaveWorkbenchContentRequest,
) (*SaveResult, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateSaveRequest(&req); err != nil {
		return nil, err
	}
	ydoc, err := decodeYDoc(req.YDoc)
	if err != nil {
		return nil, err
	}

	// 仅 force 才读当前条目：普通保存每次自动保存都拉 8MB Y.Doc 不值得。
	if req.Force {
		current, err := s.repo.GetItem(ctx, tenantID, ownerID, itemID)
		if err != nil {
			return nil, mapItemError(err)
		}
		// 旧状态保不住就不覆盖，不能为了“强制成功”丢掉可回滚的内容。
		if err := s.insertCheckpoint(ctx, current); err != nil {
			return nil, err
		}
	}

	version, updatedAt, err := s.repo.UpdateItemContent(
		ctx, tenantID, ownerID, itemID, req.BaseVersion, req.Force, ydoc, req.TextContent,
		req.EditorSchemaVersion,
	)
	if err != nil {
		return nil, mapSaveError(err, version, updatedAt)
	}

	// 反向链接（P2-4 的 §P4-2「页内提及」）：保存成功后重算，失败只记日志。
	// 放在事务外、返回前：它不能影响保存结果，但也不值得为它起 goroutine。
	s.syncItemLinks(ctx, tenantID, ownerID, itemID, req.TextContent)

	checkpointed := req.Force
	if !req.Force {
		checkpointed = s.shouldCheckpoint(ctx, tenantID, ownerID, itemID, version, req.Checkpoint)
		if checkpointed {
			// 版本内容直接取本次请求，不再回读 8MB 快照；写失败只降级提醒，不影响保存结果。
			if err := s.repo.InsertVersion(ctx, &types.WorkbenchItemVersion{
				ID:                  uuid.NewString(),
				ItemID:              itemID,
				TenantID:            tenantID,
				OwnerID:             ownerID,
				Version:             version,
				EditorSchemaVersion: req.EditorSchemaVersion,
				YDoc:                ydoc,
				TextContent:         req.TextContent,
				CreatedAt:           updatedAt,
			}); err != nil {
				logger.Warnf(ctx, "workbench: failed to insert checkpoint for item %s: %v", itemID, err)
				checkpointed = false
			}
		}
	}

	return &types.SaveWorkbenchContentResult{
		SnapshotVersion:     version,
		EditorSchemaVersion: req.EditorSchemaVersion,
		Checkpointed:        checkpointed,
		UpdatedAt:           updatedAt,
	}, nil
}

// shouldCheckpoint 是实现方案 §10.4.3 的唯一判定点。
func (s *Service) shouldCheckpoint(
	ctx context.Context, tenantID uint64, ownerID, itemID string, version int, explicit bool,
) bool {
	if version == 1 || explicit {
		return true
	}
	recent, err := s.repo.ListVersions(ctx, tenantID, ownerID, itemID, 1)
	if err != nil || len(recent) == 0 {
		// 查不到就当作「没有近期版本」，写一条比丢检查点安全。
		return true
	}
	return time.Since(recent[0].CreatedAt) >= checkpointInterval
}

// insertCheckpoint 把条目的当前状态落成一条历史版本。
func (s *Service) insertCheckpoint(ctx context.Context, item *types.WorkbenchItem) error {
	return s.repo.InsertVersion(ctx, &types.WorkbenchItemVersion{
		ID:                  uuid.NewString(),
		ItemID:              item.ID,
		TenantID:            item.TenantID,
		OwnerID:             item.OwnerID,
		Version:             item.SnapshotVersion,
		EditorSchemaVersion: item.EditorSchemaVersion,
		YDoc:                item.YDoc,
		TextContent:         item.TextContent,
		CreatedAt:           time.Now().UTC(),
	})
}

// GetContent 读取条目内容，YDoc 以 base64 返回（与保存入参保持同一编码）。
func (s *Service) GetContent(ctx context.Context, itemID string) (*types.WorkbenchItemContent, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	item, err := s.repo.GetItem(ctx, tenantID, ownerID, itemID)
	if err != nil {
		return nil, mapItemError(err)
	}
	return &types.WorkbenchItemContent{
		ID:                  item.ID,
		SnapshotVersion:     item.SnapshotVersion,
		EditorSchemaVersion: item.EditorSchemaVersion,
		YDoc:                base64.StdEncoding.EncodeToString(item.YDoc),
		TextContent:         item.TextContent,
		UpdatedAt:           item.UpdatedAt,
	}, nil
}

// ListVersions 返回历史版本摘要（不含 Y.Doc）。
func (s *Service) ListVersions(
	ctx context.Context, itemID string, limit int,
) ([]*types.WorkbenchVersionSummary, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	// 先确认条目属于当前用户：否则不同 owner 的版本列表会返回空数组而不是 404，
	// 变相暴露“该 id 可能是别人的条目”。
	if _, err := s.repo.GetItemMeta(ctx, tenantID, ownerID, itemID); err != nil {
		return nil, mapItemError(err)
	}
	return s.repo.ListVersions(ctx, tenantID, ownerID, itemID, limit)
}

// RestoreVersion 用历史快照生成一个新版本（版本号 = 当前 + 1）。
//
// 恢复前先把当前状态落成版本，让“恢复”这个动作本身也可再回滚；
// 当前状态保不住就中止，不拿用户现有内容去赌。
func (s *Service) RestoreVersion(
	ctx context.Context, itemID string, version, baseVersion int,
) (*SaveResult, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if version <= 0 {
		return nil, apperrors.NewBadRequestError("version must be positive")
	}
	if baseVersion < 0 {
		return nil, apperrors.NewBadRequestError("base_version must not be negative")
	}

	current, err := s.repo.GetItem(ctx, tenantID, ownerID, itemID)
	if err != nil {
		return nil, mapItemError(err)
	}
	// 版本前置校验放在服务层：仓储若先做 UPDATE 再发现 base 过期，
	// 会白白写一次事务。
	if current.SnapshotVersion != baseVersion {
		return nil, apperrors.NewWorkbenchVersionConflict(current.SnapshotVersion, current.UpdatedAt)
	}
	if err := s.insertCheckpoint(ctx, current); err != nil {
		return nil, err
	}

	newVersion, newSchema, updatedAt, err := s.repo.RestoreVersion(
		ctx, tenantID, ownerID, itemID, version, baseVersion)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, notFound("workbench version not found")
		}
		return nil, mapSaveError(err, newVersion, updatedAt)
	}
	return &types.SaveWorkbenchContentResult{
		SnapshotVersion:     newVersion,
		EditorSchemaVersion: newSchema,
		Checkpointed:        true,
		UpdatedAt:           updatedAt,
	}, nil
}

// validateSaveRequest 校验与存储无关的入参约束（§7.5 / §8.3）。
func validateSaveRequest(req *types.SaveWorkbenchContentRequest) error {
	if strings.TrimSpace(req.ClientID) == "" {
		return apperrors.NewBadRequestError("client_id is required")
	}
	if req.EditorSchemaVersion < 1 || req.EditorSchemaVersion > types.SupportedEditorSchemaVersion {
		// 高于服务端支持值属于“客户端快照无法处理”，用 2409 而不是 400，
		// 便于前端区分“参数写错”与“版本不支持，需要升级/降级快照”。
		return apperrors.NewWorkbenchSchemaUnsupported(types.SupportedEditorSchemaVersion)
	}
	if req.BaseVersion < 0 {
		// 负数基线无意义；0 是合法的“首次保存”基线（初始 snapshot_version=0）。
		return apperrors.NewBadRequestError("base_version must not be negative")
	}
	// 粗筛：base64 编码后长度约为原始字节的 4/3，用这个上界挡住明显超限的请求，
	// 避免为一个 11MB+ 的字符串先分配解码缓冲；精确判定在 decodeYDoc 里按解码后字节数做。
	if b64 := int64(types.WorkbenchMaxYDocBytes); int64(len(req.YDoc)) > b64*4/3+8 {
		return apperrors.NewWorkbenchTooLarge(b64, int64(len(req.YDoc)))
	}
	if len(req.TextContent) > types.WorkbenchMaxTextBytes {
		return apperrors.NewWorkbenchTooLarge(types.WorkbenchMaxTextBytes, int64(len(req.TextContent)))
	}
	return nil
}

// decodeYDoc 解码 base64 快照，并按解码后的字节数复核 8MB 上限。
func decodeYDoc(raw string) ([]byte, error) {
	ydoc, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, apperrors.NewWorkbenchSchemaUnsupported(types.SupportedEditorSchemaVersion).
			WithDetails(map[string]any{"reason": "ydoc is not valid base64"})
	}
	if len(ydoc) > types.WorkbenchMaxYDocBytes {
		return nil, apperrors.NewWorkbenchTooLarge(types.WorkbenchMaxYDocBytes, int64(len(ydoc)))
	}
	return ydoc, nil
}

// mapSaveError 把仓储错误翻译成对外错误码：
// 版本冲突 → 2401（带当前版本，供前端重载后重试），不存在/越权 → 2400。
func mapSaveError(err error, currentVersion int, updatedAt time.Time) error {
	if errors.Is(err, types.ErrWorkbenchVersionConflict) {
		var conflict *types.WorkbenchVersionConflictError
		if errors.As(err, &conflict) {
			return apperrors.NewWorkbenchVersionConflict(conflict.CurrentVersion, conflict.UpdatedAt)
		}
		return apperrors.NewWorkbenchVersionConflict(currentVersion, updatedAt)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return notFound("workbench item not found")
	}
	return err
}

// encodeCursor 把一页最后一条编码成 next_cursor（updated_at|id 的 base64url）。
func encodeCursor(updatedAt time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(updatedAt.UTC().Format(time.RFC3339Nano) + "|" + id))
}

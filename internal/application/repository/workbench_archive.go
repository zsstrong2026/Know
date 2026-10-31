package repository

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// workbenchArchiveColumns 显式列出 workbench_archives 的列。
// 用 SELECT * 也不出错，但显式列可以在未来加列时早暴露漏映射。
const workbenchArchiveColumns = `id, tenant_id, owner_id, kb_id, knowledge_id, source_type,
	source_id, source_version, part_no, part_count, title, status, archived_at, created_at, updated_at`

// GetLiveByPart 返回该 source 某 part 的唯一 live 行（status <> 'deleted'）。
func (r *workbenchRepository) GetLiveByPart(
	ctx context.Context, tenantID uint64, ownerID, sourceType, sourceID string, partNo int,
) (*types.WorkbenchArchive, error) {
	var row types.WorkbenchArchive
	err := r.db.WithContext(ctx).
		Table("workbench_archives").
		Select(workbenchArchiveColumns).
		Where(`tenant_id = ? AND owner_id = ? AND source_type = ? AND source_id = ?
			AND part_no = ? AND status <> ?`,
			tenantID, ownerID, sourceType, sourceID, partNo, types.WorkbenchArchiveDeleted).
		Take(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// CreatePending 插入 pending 映射。
//
// 依赖部分唯一索引 uq_wa_live_part 处理并发：两个标签页同时归档同一 part 时，
// 只有一个 INSERT 成功，另一个走 OnConflict DoNothing → RowsAffected=0 →
// 回查既有行并返回 ErrArchiveInProgress，让调用方提示“正在归档”。
func (r *workbenchRepository) CreatePending(
	ctx context.Context, row *types.WorkbenchArchive,
) (*types.WorkbenchArchive, error) {
	res := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Table("workbench_archives").
		Create(map[string]any{
			"id":             row.ID,
			"tenant_id":      row.TenantID,
			"owner_id":       row.OwnerID,
			"kb_id":          row.KBID,
			"knowledge_id":   row.KnowledgeID,
			"source_type":    row.SourceType,
			"source_id":      row.SourceID,
			"source_version": row.SourceVersion,
			"part_no":        row.PartNo,
			"part_count":     row.PartCount,
			"title":          row.Title,
			"status":         types.WorkbenchArchivePending,
			"archived_at":    row.ArchivedAt,
			"created_at":     row.CreatedAt,
			"updated_at":     row.UpdatedAt,
		})
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected > 0 {
		return row, nil
	}
	// 冲突：说明另一路已经在归档同一 part。
	existing, err := r.GetLiveByPart(
		ctx, row.TenantID, row.OwnerID, row.SourceType, row.SourceID, row.PartNo)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		// 极端竞态：冲突行刚被标 deleted，重查不到。交给调用方重试。
		return nil, types.ErrArchiveInProgress
	}
	return existing, types.ErrArchiveInProgress
}

// ActivatePart 把映射置为 active，并写入最新版本与标题。
func (r *workbenchRepository) ActivatePart(
	ctx context.Context, id, knowledgeID string, sourceVersion int, title string,
) error {
	return r.db.WithContext(ctx).
		Table("workbench_archives").
		Where("id = ?", id).
		Updates(map[string]any{
			"knowledge_id":   knowledgeID,
			"source_version": sourceVersion,
			"title":          title,
			"status":         types.WorkbenchArchiveActive,
			"archived_at":    time.Now().UTC(),
			"updated_at":     time.Now().UTC(),
		}).Error
}

// RebindKnowledge 把映射指向重建后的新知识（原知识已被用户删除）。
func (r *workbenchRepository) RebindKnowledge(
	ctx context.Context, id, knowledgeID string, sourceVersion int, title string,
) error {
	return r.ActivatePart(ctx, id, knowledgeID, sourceVersion, title)
}

// ListLiveBySource 返回某 source 的全部 live 映射，按 part_no 升序。
func (r *workbenchRepository) ListLiveBySource(
	ctx context.Context, tenantID uint64, ownerID, sourceType, sourceID string,
) ([]*types.WorkbenchArchive, error) {
	rows := make([]*types.WorkbenchArchive, 0)
	err := r.db.WithContext(ctx).
		Table("workbench_archives").
		Select(workbenchArchiveColumns).
		Where(`tenant_id = ? AND owner_id = ? AND source_type = ? AND source_id = ?
			AND status <> ?`,
			tenantID, ownerID, sourceType, sourceID, types.WorkbenchArchiveDeleted).
		Order("part_no ASC").
		Find(&rows).Error
	return rows, err
}

// ListLiveBySources 批量版：一次取回整页条目的映射，避免逐条查询。
func (r *workbenchRepository) ListLiveBySources(
	ctx context.Context, tenantID uint64, ownerID, sourceType string, ids []string,
) (map[string][]*types.WorkbenchArchive, error) {
	out := make(map[string][]*types.WorkbenchArchive, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows := make([]*types.WorkbenchArchive, 0, len(ids))
	if err := r.db.WithContext(ctx).
		Table("workbench_archives").
		Select(workbenchArchiveColumns).
		Where(`tenant_id = ? AND owner_id = ? AND source_type = ? AND source_id IN ?
			AND status <> ?`,
			tenantID, ownerID, sourceType, ids, types.WorkbenchArchiveDeleted).
		Order("source_id ASC, part_no ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.SourceID] = append(out[row.SourceID], row)
	}
	return out, nil
}

// MarkDeletedBySource 把该 source 的全部 live 映射置 deleted（不删知识）。
func (r *workbenchRepository) MarkDeletedBySource(
	ctx context.Context, tenantID uint64, ownerID, sourceType, sourceID string,
) (int64, error) {
	res := r.db.WithContext(ctx).
		Table("workbench_archives").
		Where(`tenant_id = ? AND owner_id = ? AND source_type = ? AND source_id = ?
			AND status <> ?`,
			tenantID, ownerID, sourceType, sourceID, types.WorkbenchArchiveDeleted).
		Updates(map[string]any{
			"status":     types.WorkbenchArchiveDeleted,
			"updated_at": time.Now().UTC(),
		})
	return res.RowsAffected, res.Error
}

// MarkDeletedBySourcePart 只标记单个 part（刷新归档时新集合里不再存在的 part）。
func (r *workbenchRepository) MarkDeletedBySourcePart(
	ctx context.Context, tenantID uint64, ownerID, sourceType, sourceID string, partNo int,
) (int64, error) {
	res := r.db.WithContext(ctx).
		Table("workbench_archives").
		Where(`tenant_id = ? AND owner_id = ? AND source_type = ? AND source_id = ?
			AND part_no = ? AND status <> ?`,
			tenantID, ownerID, sourceType, sourceID, partNo, types.WorkbenchArchiveDeleted).
		Updates(map[string]any{
			"status":     types.WorkbenchArchiveDeleted,
			"updated_at": time.Now().UTC(),
		})
	return res.RowsAffected, res.Error
}

// MarkDeletedByKB 把某 KB 下的 live 映射全部置 deleted（清空工作 KB 时用）。
func (r *workbenchRepository) MarkDeletedByKB(
	ctx context.Context, tenantID uint64, ownerID, kbID string,
) (int64, error) {
	res := r.db.WithContext(ctx).
		Table("workbench_archives").
		Where(`tenant_id = ? AND owner_id = ? AND kb_id = ? AND status <> ?`,
			tenantID, ownerID, kbID, types.WorkbenchArchiveDeleted).
		Updates(map[string]any{
			"status":     types.WorkbenchArchiveDeleted,
			"updated_at": time.Now().UTC(),
		})
	return res.RowsAffected, res.Error
}

// CountAnyBySource 统计该 source 的全部映射（含 status='deleted'）。
func (r *workbenchRepository) CountAnyBySource(
	ctx context.Context, tenantID uint64, ownerID, sourceType, sourceID string,
) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Table("workbench_archives").
		Where("tenant_id = ? AND owner_id = ? AND source_type = ? AND source_id = ?",
			tenantID, ownerID, sourceType, sourceID).
		Count(&count).Error
	return count, err
}

// CountArchiveParts 统计该用户 live 状态的归档分片数（Bootstrap 计数用）。
func (r *workbenchRepository) CountArchiveParts(
	ctx context.Context, tenantID uint64, ownerID string,
) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Table("workbench_archives").
		Where("tenant_id = ? AND owner_id = ? AND status <> ?",
			tenantID, ownerID, types.WorkbenchArchiveDeleted).
		Count(&count).Error
	return count, err
}

package repository

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// workbenchOrphanColumns 显式列出 workbench_orphans 的列。
const workbenchOrphanColumns = `id, tenant_id, user_id, reason, suspended_at, purge_after,
	created_at, updated_at`

// UpsertOrphan 写入或重置保留标记（§5.5）。
//
// 冲突目标 (tenant_id, user_id) 由 uq_wo_tenant_user 保证：同一用户重复被移除时
// 覆盖原因与时间，等于把 30 天保留期重新计时。
func (r *workbenchRepository) UpsertOrphan(
	ctx context.Context, orphan *types.WorkbenchOrphan,
) error {
	return r.db.WithContext(ctx).
		Table("workbench_orphans").
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "tenant_id"}, {Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"reason", "suspended_at", "purge_after", "updated_at",
			}),
		}).
		Create(map[string]any{
			"id":           orphan.ID,
			"tenant_id":    orphan.TenantID,
			"user_id":      orphan.UserID,
			"reason":       orphan.Reason,
			"suspended_at": orphan.SuspendedAt,
			"purge_after":  orphan.PurgeAfter,
			"created_at":   orphan.CreatedAt,
			"updated_at":   orphan.UpdatedAt,
		}).Error
}

// GetOrphan 返回该 (tenant, user) 的保留标记。
func (r *workbenchRepository) GetOrphan(
	ctx context.Context, tenantID uint64, userID string,
) (*types.WorkbenchOrphan, error) {
	var row types.WorkbenchOrphan
	err := r.db.WithContext(ctx).
		Table("workbench_orphans").
		Select(workbenchOrphanColumns).
		Where("tenant_id = ? AND user_id = ?", tenantID, userID).
		Take(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// DeleteOrphan 删除保留标记；重新加入时调用。
func (r *workbenchRepository) DeleteOrphan(
	ctx context.Context, tenantID uint64, userID string,
) (int64, error) {
	res := r.db.WithContext(ctx).
		Table("workbench_orphans").
		Where("tenant_id = ? AND user_id = ?", tenantID, userID).
		Delete(nil)
	return res.RowsAffected, res.Error
}

// ListOrphansDue 取已到期（purge_after <= at）的保留标记。
func (r *workbenchRepository) ListOrphansDue(
	ctx context.Context, at time.Time, limit int,
) ([]*types.WorkbenchOrphan, error) {
	if limit <= 0 {
		limit = 100
	}
	rows := make([]*types.WorkbenchOrphan, 0, limit)
	err := r.db.WithContext(ctx).
		Table("workbench_orphans").
		Select(workbenchOrphanColumns).
		Where("purge_after <= ?", at).
		Order("purge_after ASC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

// ListOrphansExpiring 取 7 天预警窗口内的保留标记（from < purge_after <= to）。
func (r *workbenchRepository) ListOrphansExpiring(
	ctx context.Context, from, to time.Time, limit int,
) ([]*types.WorkbenchOrphan, error) {
	if limit <= 0 {
		limit = 100
	}
	rows := make([]*types.WorkbenchOrphan, 0, limit)
	err := r.db.WithContext(ctx).
		Table("workbench_orphans").
		Select(workbenchOrphanColumns).
		Where("purge_after > ? AND purge_after <= ?", from, to).
		Order("purge_after ASC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

// PurgeOwnerData 硬删该 owner 在工作台的全部数据行。
//
// 逐表按 (tenant_id, owner_id) 删即可：七张表都带这两列，不依赖条目 id 回查，
// 也天然覆盖“条目已删但版本残留”的历史脏数据。
// orphan 标记本身不在这里删：由清理任务在成功之后单独删除，
// 以便失败时下轮重试（删掉标记就没有重试锚点了）。
func (r *workbenchRepository) PurgeOwnerData(
	ctx context.Context, tenantID uint64, ownerID string,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, table := range []string{
			"workbench_item_versions",
			"workbench_item_links",
			"workbench_item_assets",
			"workbench_archives",
			"workbench_templates",
			"workbench_items",
			"workbench_folders",
		} {
			if err := tx.Unscoped().
				Table(table).
				Where("tenant_id = ? AND owner_id = ?", tenantID, ownerID).
				Delete(nil).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

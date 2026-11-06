package repository

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// InsertVersion 插入历史版本行。
//
// OnConflict DoNothing：两个标签页同时触发 checkpoint 时只留一条，
// 不报唯一键冲突，也不覆盖已经写入的字节。
func (r *workbenchRepository) InsertVersion(
	ctx context.Context, v *types.WorkbenchItemVersion,
) error {
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Table("workbench_item_versions").
		Create(map[string]any{
			"id":                    v.ID,
			"item_id":               v.ItemID,
			"tenant_id":             v.TenantID,
			"owner_id":              v.OwnerID,
			"version":               v.Version,
			"editor_schema_version": v.EditorSchemaVersion,
			"ydoc":                  v.YDoc,
			"text_content":          v.TextContent,
			"created_by":            v.CreatedBy,
			"created_at":            v.CreatedAt,
		}).Error
}

// ListVersions 返回版本摘要（不含 ydoc），size_bytes 由 length(ydoc) 现算。
// 表里没有 size_bytes 列，所以这里必须用 SQL 投影而不是让 GORM 拼字段。
func (r *workbenchRepository) ListVersions(
	ctx context.Context, tenantID uint64, ownerID, itemID string, limit int,
) ([]*types.WorkbenchVersionSummary, error) {
	if limit <= 0 {
		limit = types.WorkbenchVersionsDefaultLimit
	}
	if limit > types.WorkbenchVersionsMaxLimit {
		limit = types.WorkbenchVersionsMaxLimit
	}
	rows := make([]*types.WorkbenchVersionSummary, 0)
	err := r.db.WithContext(ctx).
		Table("workbench_item_versions").
		Select(`version, editor_schema_version, COALESCE(length(ydoc), 0) AS size_bytes, created_at`).
		Where("item_id = ? AND tenant_id = ? AND owner_id = ?", itemID, tenantID, ownerID).
		Order("version DESC").
		Limit(limit).
		Scan(&rows).Error
	return rows, err
}

// GetVersion 读取单个历史版本（含 ydoc）。
func (r *workbenchRepository) GetVersion(
	ctx context.Context, tenantID uint64, ownerID, itemID string, version int,
) (*types.WorkbenchItemVersion, error) {
	var row types.WorkbenchItemVersion
	err := r.db.WithContext(ctx).
		Table("workbench_item_versions").
		Select(`id, item_id, tenant_id, owner_id, version, editor_schema_version,
			ydoc, text_content, created_by, created_at`).
		Where("item_id = ? AND tenant_id = ? AND owner_id = ? AND version = ?",
			itemID, tenantID, ownerID, version).
		Take(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// RestoreVersion 在一个事务里把历史快照写回当前条目。
//
// 关键约定：
//   - 新版本号 = 当前版本 + 1，绝不把旧版本号写回；
//   - editor_schema_version 一并恢复，避免用新 schema 解释旧 Y.Doc；
//   - 乐观锁失败返回 types.ErrWorkbenchVersionConflict，版本不存在返回 NotFound。
func (r *workbenchRepository) RestoreVersion(
	ctx context.Context, tenantID uint64, ownerID, itemID string, version, baseVersion int,
) (int, int, time.Time, error) {
	var (
		newVersion int
		newSchema  int
		updatedAt  time.Time
	)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var source types.WorkbenchItemVersion
		if err := tx.Table("workbench_item_versions").
			Select(`id, item_id, tenant_id, owner_id, version, editor_schema_version,
				ydoc, text_content, created_by, created_at`).
			Where("item_id = ? AND tenant_id = ? AND owner_id = ? AND version = ?",
				itemID, tenantID, ownerID, version).
			Take(&source).Error; err != nil {
			// 版本不属于该 item/owner 也按不存在处理，由服务层转 2400。
			return err
		}

		now := time.Now().UTC()
		res := tx.Table("workbench_items").
			Where(`id = ? AND tenant_id = ? AND owner_id = ? AND deleted_at IS NULL
				AND snapshot_version = ?`, itemID, tenantID, ownerID, baseVersion).
			Updates(map[string]any{
				"ydoc":                  source.YDoc,
				"text_content":          source.TextContent,
				"editor_schema_version": source.EditorSchemaVersion,
				"snapshot_version":      gorm.Expr("snapshot_version + 1"),
				"updated_at":            now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return types.ErrWorkbenchVersionConflict
		}

		var current types.WorkbenchItem
		if err := tx.Table("workbench_items").
			Select("snapshot_version, editor_schema_version, updated_at").
			Where("id = ? AND tenant_id = ? AND owner_id = ?", itemID, tenantID, ownerID).
			Take(&current).Error; err != nil {
			return err
		}
		newVersion, newSchema, updatedAt = current.SnapshotVersion, current.EditorSchemaVersion, current.UpdatedAt
		// 版本行由服务层在调用本方法之前写入（“恢复前先给当前状态留档”），
		// 仓储这里不再写第二条，否则同一版本号会出现两条相互矛盾的记录。
		return nil
	})
	return newVersion, newSchema, updatedAt, err
}

// PruneVersions 只保留最近 keep 个版本，返回删除行数。
// 用「先取要保留的最小版本号，再删更小的」两步，避免依赖数据库的 LIMIT 子查询语法差异。
func (r *workbenchRepository) PruneVersions(
	ctx context.Context, tenantID uint64, ownerID, itemID string, keep int,
) (int64, error) {
	if keep <= 0 {
		return 0, nil
	}
	var cutoff int
	err := r.db.WithContext(ctx).
		Table("workbench_item_versions").
		Select("version").
		Where("item_id = ? AND tenant_id = ? AND owner_id = ?", itemID, tenantID, ownerID).
		Order("version DESC").
		Offset(keep - 1).
		Limit(1).
		Scan(&cutoff).Error
	if err != nil || cutoff == 0 {
		// cutoff==0 说明版本数不足 keep，无需清理。
		return 0, err
	}
	res := r.db.WithContext(ctx).
		Table("workbench_item_versions").
		Where("item_id = ? AND tenant_id = ? AND owner_id = ? AND version < ?",
			itemID, tenantID, ownerID, cutoff).
		Delete(nil)
	return res.RowsAffected, res.Error
}

// PruneExpiredVersions 跨用户执行版本保留（§5.6 第 4 条）。
//
// 保留规则：每个条目最近的 keep 个版本永不过期，之外的行只在 created_at
// 早于 before 时删除。用相关子查询表达「不在最近 keep 个里」，
// 避免为每个条目跑一次单独查询。
func (r *workbenchRepository) PruneExpiredVersions(
	ctx context.Context, before time.Time, keep, limit int,
) (int64, error) {
	if keep <= 0 {
		return 0, nil
	}
	sub := r.db.WithContext(ctx).
		Table("workbench_item_versions AS v2").
		Select("v2.id").
		Where("v2.item_id = v.item_id").
		Order("v2.version DESC").
		Limit(keep)
	query := r.db.WithContext(ctx).
		Table("workbench_item_versions AS v").
		Where("v.created_at < ?", before).
		Where("v.id NOT IN (?)", sub)
	if limit > 0 {
		query = query.Limit(limit)
	}
	res := query.Delete(nil)
	return res.RowsAffected, res.Error
}

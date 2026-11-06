package repository

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// 个人标签仓储（P4-2）。所有查询都带 tenant_id + owner_id，越权即 not found。
//
// 标签与条目的关联单独一张 workbench_item_tags：主键 (item_id, tag_id) 天然去重，
// 覆盖式写入时先按 item_id 删再插，不需要 diff。
var workbenchTagMutableFields = map[string]bool{
	"name":  true,
	"color": true,
}

// CreateTag 插入标签；同名（未删除）由唯一索引挡住，转换为可识别的重复错误。
func (r *workbenchRepository) CreateTag(
	ctx context.Context, tenantID uint64, ownerID string, tag *types.WorkbenchTag,
) error {
	tag.TenantID = tenantID
	tag.OwnerID = ownerID
	now := time.Now().UTC()
	if tag.CreatedAt.IsZero() {
		tag.CreatedAt = now
	}
	tag.UpdatedAt = now
	err := r.db.WithContext(ctx).Table("workbench_tags").Create(map[string]any{
		"id":         tag.ID,
		"tenant_id":  tag.TenantID,
		"owner_id":   tag.OwnerID,
		"name":       tag.Name,
		"color":      tag.Color,
		"created_at": tag.CreatedAt,
		"updated_at": tag.UpdatedAt,
	}).Error
	if err != nil && isUniqueViolation(err) {
		return types.ErrWorkbenchTagNameDup
	}
	return err
}

// ListTags 返回该用户的标签与条目数（一次 LEFT JOIN 聚合，避免 N+1）。
func (r *workbenchRepository) ListTags(
	ctx context.Context, tenantID uint64, ownerID string,
) ([]*types.WorkbenchTag, error) {
	rows := make([]*types.WorkbenchTag, 0)
	err := r.db.WithContext(ctx).
		Table("workbench_tags AS t").
		Select(`t.id, t.tenant_id, t.owner_id, t.name, t.color, t.created_at, t.updated_at,
			COUNT(link.item_id) AS item_count`).
		Joins("LEFT JOIN workbench_item_tags AS link ON link.tag_id = t.id AND link.tenant_id = t.tenant_id AND link.owner_id = t.owner_id").
		Where("t.tenant_id = ? AND t.owner_id = ? AND t.deleted_at IS NULL", tenantID, ownerID).
		Group("t.id, t.tenant_id, t.owner_id, t.name, t.color, t.created_at, t.updated_at").
		Order("t.name ASC").
		Scan(&rows).Error
	return rows, err
}

// GetTag 单条读取；他人或跨租户一律 not found。
func (r *workbenchRepository) GetTag(
	ctx context.Context, tenantID uint64, ownerID, id string,
) (*types.WorkbenchTag, error) {
	var tag types.WorkbenchTag
	err := r.db.WithContext(ctx).
		Table("workbench_tags").
		Where("id = ? AND tenant_id = ? AND owner_id = ? AND deleted_at IS NULL", id, tenantID, ownerID).
		Take(&tag).Error
	if err != nil {
		return nil, err
	}
	return &tag, nil
}

// UpdateTag 只改白名单字段；同名冲突同样转成重复错误。
func (r *workbenchRepository) UpdateTag(
	ctx context.Context, tenantID uint64, ownerID, id string, fields map[string]any,
) (*types.WorkbenchTag, error) {
	updates := make(map[string]any, len(fields)+1)
	for key, value := range fields {
		if workbenchTagMutableFields[key] {
			updates[key] = value
		}
	}
	if len(updates) == 0 {
		return r.GetTag(ctx, tenantID, ownerID, id)
	}
	updates["updated_at"] = time.Now().UTC()
	res := r.db.WithContext(ctx).
		Table("workbench_tags").
		Where("id = ? AND tenant_id = ? AND owner_id = ? AND deleted_at IS NULL", id, tenantID, ownerID).
		Updates(updates)
	if res.Error != nil {
		if isUniqueViolation(res.Error) {
			return nil, types.ErrWorkbenchTagNameDup
		}
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return r.GetTag(ctx, tenantID, ownerID, id)
}

// DeleteTag 软删标签并在同一事务里清掉关联；条目的其它标签不受影响。
func (r *workbenchRepository) DeleteTag(ctx context.Context, tenantID uint64, ownerID, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Table("workbench_tags").
			Where("id = ? AND tenant_id = ? AND owner_id = ? AND deleted_at IS NULL", id, tenantID, ownerID).
			Update("deleted_at", time.Now().UTC())
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return tx.Table("workbench_item_tags").
			Where("tag_id = ? AND tenant_id = ? AND owner_id = ?", id, tenantID, ownerID).
			Delete(nil).Error
	})
}

// ListTagsByItem 某个条目上的标签。
func (r *workbenchRepository) ListTagsByItem(
	ctx context.Context, tenantID uint64, ownerID, itemID string,
) ([]*types.WorkbenchTag, error) {
	rows := make([]*types.WorkbenchTag, 0)
	err := r.db.WithContext(ctx).
		Table("workbench_tags AS t").
		Select("t.id, t.tenant_id, t.owner_id, t.name, t.color, t.created_at, t.updated_at").
		Joins("JOIN workbench_item_tags AS link ON link.tag_id = t.id AND link.tenant_id = t.tenant_id AND link.owner_id = t.owner_id").
		Where("link.tenant_id = ? AND link.owner_id = ? AND link.item_id = ? AND t.deleted_at IS NULL",
			tenantID, ownerID, itemID).
		Order("t.name ASC").
		Scan(&rows).Error
	return rows, err
}

// ReplaceItemTags 覆盖式写入：先清空该条目的关联，再按 tagIDs 插入。
//
// 只接受属于同一 owner 的标签 id：越权的 id 会被静默丢弃，
// 不让"猜到一个别人的 tag id"变成一次越权写入。
func (r *workbenchRepository) ReplaceItemTags(
	ctx context.Context, tenantID uint64, ownerID, itemID string, tagIDs []string,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("workbench_item_tags").
			Where("item_id = ? AND tenant_id = ? AND owner_id = ?", itemID, tenantID, ownerID).
			Delete(nil).Error; err != nil {
			return err
		}
		if len(tagIDs) == 0 {
			return nil
		}
		var owned []string
		if err := tx.Table("workbench_tags").
			Where("tenant_id = ? AND owner_id = ? AND deleted_at IS NULL AND id IN ?", tenantID, ownerID, tagIDs).
			Pluck("id", &owned).Error; err != nil {
			return err
		}
		if len(owned) == 0 {
			return nil
		}
		now := time.Now().UTC()
		rows := make([]map[string]any, 0, len(owned))
		for _, tagID := range owned {
			rows = append(rows, map[string]any{
				"tenant_id":  tenantID,
				"owner_id":   ownerID,
				"item_id":    itemID,
				"tag_id":     tagID,
				"created_at": now,
			})
		}
		return tx.Table("workbench_item_tags").Create(&rows).Error
	})
}

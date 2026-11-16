package repository

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// workbenchFolderMutableFields 是 UpdateFolder 的白名单。
// 只允许改名与改父级；tenant_id/owner_id 永远不可改。
var workbenchFolderMutableFields = map[string]bool{
	"name":       true,
	"parent_id":  true,
	"sort_order": true,
}

// CreateFolder 插入文件夹。
func (r *workbenchRepository) CreateFolder(
	ctx context.Context, tenantID uint64, ownerID string, folder *types.WorkbenchFolder,
) error {
	var parentID any
	if folder.ParentID != nil {
		parentID = *folder.ParentID
	}
	return r.db.WithContext(ctx).Table("workbench_folders").Create(map[string]any{
		"id":         folder.ID,
		"tenant_id":  tenantID,
		"owner_id":   ownerID,
		"name":       folder.Name,
		"parent_id":  parentID,
		"sort_order": folder.SortOrder,
		"created_at": folder.CreatedAt,
		"updated_at": folder.UpdatedAt,
	}).Error
}

// GetFolder 读取单个文件夹；越权与不存在都返回 NotFound。
func (r *workbenchRepository) GetFolder(
	ctx context.Context, tenantID uint64, ownerID, id string,
) (*types.WorkbenchFolder, error) {
	var folder types.WorkbenchFolder
	err := r.db.WithContext(ctx).
		Table("workbench_folders").
		Where("id = ? AND tenant_id = ? AND owner_id = ? AND deleted_at IS NULL",
			id, tenantID, ownerID).
		Take(&folder).Error
	if err != nil {
		return nil, err
	}
	return &folder, nil
}

// UpdateFolder 只写白名单字段。
func (r *workbenchRepository) UpdateFolder(
	ctx context.Context, tenantID uint64, ownerID, id string, fields map[string]any,
) (*types.WorkbenchFolder, error) {
	updates := make(map[string]any, len(fields)+1)
	for key, value := range fields {
		if workbenchFolderMutableFields[key] {
			updates[key] = value
		}
	}
	if len(updates) == 0 {
		return r.GetFolder(ctx, tenantID, ownerID, id)
	}
	updates["updated_at"] = time.Now().UTC()

	res := r.db.WithContext(ctx).
		Table("workbench_folders").
		Where("id = ? AND tenant_id = ? AND owner_id = ? AND deleted_at IS NULL",
			id, tenantID, ownerID).
		Updates(updates)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return r.GetFolder(ctx, tenantID, ownerID, id)
}

// ReorderFolders 按给定顺序重写同一父级下文件夹的 sort_order。
//
// 只接受「父级的完整子集合」：服务层先算出顺序再整体写回，因此不会出现
// 两个文件夹共用同一 sort_order 的中间态。空列表是合法的空操作。
//
// parentID 为 nil 表示根层级；用 IS NULL 而不是 = ”，与 ListFolders 的口径一致。
func (r *workbenchRepository) ReorderFolders(
	ctx context.Context, tenantID uint64, ownerID string, parentID *string, orderedIDs []string,
) error {
	if len(orderedIDs) == 0 {
		return nil
	}
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Table("workbench_folders").
			Where("tenant_id = ? AND owner_id = ? AND deleted_at IS NULL", tenantID, ownerID)
		if parentID == nil || *parentID == "" {
			query = query.Where("parent_id IS NULL")
		} else {
			query = query.Where("parent_id = ?", *parentID)
		}
		var existing []string
		if err := query.Pluck("id", &existing).Error; err != nil {
			return err
		}
		allowed := make(map[string]bool, len(existing))
		for _, id := range existing {
			allowed[id] = true
		}
		for index, id := range orderedIDs {
			// 不属于该父级的 id 直接跳过：宁可少写一条，也不要把别人的文件夹挪走
			if !allowed[id] {
				continue
			}
			if err := tx.Table("workbench_folders").
				Where("id = ? AND tenant_id = ? AND owner_id = ?", id, tenantID, ownerID).
				UpdateColumns(map[string]any{"sort_order": index, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteFolder 软删文件夹。子文件夹与条目的上移由调用方（服务层）在同一事务之前完成，
// 因为“上移到父级”需要先读到 parent_id。
func (r *workbenchRepository) DeleteFolder(
	ctx context.Context, tenantID uint64, ownerID, id string,
) error {
	res := r.db.WithContext(ctx).
		Table("workbench_folders").
		Where("id = ? AND tenant_id = ? AND owner_id = ? AND deleted_at IS NULL",
			id, tenantID, ownerID).
		Update("deleted_at", time.Now().UTC())
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ListFolders 返回该用户的全部文件夹，按层级与排序位稳定输出。
// ItemCount 由一次分组统计补齐，供文件夹树展示「该文件夹下有多少文档」。
func (r *workbenchRepository) ListFolders(
	ctx context.Context, tenantID uint64, ownerID string,
) ([]*types.WorkbenchFolder, error) {
	folders := make([]*types.WorkbenchFolder, 0)
	err := r.db.WithContext(ctx).
		Table("workbench_folders").
		Where("tenant_id = ? AND owner_id = ? AND deleted_at IS NULL", tenantID, ownerID).
		Order("parent_id ASC, sort_order ASC, created_at ASC").
		Find(&folders).Error
	if err != nil || len(folders) == 0 {
		return folders, err
	}

	// 只统计未删除条目；文件夹只收纳文档，所以这里就是文档数量。
	var counts []struct {
		FolderID string `gorm:"column:folder_id"`
		Total    int    `gorm:"column:total"`
	}
	if err := r.db.WithContext(ctx).
		Table("workbench_items").
		Select("folder_id, COUNT(*) AS total").
		Where("tenant_id = ? AND owner_id = ? AND folder_id IS NOT NULL AND deleted_at IS NULL",
			tenantID, ownerID).
		Group("folder_id").
		Scan(&counts).Error; err != nil {
		return nil, err
	}
	byFolder := make(map[string]int, len(counts))
	for _, row := range counts {
		byFolder[row.FolderID] = row.Total
	}
	for _, folder := range folders {
		folder.ItemCount = byFolder[folder.ID]
	}
	return folders, nil
}

// ReparentChildren 把直接子文件夹挂到 newParentID（nil 表示根）。
func (r *workbenchRepository) ReparentChildren(
	ctx context.Context, tenantID uint64, ownerID, folderID string, newParentID *string,
) error {
	updates := map[string]any{"updated_at": time.Now().UTC()}
	if newParentID != nil {
		updates["parent_id"] = *newParentID
	} else {
		// GORM 的 map 更新把 nil 当“不更新”，置空必须用 SQL 表达式。
		updates["parent_id"] = gorm.Expr("NULL")
	}
	return r.db.WithContext(ctx).
		Table("workbench_folders").
		Where("tenant_id = ? AND owner_id = ? AND parent_id = ? AND deleted_at IS NULL",
			tenantID, ownerID, folderID).
		Updates(updates).Error
}

// MoveItemsToFolder 把该文件夹下的条目挂到 newParentID（nil 表示根）。
func (r *workbenchRepository) MoveItemsToFolder(
	ctx context.Context, tenantID uint64, ownerID, folderID string, newParentID *string,
) error {
	updates := map[string]any{
		"folder_id":  gorm.Expr("NULL"),
		"updated_at": time.Now().UTC(),
	}
	if newParentID != nil {
		updates["folder_id"] = *newParentID
	}
	return r.db.WithContext(ctx).
		Table("workbench_items").
		Where("tenant_id = ? AND owner_id = ? AND folder_id = ? AND deleted_at IS NULL",
			tenantID, ownerID, folderID).
		Updates(updates).Error
}

// CountFolders 统计未删除的文件夹数。
func (r *workbenchRepository) CountFolders(
	ctx context.Context, tenantID uint64, ownerID string,
) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Table("workbench_folders").
		Where("tenant_id = ? AND owner_id = ? AND deleted_at IS NULL", tenantID, ownerID).
		Count(&count).Error
	return count, err
}

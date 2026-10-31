package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// workbenchTemplateSummaryColumns 列表列：刻意不含 ydoc，避免把 1MB 快照读进内存。
const workbenchTemplateSummaryColumns = `id, name, description, size_bytes, editor_schema_version,
	source_item_id, created_at, updated_at`

// workbenchTemplateMutableFields 是 UpdateTemplate 的白名单；ydoc 永不更新
// （模板是 copy-on-use 的快照，改模板不追溯已创建文档）。
var workbenchTemplateMutableFields = map[string]bool{
	"name":        true,
	"description": true,
	"updated_at":  true,
}

// ListTemplates 按 updated_at 倒序取该用户的模板元数据，最多 50 条。
func (r *workbenchRepository) ListTemplates(
	ctx context.Context, tenantID uint64, ownerID string,
) ([]*types.WorkbenchTemplateSummary, error) {
	rows := make([]*types.WorkbenchTemplateSummary, 0, types.WorkbenchTemplateMaxPerOwner)
	err := r.db.WithContext(ctx).
		Table("workbench_templates").
		Select(workbenchTemplateSummaryColumns).
		Where("tenant_id = ? AND owner_id = ?", tenantID, ownerID).
		Order("updated_at DESC").
		Limit(types.WorkbenchTemplateMaxPerOwner).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// CountTemplates 配额检查；配额在服务层兜底（UI 已预检）。
func (r *workbenchRepository) CountTemplates(
	ctx context.Context, tenantID uint64, ownerID string,
) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Table("workbench_templates").
		Where("tenant_id = ? AND owner_id = ?", tenantID, ownerID).
		Count(&count).Error
	return count, err
}

// CreateTemplate 插入一行；ydoc 由服务层从源条目复制后传入，服务端不解析。
func (r *workbenchRepository) CreateTemplate(
	ctx context.Context, tpl *types.WorkbenchTemplate,
) error {
	var sourceID any
	if tpl.SourceItemID != nil {
		sourceID = *tpl.SourceItemID
	}
	return r.db.WithContext(ctx).Table("workbench_templates").Create(map[string]any{
		"id":                    tpl.ID,
		"tenant_id":             tpl.TenantID,
		"owner_id":              tpl.OwnerID,
		"name":                  tpl.Name,
		"description":           tpl.Description,
		"ydoc":                  tpl.YDoc,
		"size_bytes":            tpl.SizeBytes,
		"editor_schema_version": tpl.EditorSchemaVersion,
		"source_item_id":        sourceID,
		"created_at":            tpl.CreatedAt,
		"updated_at":            tpl.UpdatedAt,
	}).Error
}

// GetTemplate 取元数据（不含 ydoc）；越权与不存在都返回 (nil, nil)，
// 由服务层统一报 2400，避免泄露存在性。
func (r *workbenchRepository) GetTemplate(
	ctx context.Context, tenantID uint64, ownerID, id string,
) (*types.WorkbenchTemplate, error) {
	var row types.WorkbenchTemplate
	err := r.db.WithContext(ctx).
		Table("workbench_templates").
		Select(workbenchTemplateSummaryColumns).
		Where("id = ? AND tenant_id = ? AND owner_id = ?", id, tenantID, ownerID).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// GetTemplateWithContent 只在套用模板时调用，取含 ydoc 的整行。
func (r *workbenchRepository) GetTemplateWithContent(
	ctx context.Context, tenantID uint64, ownerID, id string,
) (*types.WorkbenchTemplate, error) {
	var row types.WorkbenchTemplate
	err := r.db.WithContext(ctx).
		Table("workbench_templates").
		Where("id = ? AND tenant_id = ? AND owner_id = ?", id, tenantID, ownerID).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// UpdateTemplate 只写名称/描述白名单字段；返回更新后的整行。
func (r *workbenchRepository) UpdateTemplate(
	ctx context.Context, tenantID uint64, ownerID, id string, fields map[string]any,
) (*types.WorkbenchTemplate, error) {
	updates := make(map[string]any, len(fields)+1)
	for key, value := range fields {
		if workbenchTemplateMutableFields[key] {
			updates[key] = value
		}
	}
	if len(updates) == 0 {
		return r.GetTemplate(ctx, tenantID, ownerID, id)
	}
	updates["updated_at"] = time.Now().UTC()
	result := r.db.WithContext(ctx).
		Table("workbench_templates").
		Where("id = ? AND tenant_id = ? AND owner_id = ?", id, tenantID, ownerID).
		Updates(updates)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return r.GetTemplate(ctx, tenantID, ownerID, id)
}

// DeleteTemplate 硬删：模板不进回收站，删了就是删了。
func (r *workbenchRepository) DeleteTemplate(
	ctx context.Context, tenantID uint64, ownerID, id string,
) (int64, error) {
	result := r.db.WithContext(ctx).
		Table("workbench_templates").
		Where("id = ? AND tenant_id = ? AND owner_id = ?", id, tenantID, ownerID).
		Delete(nil)
	return result.RowsAffected, result.Error
}

// DeleteByOwner 供租户/成员清理使用。
func (r *workbenchRepository) DeleteByOwner(
	ctx context.Context, tenantID uint64, ownerID string,
) (int64, error) {
	result := r.db.WithContext(ctx).
		Table("workbench_templates").
		Where("tenant_id = ? AND owner_id = ?", tenantID, ownerID).
		Delete(nil)
	return result.RowsAffected, result.Error
}

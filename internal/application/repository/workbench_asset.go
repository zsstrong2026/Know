package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// workbenchAssetColumns 显式列名：storage_path 与 content_hash 只在本包内使用，
// 但读取时要带上（下载与去重都需要），泄漏由 JSON tag 的 `-` 负责，不靠不查。
const workbenchAssetColumns = `id, tenant_id, owner_id, item_id, storage_path, original_name,
	mime_type, content_hash, size_bytes, last_seen_at, created_at`

// CreateAsset 插入资产行。
//
// 依赖 uq_wia_item_hash 做去重：同一 item 下同 content_hash 的 live 行已存在时，
// 唯一索引报冲突，调用方应改为读回旧行（见 GetLiveAssetByHash）。
func (r *workbenchRepository) CreateAsset(
	ctx context.Context, asset *types.WorkbenchAsset,
) error {
	return r.db.WithContext(ctx).
		Table("workbench_item_assets").
		Create(map[string]any{
			"id":            asset.ID,
			"tenant_id":     asset.TenantID,
			"owner_id":      asset.OwnerID,
			"item_id":       asset.ItemID,
			"storage_path":  asset.StoragePath,
			"original_name": asset.OriginalName,
			"mime_type":     asset.MimeType,
			"content_hash":  asset.ContentHash,
			"size_bytes":    asset.SizeBytes,
			"last_seen_at":  asset.LastSeenAt,
			"created_at":    asset.CreatedAt,
		}).Error
}

// GetAsset 按 id 取 live 资产（deleted_at IS NULL）；越权与不存在都返回 (nil, nil)。
func (r *workbenchRepository) GetAsset(
	ctx context.Context, tenantID uint64, ownerID, id string,
) (*types.WorkbenchAsset, error) {
	var row types.WorkbenchAsset
	err := r.db.WithContext(ctx).
		Table("workbench_item_assets").
		Select(workbenchAssetColumns).
		Where("id = ? AND tenant_id = ? AND owner_id = ? AND deleted_at IS NULL",
			id, tenantID, ownerID).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// GetLiveAssetByHash 按内容哈希查同 item 下的 live 资产，用于上传去重。
func (r *workbenchRepository) GetLiveAssetByHash(
	ctx context.Context, tenantID uint64, ownerID, itemID, contentHash string,
) (*types.WorkbenchAsset, error) {
	if contentHash == "" {
		return nil, nil
	}
	var row types.WorkbenchAsset
	err := r.db.WithContext(ctx).
		Table("workbench_item_assets").
		Select(workbenchAssetColumns).
		Where(`tenant_id = ? AND owner_id = ? AND item_id = ? AND content_hash = ?
			AND deleted_at IS NULL`, tenantID, ownerID, itemID, contentHash).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// ListAssets 按 created_at 升序返回 item 下的 live 资产（不加载存储路径以外的大字段）。
func (r *workbenchRepository) ListAssets(
	ctx context.Context, tenantID uint64, ownerID, itemID string,
) ([]*types.WorkbenchAsset, error) {
	rows := make([]*types.WorkbenchAsset, 0, 8)
	err := r.db.WithContext(ctx).
		Table("workbench_item_assets").
		Select(workbenchAssetColumns).
		Where("tenant_id = ? AND owner_id = ? AND item_id = ? AND deleted_at IS NULL",
			tenantID, ownerID, itemID).
		Order("created_at ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// CountAssets 统计 item 下的 live 资产数（配额检查用）。
func (r *workbenchRepository) CountAssets(
	ctx context.Context, tenantID uint64, ownerID, itemID string,
) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Table("workbench_item_assets").
		Where("tenant_id = ? AND owner_id = ? AND item_id = ? AND deleted_at IS NULL",
			tenantID, ownerID, itemID).
		Count(&count).Error
	return count, err
}

// TouchAsset 更新 last_seen_at（引用时间），返回影响行数。
//
// 服务端不解析 Y.Doc，因此“资产是否仍被引用”只能靠客户端在打开文档后
// 对实际引用的 asset 逐个上报；超过保留期仍未上报的由清理任务回收。
func (r *workbenchRepository) TouchAsset(
	ctx context.Context, tenantID uint64, ownerID, id string, seenAt time.Time,
) (int64, error) {
	result := r.db.WithContext(ctx).
		Table("workbench_item_assets").
		Where("id = ? AND tenant_id = ? AND owner_id = ? AND deleted_at IS NULL",
			id, tenantID, ownerID).
		Update("last_seen_at", seenAt)
	return result.RowsAffected, result.Error
}

// SoftDeleteAsset 软删资产行，返回影响行数；0 行表示不存在或已删。
func (r *workbenchRepository) SoftDeleteAsset(
	ctx context.Context, tenantID uint64, ownerID, id string,
) (int64, error) {
	result := r.db.WithContext(ctx).
		Table("workbench_item_assets").
		Where("id = ? AND tenant_id = ? AND owner_id = ? AND deleted_at IS NULL",
			id, tenantID, ownerID).
		Update("deleted_at", time.Now().UTC())
	return result.RowsAffected, result.Error
}

// ListStaleAssets 跨用户取 last_seen_at 早于 cutoff 的 live 资产（清理任务用）。
//
// item_id 为空的行视为“从未被上报过引用”，统一按 created_at 判断：
// 上传后从未打开过的附件不会被误删——只要它仍在保留期内。
func (r *workbenchRepository) ListStaleAssets(
	ctx context.Context, cutoff time.Time, limit int,
) ([]*types.WorkbenchAsset, error) {
	rows := make([]*types.WorkbenchAsset, 0, limit)
	query := r.db.WithContext(ctx).
		Table("workbench_item_assets").
		Select(workbenchAssetColumns).
		Where(`deleted_at IS NULL AND COALESCE(last_seen_at, created_at) < ?`, cutoff).
		Order("COALESCE(last_seen_at, created_at) ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ListAssetPathsByItem 返回某 item 全部资产（含已软删）的 storage_path。
//
// 硬删条目时必须先取这份列表：PurgeItem 会连带删掉 asset 行，
// 之后再也查不到文件路径，物理文件就成了永久孤儿。
// 必须 Unscoped：WorkbenchAsset 带 gorm.DeletedAt，默认查询会自动过滤软删行，
// 而软删行的文件同样要回收。
func (r *workbenchRepository) ListAssetPathsByItem(
	ctx context.Context, tenantID uint64, ownerID, itemID string,
) ([]string, error) {
	rows := make([]*types.WorkbenchAsset, 0, 8)
	if err := r.db.WithContext(ctx).
		Unscoped().
		Table("workbench_item_assets").
		Select("storage_path").
		Where("tenant_id = ? AND owner_id = ? AND item_id = ?", tenantID, ownerID, itemID).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.StoragePath != "" {
			paths = append(paths, row.StoragePath)
		}
	}
	return paths, nil
}

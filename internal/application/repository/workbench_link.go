package repository

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// 反向链接仓储（P4-2「页内提及」）。
//
// workbench_item_links 从 000136 建表以来一直没有写入方，这里是第一个。
// 提及匹配用 `text LIKE '%title%'` 下推到数据库：一次查询完成全部候选匹配，
// 不在应用层做 O(标题数 × 文本长度) 的循环。LIKE 在 SQLite 与 PostgreSQL
// 下对 ASCII 都不区分大小写，中文标题本身无大小写问题。
//
// 边界（防误报/防越权）：
//   - 单字标题不参与匹配（types.WorkbenchMentionTitleMinRunes）；
//   - 永远排除 source 自己（自己提自己不是反向链接）；
//   - 关联行带 owner_id，ReplaceItemLinks 只删写自己的行。

// ListItemIDsMentioning 找出标题出现在 text 里的其它条目。
func (r *workbenchRepository) ListItemIDsMentioning(
	ctx context.Context, tenantID uint64, ownerID, sourceItemID, text string, limit int,
) ([]string, error) {
	if limit <= 0 {
		limit = types.WorkbenchItemMentionsLimit
	}
	ids := make([]string, 0)
	err := r.db.WithContext(ctx).
		Table("workbench_items").
		Select("id").
		Where(`tenant_id = ? AND owner_id = ? AND deleted_at IS NULL AND id <> ?
			AND length(title) >= ? AND ? LIKE '%' || title || '%'`,
			tenantID, ownerID, sourceItemID, types.WorkbenchMentionTitleMinRunes, text).
		Limit(limit).
		Pluck("id", &ids).Error
	return ids, err
}

// ReplaceItemLinks 覆盖式重写 source 的 item 引用。
//
// 只删 target_type='item' 的行：将来若加入其它 target 类型（doc/knowledge），
// 它们的生命周期不该被条目保存顺带清空。
func (r *workbenchRepository) ReplaceItemLinks(
	ctx context.Context, tenantID uint64, ownerID, sourceItemID string, targets []types.WorkbenchLinkTarget,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("workbench_item_links").
			Where("source_item_id = ? AND tenant_id = ? AND owner_id = ? AND target_type = ?",
				sourceItemID, tenantID, ownerID, types.WorkbenchLinkTargetItem).
			Delete(nil).Error; err != nil {
			return err
		}
		if len(targets) == 0 {
			return nil
		}
		now := time.Now().UTC()
		rows := make([]map[string]any, 0, len(targets))
		for _, target := range targets {
			if target.TargetID == "" || target.TargetType == "" {
				continue
			}
			rows = append(rows, map[string]any{
				// 主键无默认值，必须显式生成
				"id":             uuid.NewString(),
				"tenant_id":      tenantID,
				"owner_id":       ownerID,
				"source_item_id": sourceItemID,
				"target_type":    target.TargetType,
				"target_id":      target.TargetID,
				"created_at":     now,
			})
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.Table("workbench_item_links").Create(&rows).Error
	})
}

// ListBacklinks 谁引用了 targetItem。
//
// JOIN 回 workbench_items 取引用方的标题等摘要；引用方被删除后
// （deleted_at 非空）自然消失，不会留「幽灵引用」。
func (r *workbenchRepository) ListBacklinks(
	ctx context.Context, tenantID uint64, ownerID, targetItemID string, limit int,
) ([]*types.WorkbenchBacklink, error) {
	if limit <= 0 || limit > types.WorkbenchBacklinksMaxLimit {
		limit = types.WorkbenchBacklinksMaxLimit
	}
	rows := make([]*types.WorkbenchBacklink, 0)
	err := r.db.WithContext(ctx).
		Table("workbench_item_links AS l").
		Select("i.id, i.title, i.kind, i.updated_at").
		Joins("JOIN workbench_items AS i ON i.id = l.source_item_id AND i.tenant_id = l.tenant_id AND i.owner_id = l.owner_id").
		Where("l.tenant_id = ? AND l.owner_id = ? AND l.target_type = ? AND l.target_id = ? AND i.deleted_at IS NULL",
			tenantID, ownerID, types.WorkbenchLinkTargetItem, targetItemID).
		Order("i.updated_at DESC").
		Limit(limit).
		Scan(&rows).Error
	return rows, err
}

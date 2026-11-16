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
//   - 同名条目不参与匹配：标题相同通常来自同一模板自动命名，正文里出现的
//     只是模板标题，无法判断指向哪一篇，不能当作提及；
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
			AND length(title) >= ? AND ? LIKE '%' || title || '%'
			AND title <> COALESCE((SELECT title FROM workbench_items
				WHERE id = ? AND tenant_id = ? AND owner_id = ? AND deleted_at IS NULL), '')`,
			tenantID, ownerID, sourceItemID, types.WorkbenchMentionTitleMinRunes, text,
			sourceItemID, tenantID, ownerID).
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

// ListItemIDsBySharedTag 找出与 itemID 至少共享一个标签的其它条目（智能关联召回用）。
//
// 用自连接而不是应用层两段查询：一次拿到 id 列表，标签数量再多也不会放大行数
// （自连接天然按 tag_id 收敛）。标签被软删时也参与匹配没有意义，因此带上 deleted_at。
func (r *workbenchRepository) ListItemIDsBySharedTag(
	ctx context.Context, tenantID uint64, ownerID, itemID string, limit int,
) ([]string, error) {
	if limit <= 0 || limit > types.WorkbenchAIRelationsCandidatePool {
		limit = types.WorkbenchAIRelationsCandidatePool
	}
	ids := make([]string, 0, limit)
	err := r.db.WithContext(ctx).
		Table("workbench_item_tags AS mine").
		Select("DISTINCT other.item_id").
		Joins("JOIN workbench_item_tags AS other ON other.tag_id = mine.tag_id"+
			" AND other.tenant_id = mine.tenant_id AND other.owner_id = mine.owner_id").
		Joins("JOIN workbench_items AS i ON i.id = other.item_id"+
			" AND i.tenant_id = other.tenant_id AND i.owner_id = other.owner_id").
		Where("mine.tenant_id = ? AND mine.owner_id = ? AND mine.item_id = ?"+
			" AND other.item_id <> ? AND i.deleted_at IS NULL AND i.kind = ?",
			tenantID, ownerID, itemID, itemID, types.WorkbenchKindCanvas).
		Limit(limit).
		Pluck("other.item_id", &ids).Error
	return ids, err
}

// ListRelationCandidates 取智能关联的候选池：最近更新的画布条目（带正文摘要与标签）。
//
// 为什么带 text_content：列表接口的 DTO 刻意不返回正文（json:"-"），
// 而关联判定必须看主题，所以摘要只能在仓储层取。这里用 SUBSTR 只截前一段，
// 避免把 256KB 的正文投影全量拉进内存——候选池是 300 条量级。
//
// 标签单独一次查询后在应用层分组：SQLite 与 PostgreSQL 的聚合函数写法不一致，
// 两次查询比一个方言相关的 GROUP_CONCAT / STRING_AGG 更稳。
func (r *workbenchRepository) ListRelationCandidates(
	ctx context.Context, tenantID uint64, ownerID, excludeItemID string, limit int,
) ([]*types.WorkbenchRelationCandidate, error) {
	if limit <= 0 || limit > types.WorkbenchAIRelationsCandidatePool {
		limit = types.WorkbenchAIRelationsCandidatePool
	}
	digestLimit := types.WorkbenchAIRelationsDigestMaxRunes * 3 // 预留：均按 rune 截断，字节上限放宽

	type candidateRow struct {
		ID        string
		Title     string
		Digest    string
		UpdatedAt time.Time
	}
	rows := make([]*candidateRow, 0, limit)
	if err := r.db.WithContext(ctx).
		Table("workbench_items").
		Select("id, title, SUBSTR(text_content, 1, ?) AS digest, updated_at", digestLimit).
		Where("tenant_id = ? AND owner_id = ? AND deleted_at IS NULL AND kind = ? AND id <> ?",
			tenantID, ownerID, types.WorkbenchKindCanvas, excludeItemID).
		Order("updated_at DESC, id DESC").
		Limit(limit).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []*types.WorkbenchRelationCandidate{}, nil
	}

	out := make([]*types.WorkbenchRelationCandidate, 0, len(rows))
	index := make(map[string]*types.WorkbenchRelationCandidate, len(rows))
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		candidate := &types.WorkbenchRelationCandidate{
			ID:        row.ID,
			Title:     row.Title,
			Digest:    row.Digest,
			UpdatedAt: row.UpdatedAt,
			TagIDs:    []string{},
		}
		out = append(out, candidate)
		index[row.ID] = candidate
		ids = append(ids, row.ID)
	}

	// 一次查回这批候选的全部标签，再按 item_id 分组；查不到标签的条目保持空切片。
	type tagRow struct {
		ItemID string
		TagID  string
	}
	tagRows := make([]*tagRow, 0, len(ids)*2)
	if err := r.db.WithContext(ctx).
		Table("workbench_item_tags").
		Select("item_id, tag_id").
		Where("tenant_id = ? AND owner_id = ? AND item_id IN ?", tenantID, ownerID, ids).
		Scan(&tagRows).Error; err != nil {
		// 标签只作为打分权重之一，取不到就按「无标签」降级，不让整次召回失败。
		return out, nil
	}
	for _, tag := range tagRows {
		if candidate, ok := index[tag.ItemID]; ok {
			candidate.TagIDs = append(candidate.TagIDs, tag.TagID)
		}
	}
	return out, nil
}

package repository

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// workbenchItemListColumns 是列表查询用的元数据列集合，刻意不含 ydoc。
// 单条快照上限 8MB，列表最多 100 条，带上 ydoc 会把一次列表变成 800MB 的传输。
// journal_date / current_mode 随 000139（P1-9）落地，两列均可空，列在存量行上是 NULL。
const workbenchItemListColumns = `id, tenant_id, owner_id, kind, title, icon, folder_id,
	text_content, snapshot_version, editor_schema_version, visibility, is_favorite,
	journal_date, current_mode, last_opened_at, created_at, updated_at`

// workbenchItemMutableFields 是 UpdateItem 允许写的列白名单。
// 白名单之外一律丢弃，防止调用方顺手改 owner_id / snapshot_version / visibility。
// journal_date / current_mode 是 P1-9（000139）引入的列，形态与日期都只走这个入口。
var workbenchItemMutableFields = map[string]bool{
	"title":        true,
	"icon":         true,
	"is_favorite":  true,
	"folder_id":    true,
	"journal_date": true,
	"current_mode": true,
}

// CreateItem 插入条目。
//
// 用显式列名的 INSERT 而不是 db.Create：结构体上带有只读投影字段与 gorm.DeletedAt，
// 走全字段插入会试图写不存在的列。
func (r *workbenchRepository) CreateItem(
	ctx context.Context, tenantID uint64, ownerID string, item *types.WorkbenchItem,
) error {
	var folderID, lastOpenedAt any
	if item.FolderID != nil {
		folderID = *item.FolderID
	}
	if item.LastOpenedAt != nil {
		lastOpenedAt = *item.LastOpenedAt
	}
	// JournalDate 是 DateOnly：无效时必须写 NULL，不能写空字符串——
	// 空串会让 `journal_date IS NOT NULL` 成立，把普通画布错误地算进 Journals。
	var journalDate any
	if item.JournalDate.Valid {
		journalDate = item.JournalDate.Date
	}
	var currentMode any
	if item.CurrentMode != nil && *item.CurrentMode != "" {
		currentMode = *item.CurrentMode
	}
	return r.db.WithContext(ctx).Exec(`INSERT INTO workbench_items
		(id, tenant_id, owner_id, kind, title, icon, folder_id, ydoc, text_content,
		 snapshot_version, editor_schema_version, visibility, is_favorite,
		 journal_date, current_mode, last_opened_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, tenantID, ownerID, item.Kind, item.Title, item.Icon, folderID,
		item.YDoc, item.TextContent, item.SnapshotVersion, item.EditorSchemaVersion,
		item.Visibility, item.IsFavorite, journalDate, currentMode, lastOpenedAt,
		item.CreatedAt, item.UpdatedAt,
	).Error
}

// GetItem 读取完整条目（含 ydoc/text_content）。
func (r *workbenchRepository) GetItem(
	ctx context.Context, tenantID uint64, ownerID, id string,
) (*types.WorkbenchItem, error) {
	return r.getOne(ctx, tenantID, ownerID, id, true)
}

// GetItemMeta 只读元数据，不加载 ydoc，供读取接口与存在性校验使用。
func (r *workbenchRepository) GetItemMeta(
	ctx context.Context, tenantID uint64, ownerID, id string,
) (*types.WorkbenchItem, error) {
	return r.getOne(ctx, tenantID, ownerID, id, false)
}

func (r *workbenchRepository) getOne(
	ctx context.Context, tenantID uint64, ownerID, id string, withContent bool,
) (*types.WorkbenchItem, error) {
	var item types.WorkbenchItem
	q := r.db.WithContext(ctx).
		Table("workbench_items").
		Where("id = ? AND tenant_id = ? AND owner_id = ? AND deleted_at IS NULL", id, tenantID, ownerID)
	if !withContent {
		// 元数据查询不取 ydoc：单条快照上限 8MB，读列表/校验存在性时没必要拉全量；
		// text_content 是纯文本预览（≤256KB），冲突/元数据场景需要它。
		q = q.Select(workbenchItemListColumns)
	} else {
		q = q.Select(workbenchItemListColumns + ", ydoc")
	}
	if err := q.Take(&item).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

// GetTrashedItem 只查回收站内的条目；普通读路径不要用它。
func (r *workbenchRepository) GetTrashedItem(
	ctx context.Context, tenantID uint64, ownerID, id string,
) (*types.WorkbenchItem, error) {
	var item types.WorkbenchItem
	err := r.db.WithContext(ctx).
		Unscoped().
		Table("workbench_items").
		Select(workbenchItemListColumns).
		Where("id = ? AND tenant_id = ? AND owner_id = ? AND deleted_at IS NOT NULL", id, tenantID, ownerID).
		Take(&item).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// UpdateItem 只写白名单内的列，且不做乐观锁 —— snapshot_version 只保护 Y.Doc 内容，
// 重命名/移动/收藏不应该和内容保存互相触发 409。
func (r *workbenchRepository) UpdateItem(
	ctx context.Context, tenantID uint64, ownerID, id string,
	fields map[string]any, moveToRoot bool,
) (*types.WorkbenchItem, error) {
	updates := make(map[string]any, len(fields)+2)
	for key, value := range fields {
		if !workbenchItemMutableFields[key] {
			continue
		}
		// journal_date 的三态里「取消钉」必须写 NULL；GORM 的 map 更新会把 nil
		// 当成“不更新”，所以这里换成显式 NULL 表达式。
		if key == "journal_date" && value == nil {
			updates[key] = gorm.Expr("NULL")
			continue
		}
		updates[key] = value
	}
	if moveToRoot {
		// GORM 的 map 更新会把 nil 当成“不更新”，必须显式写 NULL 才能把条目移出文件夹。
		updates["folder_id"] = gorm.Expr("NULL")
	}
	if len(updates) == 0 {
		return r.GetItemMeta(ctx, tenantID, ownerID, id)
	}
	updates["updated_at"] = time.Now().UTC()

	res := r.db.WithContext(ctx).
		Table("workbench_items").
		Where("id = ? AND tenant_id = ? AND owner_id = ? AND deleted_at IS NULL", id, tenantID, ownerID).
		Updates(updates)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		// 回收站中的条目也走这里：按“不存在”处理，要求先 restore。
		return nil, gorm.ErrRecordNotFound
	}
	return r.GetItemMeta(ctx, tenantID, ownerID, id)
}

// UpdateItemContent 用一条带条件的 UPDATE 做乐观锁写入，避免先读后写。
func (r *workbenchRepository) UpdateItemContent(
	ctx context.Context, tenantID uint64, ownerID, id string,
	baseVersion int, force bool, ydoc []byte, text string, schemaVersion int,
) (int, time.Time, error) {
	now := time.Now().UTC()
	sql := `UPDATE workbench_items
		SET ydoc = ?, text_content = ?, snapshot_version = snapshot_version + 1,
			editor_schema_version = ?, updated_at = ?
		WHERE id = ? AND tenant_id = ? AND owner_id = ? AND deleted_at IS NULL`
	args := []any{ydoc, text, schemaVersion, now, id, tenantID, ownerID}
	if !force {
		// 非 force 才校验版本；force 是用户在 409 对话框里点“覆盖”后的显式意图。
		sql += " AND snapshot_version = ?"
		args = append(args, baseVersion)
	}
	res := r.db.WithContext(ctx).Exec(sql, args...)
	if res.Error != nil {
		return 0, time.Time{}, res.Error
	}
	if res.RowsAffected == 0 {
		// 0 行有两种可能，必须区分：条目根本不存在/越权 → NotFound；
		// 条目存在但版本不匹配 → 版本冲突（带回当前版本供前端重试）。
		current, err := r.GetItemMeta(ctx, tenantID, ownerID, id)
		if err != nil {
			return 0, time.Time{}, err
		}
		return current.SnapshotVersion, current.UpdatedAt, types.ErrWorkbenchVersionConflict
	}
	// 回读新版本号，不依赖 RETURNING，保证 SQLite 与 PG 行为一致。
	current, err := r.GetItemMeta(ctx, tenantID, ownerID, id)
	if err != nil {
		return 0, time.Time{}, err
	}
	return current.SnapshotVersion, current.UpdatedAt, nil
}

// ListItems 按 filter 分页。cursor 是 "updated_at|id" 的 base64，用于稳定翻页。
func (r *workbenchRepository) ListItems(
	ctx context.Context, tenantID uint64, ownerID string, filter types.WorkbenchItemFilter,
) ([]*types.WorkbenchItem, bool, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = types.WorkbenchItemsDefaultLimit
	}
	if limit > types.WorkbenchItemsMaxLimit {
		limit = types.WorkbenchItemsMaxLimit
	}

	db := r.db.WithContext(ctx).Table("workbench_items")
	if filter.Trashed {
		db = db.Unscoped().Where("deleted_at IS NOT NULL")
	} else {
		db = db.Where("deleted_at IS NULL")
	}
	db = db.Where("tenant_id = ? AND owner_id = ?", tenantID, ownerID)

	if filter.Kind != "" {
		db = db.Where("kind = ?", filter.Kind)
	}
	if filter.FolderID != nil {
		db = db.Where("folder_id = ?", *filter.FolderID)
	}
	if filter.Favorite != nil {
		db = db.Where("is_favorite = ?", *filter.Favorite)
	}
	if filter.Query != "" {
		// P3 换成 FTS；当前对标题与纯文本投影做 LIKE，命中率优先于性能。
		like := "%" + escapeLike(filter.Query) + "%"
		db = db.Where("(title LIKE ? ESCAPE '\\' OR text_content LIKE ? ESCAPE '\\')", like, like)
	}
	// Journal 只返回钉了日期的画布条目：日期只挂 workbench_items，
	// 章节文档不参与 Journals（§3.2 第 3 条）。
	if filter.Journal != nil && *filter.Journal {
		db = db.Where("kind = ? AND journal_date IS NOT NULL", types.WorkbenchKindCanvas)
	}

	cursor, err := decodeItemsCursor(filter.Cursor)
	if err != nil {
		return nil, false, err
	}
	if cursor != nil {
		// 与 ORDER BY 一一对应：updated_at 降序，同刻再按 id 降序。
		db = db.Where("(updated_at < ? OR (updated_at = ? AND id < ?))",
			cursor.updatedAt, cursor.updatedAt, cursor.id)
	}

	var items []*types.WorkbenchItem
	// 多取一条判断是否还有下一页，返回前裁掉。
	if err := db.Select(workbenchItemListColumns).
		Order("updated_at DESC, id DESC").
		Limit(limit + 1).
		Find(&items).Error; err != nil {
		return nil, false, err
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	return items, hasMore, nil
}

// ListByJournalMonth 只返回画布条目并按日期升序，不分页（调用方负责 500 条截断）。
func (r *workbenchRepository) ListByJournalMonth(
	ctx context.Context, tenantID uint64, ownerID, start, end string,
) ([]*types.WorkbenchItem, error) {
	items := make([]*types.WorkbenchItem, 0)
	err := r.db.WithContext(ctx).
		Table("workbench_items").
		Select(workbenchItemListColumns).
		Where("tenant_id = ? AND owner_id = ? AND kind = ? AND deleted_at IS NULL",
			tenantID, ownerID, types.WorkbenchKindCanvas).
		Where("journal_date BETWEEN ? AND ?", start, end).
		Order("journal_date ASC, created_at ASC").
		Find(&items).Error
	return items, err
}

// TrashItem 写 deleted_at。已在回收站的条目不重复写，避免变相延长 30 天保留期。
func (r *workbenchRepository) TrashItem(
	ctx context.Context, tenantID uint64, ownerID, id string,
) error {
	res := r.db.WithContext(ctx).
		Table("workbench_items").
		Where("id = ? AND tenant_id = ? AND owner_id = ? AND deleted_at IS NULL", id, tenantID, ownerID).
		Update("deleted_at", time.Now().UTC())
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// workbenchItemChildTables 是随条目一起删除的从属表，以及各表的关联列名。
// workbench_item_links 用的是 source_item_id，不能统一按 item_id 删。
var workbenchItemChildTables = []struct {
	table  string
	column string
}{
	{"workbench_item_versions", "item_id"},
	{"workbench_item_links", "source_item_id"},
	{"workbench_item_assets", "item_id"},
}

// PurgeItem 硬删条目及其从属行。存储文件由服务层在事务成功后删。
func (r *workbenchRepository) PurgeItem(
	ctx context.Context, tenantID uint64, ownerID, id string,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := purgeItemChildren(tx, tenantID, ownerID, []string{id}); err != nil {
			return err
		}
		res := tx.Unscoped().Table("workbench_items").
			Where("id = ? AND tenant_id = ? AND owner_id = ?", id, tenantID, ownerID).
			Delete(nil)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

// purgeItemChildren 按各自的关联列删除条目的从属行。
// tenantID/ownerID 传 0/"" 表示不按归属过滤 —— 只用于清理任务：
// 那里的 itemIDs 来自已按 deleted_at 筛过的行，且 id 本身全局唯一。
func purgeItemChildren(tx *gorm.DB, tenantID uint64, ownerID string, itemIDs []string) error {
	for _, child := range workbenchItemChildTables {
		q := tx.Table(child.table)
		if tenantID != 0 && ownerID != "" {
			q = q.Where("tenant_id = ? AND owner_id = ?", tenantID, ownerID)
		}
		if err := q.Where(fmt.Sprintf("%s IN ?", child.column), itemIDs).
			Delete(nil).Error; err != nil {
			return err
		}
	}
	return nil
}

// RestoreItem 清空 deleted_at 并返回恢复后的条目。
func (r *workbenchRepository) RestoreItem(
	ctx context.Context, tenantID uint64, ownerID, id string,
) (*types.WorkbenchItem, error) {
	res := r.db.WithContext(ctx).
		Unscoped().
		Table("workbench_items").
		Where("id = ? AND tenant_id = ? AND owner_id = ? AND deleted_at IS NOT NULL", id, tenantID, ownerID).
		Update("deleted_at", nil)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return r.GetItemMeta(ctx, tenantID, ownerID, id)
}

// TouchLastOpened 只更新 last_opened_at，不影响 updated_at（排序不被“打开”打乱）。
func (r *workbenchRepository) TouchLastOpened(
	ctx context.Context, tenantID uint64, ownerID, id string,
) error {
	return r.db.WithContext(ctx).
		Table("workbench_items").
		Where("id = ? AND tenant_id = ? AND owner_id = ? AND deleted_at IS NULL", id, tenantID, ownerID).
		Update("last_opened_at", time.Now().UTC()).Error
}

// CountItems 统计未进回收站的条目数。
func (r *workbenchRepository) CountItems(
	ctx context.Context, tenantID uint64, ownerID string,
) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Table("workbench_items").
		Where("tenant_id = ? AND owner_id = ? AND deleted_at IS NULL", tenantID, ownerID).
		Count(&count).Error
	return count, err
}

// PurgeTrashedBefore 硬删回收站中 deleted_at 早于 cutoff 的条目（每次最多 limit 条）。
func (r *workbenchRepository) PurgeTrashedBefore(
	ctx context.Context, cutoff time.Time, limit int,
) (int64, error) {
	if limit <= 0 {
		limit = 100
	}
	var ids []string
	if err := r.db.WithContext(ctx).
		Table("workbench_items").
		Unscoped().
		Where("deleted_at IS NOT NULL AND deleted_at < ?", cutoff).
		Order("deleted_at ASC").
		Limit(limit).
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	var purged int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := purgeItemChildren(tx, 0, "", ids); err != nil {
			return err
		}
		res := tx.Unscoped().Table("workbench_items").Where("id IN ?", ids).Delete(nil)
		if res.Error != nil {
			return res.Error
		}
		purged = res.RowsAffected
		return nil
	})
	return purged, err
}

// itemsCursor 是 "updated_at|id" 的翻页锚点。
type itemsCursor struct {
	updatedAt time.Time
	id        string
}

func decodeItemsCursor(raw string) (*itemsCursor, error) {
	if raw == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: cursor is not valid base64", types.ErrWorkbenchInvalidCursor)
	}
	parts := strings.SplitN(string(decoded), "|", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("%w: cursor must be updated_at|id", types.ErrWorkbenchInvalidCursor)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: cursor timestamp is invalid", types.ErrWorkbenchInvalidCursor)
	}
	if parts[1] == "" {
		return nil, fmt.Errorf("%w: cursor id is empty", types.ErrWorkbenchInvalidCursor)
	}
	return &itemsCursor{updatedAt: updatedAt.UTC(), id: parts[1]}, nil
}

// EncodeItemsCursor 把一页的最后一条编码成 next_cursor；服务层直接调用。
func EncodeItemsCursor(updatedAt time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(updatedAt.UTC().Format(time.RFC3339Nano) + "|" + id))
}

// escapeLike 转义 LIKE 通配符，避免用户输入的 % 变成全表匹配。
func escapeLike(s string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")
	return replacer.Replace(s)
}

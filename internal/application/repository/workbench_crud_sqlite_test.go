package repository

import (
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// workbenchFolderDDL 内联 000136 的 workbench_folders 表。
const workbenchFolderDDL = `
CREATE TABLE IF NOT EXISTS workbench_folders (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    owner_id VARCHAR(36) NOT NULL,
    name TEXT NOT NULL,
    parent_id VARCHAR(36),
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    deleted_at DATETIME
);
`

func setupWorkbenchFolderTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupWorkbenchTestDB(t)
	require.NoError(t, db.Exec(workbenchFolderDDL).Error)
	return db
}

func newTestFolder(t *testing.T, repo *workbenchRepository, tenantID uint64, ownerID string, parentID *string) *types.WorkbenchFolder {
	t.Helper()
	now := time.Now().UTC()
	folder := &types.WorkbenchFolder{
		ID:        uuid.NewString(),
		Name:      "folder",
		ParentID:  parentID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	require.NoError(t, repo.CreateFolder(t.Context(), tenantID, ownerID, folder))
	return folder
}

// 元数据更新不做乐观锁：改标题不影响 snapshot_version，也不会和内容保存互相 409。
func TestWorkbenchUpdateItem_DoesNotTouchSnapshotVersion(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}
	item := newTestItem(t, db, 1, "user-a")
	ctx := t.Context()

	_, _, err := repo.UpdateItemContent(ctx, 1, "user-a", item.ID, 0, false, []byte("doc"), "text", 1)
	require.NoError(t, err)

	updated, err := repo.UpdateItem(ctx, 1, "user-a", item.ID, map[string]any{"title": "renamed"}, false)
	require.NoError(t, err)
	assert.Equal(t, "renamed", updated.Title)
	assert.Equal(t, 1, updated.SnapshotVersion, "改元数据不得推进内容版本")

	// 白名单外的字段必须被丢弃，不能顺手改 owner_id / visibility。
	updated, err = repo.UpdateItem(ctx, 1, "user-a", item.ID, map[string]any{
		"owner_id": "user-b", "visibility": "workspace", "snapshot_version": 99,
	}, false)
	require.NoError(t, err)
	assert.Equal(t, 1, updated.SnapshotVersion)
	assert.Equal(t, types.WorkbenchVisibilityPrivate, updated.Visibility)
	stored, err := repo.GetItemMeta(ctx, 1, "user-a", item.ID)
	require.NoError(t, err)
	assert.Equal(t, "user-a", stored.OwnerID)
}

// move_to_root 必须真的把 folder_id 写成 NULL，而不是“不更新”。
func TestWorkbenchUpdateItem_MoveToRootWritesNull(t *testing.T) {
	db := setupWorkbenchFolderTestDB(t)
	repo := &workbenchRepository{db: db}
	folder := newTestFolder(t, repo, 1, "user-a", nil)
	item := newTestItem(t, db, 1, "user-a")
	ctx := t.Context()

	_, err := repo.UpdateItem(ctx, 1, "user-a", item.ID, map[string]any{"folder_id": folder.ID}, false)
	require.NoError(t, err)
	moved, err := repo.GetItemMeta(ctx, 1, "user-a", item.ID)
	require.NoError(t, err)
	require.NotNil(t, moved.FolderID)
	assert.Equal(t, folder.ID, *moved.FolderID)

	_, err = repo.UpdateItem(ctx, 1, "user-a", item.ID, map[string]any{"folder_id": folder.ID}, true)
	require.NoError(t, err)
	rooted, err := repo.GetItemMeta(ctx, 1, "user-a", item.ID)
	require.NoError(t, err)
	assert.Nil(t, rooted.FolderID, "move_to_root 必须把 folder_id 置为 NULL")
}

// 回收站：软删 → 普通读路径 404 → 回收站可查 → 恢复。
func TestWorkbenchTrashAndRestore(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}
	item := newTestItem(t, db, 1, "user-a")
	ctx := t.Context()

	require.NoError(t, repo.TrashItem(ctx, 1, "user-a", item.ID))

	_, err := repo.GetItem(ctx, 1, "user-a", item.ID)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound), "回收站条目对普通读路径不可见")

	trashed, err := repo.GetTrashedItem(ctx, 1, "user-a", item.ID)
	require.NoError(t, err)
	assert.Equal(t, item.ID, trashed.ID)

	// 重复软删必须 NotFound：避免刷新 deleted_at 变相延长保留期。
	assert.True(t, errors.Is(repo.TrashItem(ctx, 1, "user-a", item.ID), gorm.ErrRecordNotFound))

	restored, err := repo.RestoreItem(ctx, 1, "user-a", item.ID)
	require.NoError(t, err)
	assert.Equal(t, item.ID, restored.ID)
	_, err = repo.GetItem(ctx, 1, "user-a", item.ID)
	require.NoError(t, err)
}

// 永久删除要连从属行一起删，且跨 owner 不可执行。
func TestWorkbenchPurgeItemRemovesChildren(t *testing.T) {
	db := setupWorkbenchFolderTestDB(t)
	require.NoError(t, db.Exec(`
CREATE TABLE IF NOT EXISTS workbench_item_links (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    owner_id VARCHAR(36) NOT NULL,
    source_item_id VARCHAR(36) NOT NULL,
    target_type VARCHAR(16) NOT NULL,
    target_id VARCHAR(36) NOT NULL,
    source_block_id VARCHAR(64),
    created_at DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS workbench_item_assets (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    owner_id VARCHAR(36) NOT NULL,
    item_id VARCHAR(36) NOT NULL,
    storage_path TEXT NOT NULL,
    original_name TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    content_hash TEXT,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    last_seen_at DATETIME,
    created_at DATETIME NOT NULL,
    deleted_at DATETIME
);`).Error)
	repo := &workbenchRepository{db: db}
	item := newTestItem(t, db, 1, "user-a")
	ctx := t.Context()

	now := time.Now().UTC()
	require.NoError(t, db.Exec(`INSERT INTO workbench_item_versions
		(id, item_id, tenant_id, owner_id, version, editor_schema_version, created_at)
		VALUES (?, ?, 1, 'user-a', 1, 1, ?)`, uuid.NewString(), item.ID, now).Error)
	require.NoError(t, db.Exec(`INSERT INTO workbench_item_links
		(id, tenant_id, owner_id, source_item_id, target_type, target_id, created_at)
		VALUES (?, 1, 'user-a', ?, 'item', ?, ?)`, uuid.NewString(), item.ID, "t", now).Error)
	require.NoError(t, db.Exec(`INSERT INTO workbench_item_assets
		(id, tenant_id, owner_id, item_id, storage_path, original_name, mime_type, created_at)
		VALUES (?, 1, 'user-a', ?, 'p', 'n', 'image/png', ?)`, uuid.NewString(), item.ID, now).Error)

	// 越权 purge 必须失败，且不删任何行。
	assert.True(t, errors.Is(repo.PurgeItem(ctx, 1, "user-b", item.ID), gorm.ErrRecordNotFound))

	require.NoError(t, repo.PurgeItem(ctx, 1, "user-a", item.ID))
	for _, table := range []string{"workbench_item_versions", "workbench_item_links", "workbench_item_assets"} {
		var count int64
		column := "item_id"
		if table == "workbench_item_links" {
			column = "source_item_id"
		}
		require.NoError(t, db.Table(table).Where(column+" = ?", item.ID).Count(&count).Error)
		assert.Zerof(t, count, "%s 的从属行应被一并删除", table)
	}
}

// 列表分页：cursor 翻页不重不漏，且不含 ydoc。
func TestWorkbenchListItemsPagination(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}
	ctx := t.Context()

	const total = 5
	for i := 0; i < total; i++ {
		item := newTestItem(t, db, 1, "user-a")
		// 让 updated_at 严格递增，避免同刻排序不稳定。
		require.NoError(t, db.Table("workbench_items").Where("id = ?", item.ID).
			Update("updated_at", time.Now().UTC().Add(time.Duration(i)*time.Second)).Error)
	}
	// 另一用户的数据不得出现在结果里。
	newTestItem(t, db, 1, "user-b")

	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < total; page++ {
		items, hasMore, err := repo.ListItems(ctx, 1, "user-a", types.WorkbenchItemFilter{
			Limit: 2, Cursor: cursor,
		})
		require.NoError(t, err)
		require.NotEmpty(t, items)
		for _, it := range items {
			assert.Falsef(t, seen[it.ID], "分页不得重复返回 %s", it.ID)
			seen[it.ID] = true
			assert.Empty(t, it.YDoc, "列表不得加载 ydoc")
		}
		if !hasMore {
			break
		}
		cursor = EncodeWorkbenchItemsCursor(items[len(items)-1], types.WorkbenchSortUpdated)
	}
	assert.Len(t, seen, total)
}

// 非法 cursor 必须报错，不能静默返回第一页。
func TestWorkbenchListItemsRejectsInvalidCursor(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}
	_, _, err := repo.ListItems(t.Context(), 1, "user-a", types.WorkbenchItemFilter{Cursor: "not-base64!!"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.ErrWorkbenchInvalidCursor))
}

// 排序（B5）：三种排序的 ORDER BY 与 cursor 必须成对生效。
func TestWorkbenchListItemsSort(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}
	ctx := t.Context()

	// 造三条数据，让三种排序给出三种不同结果：
	//   title 升序 → Alpha / Beta / Gamma
	//   created 降序 → Gamma / Alpha / Beta
	//   updated 降序 → Beta / Alpha / Gamma
	base := time.Now().UTC().Truncate(time.Second)
	fixtures := []struct {
		title     string
		createdAt time.Time
		updatedAt time.Time
	}{
		{"Beta", base, base.Add(3 * time.Second)},
		{"Alpha", base.Add(time.Second), base.Add(2 * time.Second)},
		{"Gamma", base.Add(2 * time.Second), base.Add(time.Second)},
	}
	for _, f := range fixtures {
		item := newTestItem(t, db, 1, "user-a")
		require.NoError(t, db.Table("workbench_items").Where("id = ?", item.ID).
			Updates(map[string]any{
				"title": f.title, "created_at": f.createdAt, "updated_at": f.updatedAt,
			}).Error)
	}

	titles := func(filter types.WorkbenchItemFilter) []string {
		items, _, err := repo.ListItems(ctx, 1, "user-a", filter)
		require.NoError(t, err)
		out := make([]string, 0, len(items))
		for _, it := range items {
			out = append(out, it.Title)
		}
		return out
	}

	assert.Equal(t, []string{"Alpha", "Beta", "Gamma"}, titles(types.WorkbenchItemFilter{
		Sort: types.WorkbenchSortTitle,
	}))
	assert.Equal(t, []string{"Gamma", "Alpha", "Beta"}, titles(types.WorkbenchItemFilter{
		Sort: types.WorkbenchSortCreated,
	}))
	assert.Equal(t, []string{"Beta", "Alpha", "Gamma"}, titles(types.WorkbenchItemFilter{
		Sort: types.WorkbenchSortUpdated,
	}))
	// 未知排序值退回「最近更新」，不报错。
	assert.Equal(t, []string{"Beta", "Alpha", "Gamma"}, titles(types.WorkbenchItemFilter{
		Sort: "unknown",
	}))

	// 用 updated 分页拿到的 cursor 拿去按 title 翻页必须报非法，而不是串页。
	first, hasMore, err := repo.ListItems(ctx, 1, "user-a", types.WorkbenchItemFilter{
		Sort: types.WorkbenchSortUpdated, Limit: 1,
	})
	require.NoError(t, err)
	require.True(t, hasMore)
	cursor := EncodeWorkbenchItemsCursor(first[0], types.WorkbenchSortUpdated)
	_, _, err = repo.ListItems(ctx, 1, "user-a", types.WorkbenchItemFilter{
		Sort: types.WorkbenchSortTitle, Cursor: cursor,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.ErrWorkbenchInvalidCursor))

	// 同一排序下 cursor 可正常续页：title 排序第二页应跳过 Alpha。
	page1, _, err := repo.ListItems(ctx, 1, "user-a", types.WorkbenchItemFilter{
		Sort: types.WorkbenchSortTitle, Limit: 1,
	})
	require.NoError(t, err)
	page2, _, err := repo.ListItems(ctx, 1, "user-a", types.WorkbenchItemFilter{
		Sort:   types.WorkbenchSortTitle,
		Limit:  1,
		Cursor: EncodeWorkbenchItemsCursor(page1[0], types.WorkbenchSortTitle),
	})
	require.NoError(t, err)
	require.Len(t, page2, 1)
	assert.Equal(t, "Beta", page2[0].Title)
}

// 「最近」不得出现事项：kind=database 是全局单例视图，用 ExcludeKind 挡掉。
func TestWorkbenchListItemsExcludeKind(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}
	ctx := t.Context()

	canvasItem := newTestItem(t, db, 1, "user-a")
	require.NoError(t, db.Table("workbench_items").Where("id = ?", canvasItem.ID).
		Update("title", "画布条目").Error)
	things := newTestItem(t, db, 1, "user-a")
	require.NoError(t, db.Table("workbench_items").Where("id = ?", things.ID).
		Updates(map[string]any{"title": "事项", "kind": types.WorkbenchKindDatabase}).Error)

	// 不排除时两条都在（显式按 kind 查事项仍能查到）。
	all, _, err := repo.ListItems(ctx, 1, "user-a", types.WorkbenchItemFilter{})
	require.NoError(t, err)
	assert.Len(t, all, 2)

	// 排除 database 后只剩画布条目。
	remaining, _, err := repo.ListItems(ctx, 1, "user-a", types.WorkbenchItemFilter{
		ExcludeKind: types.WorkbenchKindDatabase,
	})
	require.NoError(t, err)
	require.Len(t, remaining, 1)
	assert.Equal(t, canvasItem.ID, remaining[0].ID)
}

// 版本历史：插入去重、列表按版本倒序、恢复用「当前+1」。
func TestWorkbenchVersionHistoryAndRestore(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}
	item := newTestItem(t, db, 1, "user-a")
	ctx := t.Context()

	now := time.Now().UTC()
	for v := 1; v <= 3; v++ {
		require.NoError(t, repo.InsertVersion(ctx, &types.WorkbenchItemVersion{
			ID: uuid.NewString(), ItemID: item.ID, TenantID: 1, OwnerID: "user-a",
			Version: v, EditorSchemaVersion: 1, YDoc: []byte{'v', byte('0' + v)},
			TextContent: "text", CreatedAt: now.Add(time.Duration(v) * time.Second),
		}))
	}
	// 重复插入同版本必须静默忽略（并发 checkpoint）。
	require.NoError(t, repo.InsertVersion(ctx, &types.WorkbenchItemVersion{
		ID: uuid.NewString(), ItemID: item.ID, TenantID: 1, OwnerID: "user-a",
		Version: 3, EditorSchemaVersion: 1, YDoc: []byte("dup"), CreatedAt: now,
	}))

	versions, err := repo.ListVersions(ctx, 1, "user-a", item.ID, 10)
	require.NoError(t, err)
	require.Len(t, versions, 3, "重复版本不得重复计入")
	assert.Equal(t, 3, versions[0].Version, "按版本倒序")
	assert.Positive(t, versions[0].SizeBytes, "size_bytes 必须由 length(ydoc) 现算")

	// 先把当前内容推到 version=1，再恢复到 version=1，新版本应为 2。
	_, _, err = repo.UpdateItemContent(ctx, 1, "user-a", item.ID, 0, false, []byte("current"), "cur", 1)
	require.NoError(t, err)
	newVersion, newSchema, _, err := repo.RestoreVersion(ctx, 1, "user-a", item.ID, 1, 1)
	require.NoError(t, err)
	assert.Equal(t, 2, newVersion, "恢复必须生成「当前+1」的新版本")
	assert.Equal(t, 1, newSchema)

	stored, err := repo.GetItem(ctx, 1, "user-a", item.ID)
	require.NoError(t, err)
	assert.Equal(t, []byte("v1"), stored.YDoc, "恢复后内容应来自历史版本")
	assert.Equal(t, 2, stored.SnapshotVersion)

	// 用过期 base 恢复必须冲突。
	_, _, _, err = repo.RestoreVersion(ctx, 1, "user-a", item.ID, 1, 1)
	assert.True(t, errors.Is(err, types.ErrWorkbenchVersionConflict))
}

// PruneVersions 只保留最近 keep 个。
func TestWorkbenchPruneVersions(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}
	item := newTestItem(t, db, 1, "user-a")
	ctx := t.Context()
	now := time.Now().UTC()

	for v := 1; v <= 5; v++ {
		require.NoError(t, repo.InsertVersion(ctx, &types.WorkbenchItemVersion{
			ID: uuid.NewString(), ItemID: item.ID, TenantID: 1, OwnerID: "user-a",
			Version: v, EditorSchemaVersion: 1, CreatedAt: now.Add(time.Duration(v) * time.Second),
		}))
	}
	deleted, err := repo.PruneVersions(ctx, 1, "user-a", item.ID, 2)
	require.NoError(t, err)
	assert.EqualValues(t, 3, deleted)

	versions, err := repo.ListVersions(ctx, 1, "user-a", item.ID, 10)
	require.NoError(t, err)
	require.Len(t, versions, 2)
	assert.Equal(t, 5, versions[0].Version)
	assert.Equal(t, 4, versions[1].Version)
}

// 文件夹：越权不可读、子项上移后可删、计数正确。
func TestWorkbenchFolderOwnershipAndDelete(t *testing.T) {
	db := setupWorkbenchFolderTestDB(t)
	repo := &workbenchRepository{db: db}
	ctx := t.Context()

	parent := newTestFolder(t, repo, 1, "user-a", nil)
	child := newTestFolder(t, repo, 1, "user-a", &parent.ID)
	item := newTestItem(t, db, 1, "user-a")
	_, err := repo.UpdateItem(ctx, 1, "user-a", item.ID, map[string]any{"folder_id": parent.ID}, false)
	require.NoError(t, err)

	// 跨 owner 读不到。
	_, err = repo.GetFolder(ctx, 1, "user-b", parent.ID)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))

	// 删除前先把子文件夹与条目上移到 parent（这里 parent 是根，故移到根）。
	require.NoError(t, repo.ReparentChildren(ctx, 1, "user-a", parent.ID, nil))
	require.NoError(t, repo.MoveItemsToFolder(ctx, 1, "user-a", parent.ID, nil))
	require.NoError(t, repo.DeleteFolder(ctx, 1, "user-a", parent.ID))

	childAfter, err := repo.GetFolder(ctx, 1, "user-a", child.ID)
	require.NoError(t, err)
	assert.Nil(t, childAfter.ParentID, "子文件夹应上移到根")

	itemAfter, err := repo.GetItemMeta(ctx, 1, "user-a", item.ID)
	require.NoError(t, err)
	assert.Nil(t, itemAfter.FolderID, "条目应上移到根")

	count, err := repo.CountFolders(ctx, 1, "user-a")
	require.NoError(t, err)
	assert.EqualValues(t, 1, count, "只剩子文件夹")
}

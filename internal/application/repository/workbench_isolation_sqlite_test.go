package repository

import (
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 工作台资源的所有者隔离（G4 门禁的仓储层自动化部分）。
//
// 硬约束（实现方案 §4.1）：资源按 tenant_id + owner_id 隔离，owner 由服务端从 ctx 取；
// 他人的条目/版本/资产/归档一律「不存在」，不能泄露名称、数量、存在性。
//
// 这组用例覆盖的是**查询条件本身**：即使调用方传了别人的 id，SQL 也不会命中。
// 上层把 ErrRecordNotFound / 空结果映射成 2400（不是 403），因此这里断言的是
// 「查不到」而不是「报错」，与产品口径一致。
func setupWorkbenchIsolationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupWorkbenchTestDB(t)
	require.NoError(t, db.Exec(workbenchFolderDDL).Error)
	require.NoError(t, db.Exec(workbenchAssetDDL).Error)
	require.NoError(t, db.Exec(workbenchArchiveDDL).Error)
	return db
}

func TestWorkbenchIsolation_ItemNotVisibleToOtherOwner(t *testing.T) {
	db := setupWorkbenchIsolationDB(t)
	repo := &workbenchRepository{db: db}
	const tenant uint64 = 7

	owned := newTestItem(t, db, tenant, "owner-a")

	t.Run("本人可读", func(t *testing.T) {
		got, err := repo.GetItem(t.Context(), tenant, "owner-a", owned.ID)
		require.NoError(t, err)
		assert.Equal(t, owned.ID, got.ID)
	})

	t.Run("同租户的另一用户读不到", func(t *testing.T) {
		_, err := repo.GetItem(t.Context(), tenant, "owner-b", owned.ID)
		assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
		_, err = repo.GetItemMeta(t.Context(), tenant, "owner-b", owned.ID)
		assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	})

	t.Run("跨租户同样读不到", func(t *testing.T) {
		_, err := repo.GetItem(t.Context(), 8, "owner-a", owned.ID)
		assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	})

	t.Run("列表只返回自己的条目", func(t *testing.T) {
		items, hasMore, err := repo.ListItems(t.Context(), tenant, "owner-b", types.WorkbenchItemFilter{})
		require.NoError(t, err)
		assert.Empty(t, items)
		assert.False(t, hasMore)

		items, _, err = repo.ListItems(t.Context(), tenant, "owner-a", types.WorkbenchItemFilter{})
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Equal(t, owned.ID, items[0].ID)
	})
}

func TestWorkbenchIsolation_VersionsFilteredByOwner(t *testing.T) {
	db := setupWorkbenchIsolationDB(t)
	repo := &workbenchRepository{db: db}
	const tenant uint64 = 7

	owned := newTestItem(t, db, tenant, "owner-a")
	// 直接写一条版本行：仓储没有「建版本」的公开入口（版本由内容保存产生）
	require.NoError(t, db.Exec(`INSERT INTO workbench_item_versions
		(id, item_id, tenant_id, owner_id, version, editor_schema_version, ydoc, created_at)
		VALUES (?, ?, ?, ?, 1, 1, ?, CURRENT_TIMESTAMP)`,
		"v-1", owned.ID, tenant, "owner-a", []byte("snapshot")).Error)

	versions, err := repo.ListVersions(t.Context(), tenant, "owner-a", owned.ID, 10)
	require.NoError(t, err)
	require.Len(t, versions, 1)

	// 换 owner：同一 item id 也查不出任何版本
	versions, err = repo.ListVersions(t.Context(), tenant, "owner-b", owned.ID, 10)
	require.NoError(t, err)
	assert.Empty(t, versions)

	// 跨租户同样为空
	versions, err = repo.ListVersions(t.Context(), 8, "owner-a", owned.ID, 10)
	require.NoError(t, err)
	assert.Empty(t, versions)
}

func TestWorkbenchIsolation_AssetAndArchiveNotVisible(t *testing.T) {
	db := setupWorkbenchIsolationDB(t)
	repo := &workbenchRepository{db: db}
	const tenant uint64 = 7

	owned := newTestItem(t, db, tenant, "owner-a")
	// 资产表没有 updated_at（只记 created_at 与 last_seen_at）
	require.NoError(t, db.Exec(`INSERT INTO workbench_item_assets
		(id, tenant_id, owner_id, item_id, storage_path, original_name, mime_type, size_bytes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1024, CURRENT_TIMESTAMP)`,
		"asset-1", tenant, "owner-a", owned.ID, "path/asset-1.png", "封面.png", "image/png").Error)
	require.NoError(t, db.Exec(`INSERT INTO workbench_archives
		(id, tenant_id, owner_id, kb_id, knowledge_id, source_type, source_id, source_version,
		 part_no, part_count, title, status, archived_at, created_at, updated_at)
		VALUES (?, ?, ?, 'kb-1', 'k-1', 'item', ?, 1, 1, 1, '归档标题', 'active',
		 CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		"arc-1", tenant, "owner-a", owned.ID).Error)

	// 资产：他人拿到 nil（与「已删除」同一出口，不区分存在性）
	asset, err := repo.GetAsset(t.Context(), tenant, "owner-a", "asset-1")
	require.NoError(t, err)
	require.NotNil(t, asset)

	asset, err = repo.GetAsset(t.Context(), tenant, "owner-b", "asset-1")
	require.NoError(t, err)
	assert.Nil(t, asset)

	// 归档：他人列表为空
	archives, err := repo.ListLiveBySource(t.Context(), tenant, "owner-a", types.WorkbenchSourceItem, owned.ID)
	require.NoError(t, err)
	require.Len(t, archives, 1)

	archives, err = repo.ListLiveBySource(t.Context(), tenant, "owner-b", types.WorkbenchSourceItem, owned.ID)
	require.NoError(t, err)
	assert.Empty(t, archives)
}

func TestWorkbenchIsolation_FoldersScopedByOwner(t *testing.T) {
	db := setupWorkbenchIsolationDB(t)
	repo := &workbenchRepository{db: db}
	const tenant uint64 = 7

	folder := newTestFolder(t, repo, tenant, "owner-a", nil)

	list, err := repo.ListFolders(t.Context(), tenant, "owner-a")
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, folder.ID, list[0].ID)

	list, err = repo.ListFolders(t.Context(), tenant, "owner-b")
	require.NoError(t, err)
	assert.Empty(t, list)

	// 删别人的文件夹：必须查不到（而不是删掉）
	err = repo.DeleteFolder(t.Context(), tenant, "owner-b", folder.ID)
	if err == nil {
		// 某些实现用 RowsAffected 判定；无论哪种口径，最终都不能真的删掉
		list, listErr := repo.ListFolders(t.Context(), tenant, "owner-a")
		require.NoError(t, listErr)
		assert.Len(t, list, 1, "越权删除不能生效")
	} else {
		assert.True(t, errors.Is(err, gorm.ErrRecordNotFound), "越权删除应报 not found，实际 %v", err)
	}
}

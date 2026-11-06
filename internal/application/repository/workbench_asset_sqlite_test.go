package repository

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// workbenchAssetDDL 内联 000044 的资产表与两个索引（含唯一索引的去重语义）。
const workbenchAssetDDL = `
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
);
CREATE INDEX IF NOT EXISTS idx_wia_item
    ON workbench_item_assets (tenant_id, owner_id, item_id)
    WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_wia_item_hash
    ON workbench_item_assets (tenant_id, owner_id, item_id, content_hash)
    WHERE content_hash IS NOT NULL AND deleted_at IS NULL;
`

func setupWorkbenchAssetDB(t *testing.T) *workbenchRepository {
	t.Helper()
	db := setupWorkbenchFolderTestDB(t)
	require.NoError(t, db.Exec(workbenchAssetDDL).Error)
	return &workbenchRepository{db: db}
}

func newAsset(tenantID uint64, ownerID, itemID, hash string) *types.WorkbenchAsset {
	now := time.Now().UTC()
	return &types.WorkbenchAsset{
		ID:           uuid.NewString(),
		TenantID:     tenantID,
		OwnerID:      ownerID,
		ItemID:       itemID,
		StoragePath:  "local://1/" + itemID + "/file.bin",
		OriginalName: "file.bin",
		MimeType:     "application/octet-stream",
		ContentHash:  hash,
		SizeBytes:    128,
		CreatedAt:    now,
	}
}

// 同 item 同 hash 只能有一行 live：重复插入被唯一索引拒绝。
func TestWorkbenchAssetUniqueHashPerItem(t *testing.T) {
	repo := setupWorkbenchAssetDB(t)
	ctx := t.Context()

	require.NoError(t, repo.CreateAsset(ctx, newAsset(1, "user-a", "item-1", "hash-1")))
	err := repo.CreateAsset(ctx, newAsset(1, "user-a", "item-1", "hash-1"))
	require.Error(t, err, "同 item 同 hash 必须冲突")

	// 不同 item 允许同 hash（同一文件被粘贴到两个文档）。
	require.NoError(t, repo.CreateAsset(ctx, newAsset(1, "user-a", "item-2", "hash-1")))
	// 不同 owner 也互不影响。
	require.NoError(t, repo.CreateAsset(ctx, newAsset(1, "user-b", "item-1", "hash-1")))
}

// 软删后的行不参与唯一索引，可以重新上传同一文件。
func TestWorkbenchAssetUniqueHashIgnoresSoftDeleted(t *testing.T) {
	repo := setupWorkbenchAssetDB(t)
	ctx := t.Context()

	first := newAsset(1, "user-a", "item-1", "hash-1")
	require.NoError(t, repo.CreateAsset(ctx, first))
	affected, err := repo.SoftDeleteAsset(ctx, 1, "user-a", first.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, affected)

	// 软删后重新插入同 hash：不应冲突。
	require.NoError(t, repo.CreateAsset(ctx, newAsset(1, "user-a", "item-1", "hash-1")))
	// 但 GetLiveAssetByHash 只能拿到新行。
	live, err := repo.GetLiveAssetByHash(ctx, 1, "user-a", "item-1", "hash-1")
	require.NoError(t, err)
	require.NotNil(t, live)
	assert.NotEqual(t, first.ID, live.ID)
}

// 跨 tenant / 跨 owner 一律查不到：越权与不存在同构。
func TestWorkbenchAssetOwnerIsolation(t *testing.T) {
	repo := setupWorkbenchAssetDB(t)
	ctx := t.Context()

	asset := newAsset(1, "user-a", "item-1", "hash-1")
	require.NoError(t, repo.CreateAsset(ctx, asset))

	cases := []struct {
		name    string
		tenant  uint64
		ownerID string
	}{
		{name: "other owner", tenant: 1, ownerID: "user-b"},
		{name: "other tenant", tenant: 2, ownerID: "user-a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repo.GetAsset(ctx, tc.tenant, tc.ownerID, asset.ID)
			require.NoError(t, err)
			assert.Nil(t, got, "越权必须返回 nil 而不是报错，避免泄露存在性")

			affected, err := repo.SoftDeleteAsset(ctx, tc.tenant, tc.ownerID, asset.ID)
			require.NoError(t, err)
			assert.EqualValues(t, 0, affected)
		})
	}

	// 原 owner 仍能读到，说明上面的删除没有误伤。
	got, err := repo.GetAsset(ctx, 1, "user-a", asset.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
}

// 引用时间刷新与列表/计数。
func TestWorkbenchAssetTouchAndList(t *testing.T) {
	repo := setupWorkbenchAssetDB(t)
	ctx := t.Context()

	first := newAsset(1, "user-a", "item-1", "hash-1")
	second := newAsset(1, "user-a", "item-1", "hash-2")
	require.NoError(t, repo.CreateAsset(ctx, first))
	require.NoError(t, repo.CreateAsset(ctx, second))

	count, err := repo.CountAssets(ctx, 1, "user-a", "item-1")
	require.NoError(t, err)
	assert.EqualValues(t, 2, count)

	seenAt := time.Now().UTC().Add(time.Hour)
	affected, err := repo.TouchAsset(ctx, 1, "user-a", first.ID, seenAt)
	require.NoError(t, err)
	assert.EqualValues(t, 1, affected)

	rows, err := repo.ListAssets(ctx, 1, "user-a", "item-1")
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.NotNil(t, rows[0].LastSeenAt, "first 已刷新引用时间")
	assert.Nil(t, rows[1].LastSeenAt, "second 未上报引用")
}

// 过期扫描按 COALESCE(last_seen_at, created_at)：从未上报过的按创建时间判断。
func TestWorkbenchAssetListStale(t *testing.T) {
	repo := setupWorkbenchAssetDB(t)
	ctx := t.Context()

	stale := newAsset(1, "user-a", "item-1", "hash-old")
	stale.CreatedAt = time.Now().UTC().AddDate(0, 0, -60)
	require.NoError(t, repo.CreateAsset(ctx, stale))

	fresh := newAsset(1, "user-a", "item-2", "hash-new")
	require.NoError(t, repo.CreateAsset(ctx, fresh))

	cutoff := time.Now().UTC().AddDate(0, 0, -30)
	rows, err := repo.ListStaleAssets(ctx, cutoff, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, stale.ID, rows[0].ID)
}

// 硬删条目时先取路径：包含已软删的行，避免文件变成永久孤儿。
func TestWorkbenchAssetListPathsIncludesSoftDeleted(t *testing.T) {
	repo := setupWorkbenchAssetDB(t)
	ctx := t.Context()

	live := newAsset(1, "user-a", "item-1", "hash-live")
	deleted := newAsset(1, "user-a", "item-1", "hash-deleted")
	require.NoError(t, repo.CreateAsset(ctx, live))
	require.NoError(t, repo.CreateAsset(ctx, deleted))
	_, err := repo.SoftDeleteAsset(ctx, 1, "user-a", deleted.ID)
	require.NoError(t, err)

	paths, err := repo.ListAssetPathsByItem(ctx, 1, "user-a", "item-1")
	require.NoError(t, err)
	assert.Len(t, paths, 2, "软删行的文件也要回收")

	// 别的条目不受影响。
	otherPaths, err := repo.ListAssetPathsByItem(ctx, 1, "user-a", "item-2")
	require.NoError(t, err)
	assert.Empty(t, otherPaths)
}

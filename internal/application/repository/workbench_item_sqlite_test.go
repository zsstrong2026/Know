package repository

import (
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// workbenchTestDDL 内联 000136_workbench.up.sql 里本任务用到的两张表。
// 不用 AutoMigrate：WorkbenchItem 上的 journal_date/current_mode 等字段来自后续迁移，
// 且 PG 的 bytea/jsonb 在 SQLite 下无法直接映射，故 ydoc 用 BLOB。
const workbenchTestDDL = `
CREATE TABLE IF NOT EXISTS workbench_items (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    owner_id VARCHAR(36) NOT NULL,
    kind VARCHAR(16) NOT NULL,
    title TEXT NOT NULL,
    icon TEXT,
    folder_id VARCHAR(36),
    ydoc BLOB,
    text_content TEXT,
    snapshot_version INTEGER NOT NULL DEFAULT 0,
    editor_schema_version INTEGER NOT NULL DEFAULT 1,
    visibility VARCHAR(16) NOT NULL DEFAULT 'private',
    is_favorite BOOLEAN NOT NULL DEFAULT 0,
    journal_date TEXT,
    current_mode TEXT,
    last_opened_at DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    deleted_at DATETIME,
    thumbnail BLOB,
    thumbnail_content_type TEXT NOT NULL DEFAULT '',
    thumbnail_version INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS workbench_item_versions (
    id VARCHAR(36) PRIMARY KEY,
    item_id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    owner_id VARCHAR(36) NOT NULL,
    version INTEGER NOT NULL,
    editor_schema_version INTEGER NOT NULL DEFAULT 1,
    ydoc BLOB,
    text_content TEXT,
    created_by VARCHAR(36),
    created_at DATETIME NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_wiv_item_version ON workbench_item_versions (item_id, version);

CREATE TABLE IF NOT EXISTS workbench_notices (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    kind VARCHAR(32) NOT NULL,
    dedupe_key VARCHAR(128) NOT NULL DEFAULT '',
    payload TEXT NOT NULL DEFAULT '{}',
    read_at DATETIME,
    created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_wn_user_unread ON workbench_notices (user_id, read_at, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_wn_dedupe ON workbench_notices (user_id, dedupe_key) WHERE dedupe_key <> '';
`

func setupWorkbenchTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(workbenchTestDDL).Error)
	return db
}

// newTestItem 造一条属于 (tenantID, ownerID) 的条目，初始 snapshot_version=0。
func newTestItem(t *testing.T, db *gorm.DB, tenantID uint64, ownerID string) *types.WorkbenchItem {
	t.Helper()
	now := time.Now().UTC()
	item := &types.WorkbenchItem{
		ID:                  uuid.New().String(),
		TenantID:            tenantID,
		OwnerID:             ownerID,
		Kind:                types.WorkbenchKindCanvas,
		Title:               "test canvas",
		SnapshotVersion:     types.WorkbenchSnapshotVersionInitial,
		EditorSchemaVersion: types.SupportedEditorSchemaVersion,
		Visibility:          types.WorkbenchVisibilityPrivate,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	require.NoError(t, (&workbenchRepository{db: db}).CreateItem(t.Context(), tenantID, ownerID, item))
	return item
}

// 用例 1：连续保存两次，版本从 1 → 2 → 3，snapshot_version 单调递增。
func TestWorkbenchUpdateItemContent_IncrementsVersion(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}
	item := newTestItem(t, db, 1, "user-a")
	ctx := t.Context()

	// 首次落库把版本推到 1（DDL 默认值为 0，与 types.WorkbenchSnapshotVersionInitial 一致）。
	v1, _, err := repo.UpdateItemContent(ctx, 1, "user-a", item.ID, 0, false, []byte("doc-1"), "text-1", 1)
	require.NoError(t, err)
	assert.Equal(t, 1, v1, "首次保存应从 0 递增到 1")

	v2, _, err := repo.UpdateItemContent(ctx, 1, "user-a", item.ID, v1, false, []byte("doc-2"), "text-2", 1)
	require.NoError(t, err)
	assert.Equal(t, 2, v2, "第二次保存应递增到 2")

	v3, _, err := repo.UpdateItemContent(ctx, 1, "user-a", item.ID, v2, false, []byte("doc-3"), "text-3", 1)
	require.NoError(t, err)
	assert.Equal(t, 3, v3, "第三次保存应递增到 3")

	stored, err := repo.GetItem(ctx, 1, "user-a", item.ID)
	require.NoError(t, err)
	assert.Equal(t, 3, stored.SnapshotVersion)
	assert.Equal(t, "text-3", stored.TextContent)
	assert.Equal(t, []byte("doc-3"), stored.YDoc)
}

// 用例 2：双标签 409 且可恢复 —— A、B 都读到 version=1；
// A 用 base=1 保存成功（→2），B 用 base=1 保存必须冲突并返回 currentVersion=2；
// B 改用 base=2 重试成功（→3）。
func TestWorkbenchUpdateItemContent_TwoTabsConflictIsRecoverable(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}
	item := newTestItem(t, db, 1, "user-a")
	ctx := t.Context()

	// 先做一次落库，让两个标签页打开时看到的是 version=1。
	seedVersion, _, err := repo.UpdateItemContent(ctx, 1, "user-a", item.ID, 0, false,
		[]byte("seed"), "seed-text", 1)
	require.NoError(t, err)
	require.Equal(t, 1, seedVersion)

	// 两个标签页读到的基线版本一致。
	tabA, err := repo.GetItemMeta(ctx, 1, "user-a", item.ID)
	require.NoError(t, err)
	tabB, err := repo.GetItemMeta(ctx, 1, "user-a", item.ID)
	require.NoError(t, err)
	require.Equal(t, tabA.SnapshotVersion, tabB.SnapshotVersion)
	require.Equal(t, 1, tabA.SnapshotVersion)

	// A 先保存。
	vA, _, err := repo.UpdateItemContent(ctx, 1, "user-a", item.ID, tabA.SnapshotVersion, false,
		[]byte("A"), "A-text", 1)
	require.NoError(t, err)
	assert.Equal(t, 2, vA, "A 保存后版本应为 2")

	// B 用过期基线保存：必须冲突，且带回当前版本 2。
	vB, updatedAt, err := repo.UpdateItemContent(ctx, 1, "user-a", item.ID, tabB.SnapshotVersion, false,
		[]byte("B"), "B-text", 1)
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.ErrWorkbenchVersionConflict), "B 必须收到版本冲突")
	assert.Equal(t, 2, vB, "冲突分支必须带回当前版本")
	assert.False(t, updatedAt.IsZero(), "冲突分支必须带回当前 updated_at")

	// B 用 409 带回的版本重试：成功，版本 → 3。这一条证明 409 可恢复。
	vB2, _, err := repo.UpdateItemContent(ctx, 1, "user-a", item.ID, vB, false, []byte("B"), "B-text", 1)
	require.NoError(t, err)
	assert.Equal(t, 3, vB2, "用最新版本重试必须成功")

	stored, err := repo.GetItem(ctx, 1, "user-a", item.ID)
	require.NoError(t, err)
	assert.Equal(t, 3, stored.SnapshotVersion)
	assert.Equal(t, "B-text", stored.TextContent, "重试后落库的是 B 的内容")
}

// 用例 3：force=true 跳过版本校验，直接成功。
func TestWorkbenchUpdateItemContent_ForceSkipsVersionCheck(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}
	item := newTestItem(t, db, 1, "user-a")
	ctx := t.Context()

	_, _, err := repo.UpdateItemContent(ctx, 1, "user-a", item.ID, 0, false, []byte("v1"), "v1", 1)
	require.NoError(t, err)

	// base 明显过期（0），但 force 打开时应直接覆盖。
	v, _, err := repo.UpdateItemContent(ctx, 1, "user-a", item.ID, 0, true, []byte("forced"), "forced", 1)
	require.NoError(t, err)
	assert.Equal(t, 2, v, "force 保存照样让版本 +1")

	stored, err := repo.GetItem(ctx, 1, "user-a", item.ID)
	require.NoError(t, err)
	assert.Equal(t, "forced", stored.TextContent)
}

// 用例 4：跨租户 / 跨 owner 保存返回 NotFound，既不 409 也不泄漏存在性。
func TestWorkbenchUpdateItemContent_CrossScopeIsNotFound(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}
	item := newTestItem(t, db, 1, "user-a")
	ctx := t.Context()

	cases := []struct {
		name     string
		tenantID uint64
		ownerID  string
	}{
		{name: "other tenant", tenantID: 2, ownerID: "user-a"},
		{name: "other owner", tenantID: 1, ownerID: "user-b"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// force=true 也一样要过 scope 校验：越权不能用 force 绕过。
			_, _, err := repo.UpdateItemContent(ctx, tc.tenantID, tc.ownerID, item.ID,
				0, true, []byte("hijack"), "hijack", 1)
			require.Error(t, err)
			assert.True(t, errors.Is(err, gorm.ErrRecordNotFound),
				"跨作用域必须返回 NotFound，不能返回 409（got %v）", err)
			assert.False(t, errors.Is(err, types.ErrWorkbenchVersionConflict))

			_, getErr := repo.GetItem(ctx, tc.tenantID, tc.ownerID, item.ID)
			assert.True(t, errors.Is(getErr, gorm.ErrRecordNotFound))
		})
	}

	// 原条目未被越权写入。
	stored, err := repo.GetItem(ctx, 1, "user-a", item.ID)
	require.NoError(t, err)
	assert.Equal(t, types.WorkbenchSnapshotVersionInitial, stored.SnapshotVersion)
	assert.Empty(t, stored.TextContent, "越权写入不得生效")
}

// GetItemMeta 不加载 ydoc，但版本与内容预览仍在。
func TestWorkbenchGetItemMeta_OmitsYDoc(t *testing.T) {
	db := setupWorkbenchTestDB(t)
	repo := &workbenchRepository{db: db}
	item := newTestItem(t, db, 1, "user-a")
	ctx := t.Context()

	_, _, err := repo.UpdateItemContent(ctx, 1, "user-a", item.ID, 0, false,
		[]byte("payload"), "preview", 1)
	require.NoError(t, err)

	meta, err := repo.GetItemMeta(ctx, 1, "user-a", item.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, meta.SnapshotVersion)
	assert.Equal(t, "preview", meta.TextContent)
	assert.Empty(t, meta.YDoc, "元数据查询不得加载 ydoc")
}

package repository

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 与 000062 / 000158 保持一致的最小 DDL（测试库不跑迁移器）。
const workbenchTagDDL = `
CREATE TABLE IF NOT EXISTS workbench_tags (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    owner_id VARCHAR(36) NOT NULL,
    name VARCHAR(40) NOT NULL,
    color VARCHAR(16) NOT NULL DEFAULT 'gray',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    deleted_at DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_wt_owner_name
    ON workbench_tags (tenant_id, owner_id, name)
    WHERE deleted_at IS NULL;
CREATE TABLE IF NOT EXISTS workbench_item_tags (
    tenant_id INTEGER NOT NULL,
    owner_id VARCHAR(36) NOT NULL,
    item_id VARCHAR(36) NOT NULL,
    tag_id VARCHAR(36) NOT NULL,
    created_at DATETIME NOT NULL,
    PRIMARY KEY (item_id, tag_id)
);
CREATE INDEX IF NOT EXISTS idx_wit_tag
    ON workbench_item_tags (tenant_id, owner_id, tag_id);
`

func setupWorkbenchTagDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupWorkbenchTestDB(t)
	require.NoError(t, db.Exec(workbenchTagDDL).Error)
	return db
}

func newTestTag(t *testing.T, repo *workbenchRepository, tenantID uint64, ownerID, name string) *types.WorkbenchTag {
	t.Helper()
	tag := &types.WorkbenchTag{ID: "tag-" + name, Name: name, Color: types.WorkbenchTagColorDefault}
	require.NoError(t, repo.CreateTag(t.Context(), tenantID, ownerID, tag))
	return tag
}

func TestWorkbenchTag_CreateRejectsDuplicateName(t *testing.T) {
	db := setupWorkbenchTagDB(t)
	repo := &workbenchRepository{db: db}

	newTestTag(t, repo, 7, "owner-a", "紧急")
	err := repo.CreateTag(t.Context(), 7, "owner-a", &types.WorkbenchTag{ID: "tag-2", Name: "紧急", Color: "gray"})
	assert.ErrorIs(t, err, types.ErrWorkbenchTagNameDup)

	// 另一个用户可以同名
	require.NoError(t, repo.CreateTag(t.Context(), 7, "owner-b", &types.WorkbenchTag{ID: "tag-3", Name: "紧急", Color: "gray"}))
}

func TestWorkbenchTag_ListCountsItems(t *testing.T) {
	db := setupWorkbenchTagDB(t)
	repo := &workbenchRepository{db: db}
	const tenant uint64 = 7

	tag := newTestTag(t, repo, tenant, "owner-a", "产品")
	other := newTestTag(t, repo, tenant, "owner-a", "运营")
	item := newTestItem(t, db, tenant, "owner-a")

	require.NoError(t, repo.ReplaceItemTags(t.Context(), tenant, "owner-a", item.ID, []string{tag.ID}))

	tags, err := repo.ListTags(t.Context(), tenant, "owner-a")
	require.NoError(t, err)
	require.Len(t, tags, 2)
	// 按名称排序：产品 / 运营
	assert.Equal(t, "产品", tags[0].Name)
	assert.Equal(t, 1, tags[0].ItemCount, "打了标签的条目数要能统计出来")
	assert.Equal(t, "运营", tags[1].Name)
	assert.Equal(t, 0, tags[1].ItemCount)

	// 别人的标签看不见
	tags, err = repo.ListTags(t.Context(), tenant, "owner-b")
	require.NoError(t, err)
	assert.Empty(t, tags)

	_ = other
}

func TestWorkbenchTag_ReplaceItemTagsIsCoverAndScoped(t *testing.T) {
	db := setupWorkbenchTagDB(t)
	repo := &workbenchRepository{db: db}
	const tenant uint64 = 7

	first := newTestTag(t, repo, tenant, "owner-a", "A")
	second := newTestTag(t, repo, tenant, "owner-a", "B")
	foreign := newTestTag(t, repo, tenant, "owner-b", "外人的")
	item := newTestItem(t, db, tenant, "owner-a")

	// 覆盖式：第一次 [A]，第二次 [B, 外人的] → 只剩 B
	require.NoError(t, repo.ReplaceItemTags(t.Context(), tenant, "owner-a", item.ID, []string{first.ID}))
	require.NoError(t, repo.ReplaceItemTags(t.Context(), tenant, "owner-a", item.ID, []string{second.ID, foreign.ID}))

	tags, err := repo.ListTagsByItem(t.Context(), tenant, "owner-a", item.ID)
	require.NoError(t, err)
	require.Len(t, tags, 1)
	assert.Equal(t, second.ID, tags[0].ID, "越权的 tag id 必须被丢弃")

	// 空数组 = 清空
	require.NoError(t, repo.ReplaceItemTags(t.Context(), tenant, "owner-a", item.ID, nil))
	tags, err = repo.ListTagsByItem(t.Context(), tenant, "owner-a", item.ID)
	require.NoError(t, err)
	assert.Empty(t, tags)
}

func TestWorkbenchTag_DeleteClearsLinks(t *testing.T) {
	db := setupWorkbenchTagDB(t)
	repo := &workbenchRepository{db: db}
	const tenant uint64 = 7

	tag := newTestTag(t, repo, tenant, "owner-a", "临时")
	item := newTestItem(t, db, tenant, "owner-a")
	require.NoError(t, repo.ReplaceItemTags(t.Context(), tenant, "owner-a", item.ID, []string{tag.ID}))

	require.NoError(t, repo.DeleteTag(t.Context(), tenant, "owner-a", tag.ID))

	// 标签软删后不可再查，也不能重建同名以外的行为
	_, err := repo.GetTag(t.Context(), tenant, "owner-a", tag.ID)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	tags, err := repo.ListTagsByItem(t.Context(), tenant, "owner-a", item.ID)
	require.NoError(t, err)
	assert.Empty(t, tags, "关联行要一起清掉")

	// 同名可以重建（部分唯一索引排除了已删行）
	require.NoError(t, repo.CreateTag(t.Context(), tenant, "owner-a", &types.WorkbenchTag{
		ID: "tag-new", Name: "临时", Color: "gray",
	}))

	// 越权删除查不到
	err = repo.DeleteTag(t.Context(), tenant, "owner-b", "tag-new")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestWorkbenchTag_ListItemsByTag(t *testing.T) {
	db := setupWorkbenchTagDB(t)
	repo := &workbenchRepository{db: db}
	const tenant uint64 = 7

	tag := newTestTag(t, repo, tenant, "owner-a", "本周")
	hit := newTestItem(t, db, tenant, "owner-a")
	newTestItem(t, db, tenant, "owner-a")
	require.NoError(t, repo.ReplaceItemTags(t.Context(), tenant, "owner-a", hit.ID, []string{tag.ID}))

	items, _, err := repo.ListItems(t.Context(), tenant, "owner-a", types.WorkbenchItemFilter{TagID: tag.ID})
	require.NoError(t, err)
	require.Len(t, items, 1, "按标签过滤只返回打了该标签的条目")
	assert.Equal(t, hit.ID, items[0].ID)

	// 其它标签筛不出东西
	none := newTestTag(t, repo, tenant, "owner-a", "空标签")
	items, _, err = repo.ListItems(t.Context(), tenant, "owner-a", types.WorkbenchItemFilter{TagID: none.ID})
	require.NoError(t, err)
	assert.Empty(t, items)
}
